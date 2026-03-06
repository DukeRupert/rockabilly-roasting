package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/logging"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// --- Request/Response types ---

type subscribeConfirmRequest struct {
	PlanID          string `json:"plan_id"`
	VariantID       string `json:"variant_id"`
	Quantity        int    `json:"quantity"`
	Email           string `json:"email"`
	FirstName       string `json:"first_name"`
	LastName        string `json:"last_name"`
	Line1           string `json:"line1"`
	Line2           string `json:"line2,omitempty"`
	City            string `json:"city"`
	State           string `json:"state"`
	PostalCode      string `json:"postal_code"`
	Country         string `json:"country"`
	PaymentIntentID string `json:"payment_intent_id"`
}

type subscribeConfirmResponse struct {
	SubscriptionID string `json:"subscription_id"`
	OrderID        string `json:"order_id"`
}

// --- Handlers ---

// handleSubscribePage renders the subscription signup page for a plan + variant.
func (d *Deps) handleSubscribePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	planIDStr := r.URL.Query().Get("plan_id")
	variantIDStr := r.URL.Query().Get("variant_id")
	quantityStr := r.URL.Query().Get("quantity")

	planID, err := uuid.Parse(planIDStr)
	if err != nil {
		http.Error(w, "invalid plan_id", http.StatusBadRequest)
		return
	}
	variantID, err := uuid.Parse(variantIDStr)
	if err != nil {
		http.Error(w, "invalid variant_id", http.StatusBadRequest)
		return
	}

	quantity := 1
	if quantityStr != "" {
		quantity, err = strconv.Atoi(quantityStr)
		if err != nil || quantity < 1 || quantity > 10 {
			http.Error(w, "invalid quantity", http.StatusBadRequest)
			return
		}
	}

	var plan *domain.SubscriptionPlan
	var price int

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		plan, txErr = d.SubscriptionService.GetPlan(ctx, tx, planID)
		if txErr != nil {
			return txErr
		}
		if !plan.IsActive {
			return app.ErrSubscriptionPlanInactive
		}
		p, txErr := d.PricingService.GetBasePrice(ctx, tx, variantID, "USD")
		if txErr != nil {
			return txErr
		}
		price = p.Amount
		if plan.DiscountPct > 0 {
			price = price - (price * plan.DiscountPct / 100)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, app.ErrSubscriptionPlanNotFound) {
			http.Error(w, "subscription plan not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, app.ErrSubscriptionPlanInactive) {
			http.Error(w, "subscription plan is not active", http.StatusNotFound)
			return
		}
		if errors.Is(err, app.ErrPriceNotFound) {
			http.Error(w, "price not found for this variant", http.StatusNotFound)
			return
		}
		Error(w, r, err)
		return
	}

	props := storefront.SubscribePageProps{
		Plan:      plan,
		VariantID: variantID,
		Quantity:  quantity,
		Price:     price * quantity,
		StripeKey: os.Getenv("STRIPE_PUBLISHABLE_KEY"),
		CartCount: d.cartItemCountFromCookie(r),
	}

	if IsHTMX(r) {
		storefront.SubscribeContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.SubscribePage(props).Render(ctx, w) //nolint:errcheck
}

// handleSubscribePaymentIntent creates a PaymentIntent for a subscription's first order.
func (d *Deps) handleSubscribePaymentIntent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	var req struct {
		PlanID     string `json:"plan_id"`
		VariantID  string `json:"variant_id"`
		Quantity   int    `json:"quantity"`
		Email      string `json:"email"`
		FirstName  string `json:"first_name"`
		LastName   string `json:"last_name"`
		Line1      string `json:"line1"`
		Line2      string `json:"line2,omitempty"`
		City       string `json:"city"`
		State      string `json:"state"`
		PostalCode string `json:"postal_code"`
		Country    string `json:"country"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	planID, err := uuid.Parse(req.PlanID)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid plan_id"})
		return
	}
	variantID, err := uuid.Parse(req.VariantID)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid variant_id"})
		return
	}

	quantity := req.Quantity
	if quantity < 1 || quantity > 10 {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "quantity must be between 1 and 10"})
		return
	}

	var plan *domain.SubscriptionPlan
	var price int

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		plan, txErr = d.SubscriptionService.GetPlan(ctx, tx, planID)
		if txErr != nil {
			return txErr
		}
		p, txErr := d.PricingService.GetBasePrice(ctx, tx, variantID, "USD")
		if txErr != nil {
			return txErr
		}
		price = p.Amount
		if plan.DiscountPct > 0 {
			price = price - (price * plan.DiscountPct / 100)
		}
		return nil
	})
	if err != nil {
		logger.Error("subscribe payment-intent", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load plan"})
		return
	}

	totalPrice := price * quantity

	// Create or find Stripe customer so the payment method is saved for future charges
	var stripeCustomerID string
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		existing, txErr := d.CustomerService.GetCustomerByEmail(ctx, tx, req.Email)
		if txErr == nil && existing.StripeCustomerID != nil {
			stripeCustomerID = *existing.StripeCustomerID
		}
		return nil
	})
	if err != nil {
		logger.Error("subscribe lookup customer", "error", err)
	}

	if stripeCustomerID == "" {
		stripeCust, stripeErr := d.PaymentProvider.CreateCustomer(ctx, payments.CreateCustomerRequest{
			Email: req.Email,
			Name:  req.FirstName + " " + req.LastName,
		})
		if stripeErr != nil {
			logger.Error("subscribe create stripe customer", "error", stripeErr)
			JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create payment customer"})
			return
		}
		stripeCustomerID = stripeCust.ID
	}

	pi, err := d.PaymentProvider.CreatePaymentIntent(ctx, payments.CreatePaymentIntentRequest{
		AmountCents:      int64(totalPrice),
		Currency:         "usd",
		CustomerID:       stripeCustomerID,
		SetupFutureUsage: "off_session",
		Metadata: map[string]string{
			"plan_id":             planID.String(),
			"variant_id":          variantID.String(),
			"email":               req.Email,
			"first_name":          req.FirstName,
			"last_name":           req.LastName,
			"stripe_customer_id":  stripeCustomerID,
		},
		ShippingAddress: &payments.ShippingAddress{
			Name:       req.FirstName + " " + req.LastName,
			Line1:      req.Line1,
			Line2:      req.Line2,
			City:       req.City,
			State:      req.State,
			PostalCode: req.PostalCode,
			Country:    req.Country,
		},
	})
	if err != nil {
		logger.Error("subscribe create PI", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create payment"})
		return
	}

	JSON(w, http.StatusOK, checkoutPaymentIntentResponse{
		ClientSecret: pi.ClientSecret,
		Amount:       totalPrice,
		Currency:     "usd",
	})
}

// handleSubscribeConfirm creates the subscription and first order after payment succeeds.
func (d *Deps) handleSubscribeConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	var req subscribeConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	planID, err := uuid.Parse(req.PlanID)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid plan_id"})
		return
	}
	variantID, err := uuid.Parse(req.VariantID)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid variant_id"})
		return
	}

	quantity := req.Quantity
	if quantity < 1 || quantity > 10 {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "quantity must be between 1 and 10"})
		return
	}

	// Verify payment succeeded
	pi, err := d.PaymentProvider.GetPaymentIntent(ctx, req.PaymentIntentID)
	if err != nil {
		logger.Error("subscribe get PI", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify payment"})
		return
	}
	if pi.Status != payments.PaymentIntentStatusSucceeded {
		JSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "payment has not succeeded"})
		return
	}

	// Read Stripe customer ID from PI metadata (set during payment-intent creation)
	stripeCustomerID := pi.Metadata["stripe_customer_id"]
	if stripeCustomerID == "" {
		logger.Error("subscribe confirm: missing stripe_customer_id in PI metadata")
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "missing payment customer"})
		return
	}

	// --- DB transaction: create everything ---

	var resp subscribeConfirmResponse

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		// Get plan + price
		plan, txErr := d.SubscriptionService.GetPlan(ctx, tx, planID)
		if txErr != nil {
			return fmt.Errorf("get plan: %w", txErr)
		}

		price, txErr := d.PricingService.GetBasePrice(ctx, tx, variantID, "USD")
		if txErr != nil {
			return fmt.Errorf("get price: %w", txErr)
		}

		finalPrice := price.Amount
		if plan.DiscountPct > 0 {
			finalPrice = finalPrice - (finalPrice * plan.DiscountPct / 100)
		}

		// Find or create customer
		if req.Country == "" {
			req.Country = "US"
		}
		customer, txErr := d.CustomerService.GetCustomerByEmail(ctx, tx, req.Email)
		if txErr != nil {
			if !errors.Is(txErr, app.ErrCustomerNotFound) {
				return fmt.Errorf("lookup customer: %w", txErr)
			}
			customer, txErr = d.CustomerService.CreateGuest(ctx, tx, req.Email, req.FirstName, req.LastName)
			if txErr != nil {
				return fmt.Errorf("create guest: %w", txErr)
			}
		}

		// Save Stripe customer ID
		if customer.StripeCustomerID == nil {
			_, txErr = d.CustomerStore.UpdateStripeCustomerID(ctx, tx, customer.ID, stripeCustomerID)
			if txErr != nil {
				return fmt.Errorf("save stripe customer id: %w", txErr)
			}
		}

		// Create address
		var line2 *string
		if req.Line2 != "" {
			line2 = &req.Line2
		}
		addr, txErr := d.CustomerService.CreateAddress(ctx, tx, store.CreateAddressParams{
			CustomerID:  &customer.ID,
			FirstName:   req.FirstName,
			LastName:    req.LastName,
			Line1:       req.Line1,
			Line2:       line2,
			City:        req.City,
			State:       req.State,
			PostalCode:  req.PostalCode,
			CountryCode: req.Country,
		})
		if txErr != nil {
			return fmt.Errorf("create address: %w", txErr)
		}

		// Create subscription
		sub, txErr := d.SubscriptionService.CreateSubscription(ctx, tx, app.CreateSubscriptionParams{
			CustomerID:        customer.ID,
			PlanID:            planID,
			VariantID:         variantID,
			Quantity:          quantity,
			ShippingAddressID: addr.ID,
		}, app.Actor{
			Type: "customer",
			ID:   &customer.ID,
			Name: "subscription checkout",
		})
		if txErr != nil {
			return fmt.Errorf("create subscription: %w", txErr)
		}
		resp.SubscriptionID = sub.ID.String()

		// Place first order
		order, txErr := d.CheckoutService.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID: customer.ID,
			Items: []app.CartItem{{
				VariantID: variantID,
				Quantity:  quantity,
				UnitPrice: finalPrice,
			}},
			ShippingAddressID: addr.ID,
			BillingAddressID:  addr.ID,
			CurrencyCode:      "USD",
			SubscriptionID:    &sub.ID,
		}, app.Actor{
			Type: "customer",
			ID:   &customer.ID,
			Name: "subscription checkout",
		})
		if txErr != nil {
			return fmt.Errorf("place order: %w", txErr)
		}
		resp.OrderID = order.ID.String()

		// Store Stripe PI ID + mark payment captured
		_, txErr = d.OrderService.UpdateStripePaymentIntentID(ctx, tx, order.ID, req.PaymentIntentID)
		if txErr != nil {
			return fmt.Errorf("set stripe PI: %w", txErr)
		}
		_, txErr = d.OrderService.UpdatePaymentStatus(ctx, tx, order.ID, domain.PaymentStatusCaptured, app.Actor{
			Type: "system",
			Name: "subscription_checkout",
		})
		if txErr != nil {
			return fmt.Errorf("update payment status: %w", txErr)
		}

		// Link order to subscription
		txErr = d.SubscriptionService.LinkOrder(ctx, tx, sub.ID, order.ID, sub.CurrentPeriodStart, sub.CurrentPeriodEnd)
		if txErr != nil {
			return fmt.Errorf("link order: %w", txErr)
		}

		return nil
	})
	if err != nil {
		logger.Error("subscribe confirm", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create subscription"})
		return
	}

	JSON(w, http.StatusOK, resp)
}
