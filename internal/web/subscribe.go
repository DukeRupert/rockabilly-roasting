package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/platform/logging"
	mediapkg "github.com/dukerupert/hiri/internal/platform/media"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// --- Request/Response types ---

type subscribePaymentIntentRequest struct {
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
	// PreviousPaymentIntentID lets the client hand back the PI it is
	// abandoning (address edited after the payment element mounted) so we
	// can cancel it — which in turn cancels its pre-created order via the
	// payment_intent.canceled webhook.
	PreviousPaymentIntentID string `json:"previous_payment_intent_id,omitempty"`
}

type subscribeConfirmRequest struct {
	PaymentIntentID string `json:"payment_intent_id"`
}

type subscribeConfirmResponse struct {
	SubscriptionID string `json:"subscription_id,omitempty"`
	OrderID        string `json:"order_id"`
	// Status is "active" when the subscription exists, or "processing" when
	// payment is still settling asynchronously — the payment_intent.succeeded
	// webhook will activate the subscription once it clears.
	Status string `json:"status"`
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
	var basePrice, unitPrice int
	var productTitle, productSlug, variantLabel, thumbnailURL string

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
		basePrice = p.Amount
		unitPrice = basePrice
		if plan.DiscountPct > 0 {
			unitPrice = basePrice - (basePrice * plan.DiscountPct / 100)
		}

		// Product context so the page leads with the coffee, not the plan.
		variant, txErr := d.CatalogService.GetVariant(ctx, tx, variantID)
		if txErr != nil {
			return fmt.Errorf("get variant: %w", txErr)
		}
		product, txErr := d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
		if txErr != nil {
			return fmt.Errorf("get product: %w", txErr)
		}
		productTitle = product.Title
		productSlug = product.Slug

		if label, lErr := d.CatalogService.VariantLabel(ctx, tx, variantID); lErr == nil {
			variantLabel = label
		}
		if media, mErr := d.CatalogService.ListProductMedia(ctx, tx, variant.ProductID); mErr == nil && len(media) > 0 {
			thumbnailURL = d.MediaConfig.ProductImageURL(media[0].R2Key, mediapkg.VariantThumbnail)
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
		Plan:         plan,
		VariantID:    variantID,
		Quantity:     quantity,
		UnitPrice:    unitPrice,
		Subtotal:     unitPrice * quantity,
		BasePrice:    basePrice,
		ProductTitle: productTitle,
		ProductSlug:  productSlug,
		VariantLabel: variantLabel,
		ThumbnailURL: thumbnailURL,
		NextChargeAt: d.SubscriptionService.NextRenewalDate(time.Now(), plan),
		MerchantTZ:   d.MerchantTZ,
		StripeKey:    os.Getenv("STRIPE_PUBLISHABLE_KEY"),
		CartCount:    d.cartItemCountFromCookie(r),
	}

	if IsHTMX(r) {
		storefront.SubscribeContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.SubscribePage(props).Render(ctx, w) //nolint:errcheck
}

// subscribeContextResponse carries the signed-in customer's prefill for the
// subscribe form. Prefill is null for guests.
type subscribeContextResponse struct {
	Prefill *checkoutPrefill `json:"prefill"`
}

// handleSubscribeContext returns the session customer's contact + default
// address so the subscribe form can prefill, mirroring the checkout cart's
// prefill. Guests get an empty payload. Best-effort — never errors the page.
func (d *Deps) handleSubscribeContext(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customer, ok := auth.CustomerFromContext(ctx)
	if !ok {
		JSON(w, http.StatusOK, subscribeContextResponse{})
		return
	}

	var resp subscribeContextResponse
	_ = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		prefill, err := d.checkoutPrefillForCustomer(ctx, tx, customer)
		if err != nil {
			logging.FromContext(ctx).Warn("subscribe context: load prefill", "error", err)
			return nil // convenience only
		}
		resp.Prefill = prefill
		return nil
	})
	JSON(w, http.StatusOK, resp)
}

// handleSubscribePaymentIntent prepares a subscription signup for payment.
// Mirrors the retail checkout's pre-creation pattern so an interrupted flow
// is always recoverable from the PaymentIntent alone:
//
//  1. Load plan + price, find-or-create the customer, save the address (tx).
//  2. Ensure a Stripe customer and create the PaymentIntent (external).
//  3. Place the first order in pending+awaiting with subscription-signup
//     metadata and link it to the PI (tx).
//
// The subscription row itself is NOT created here — payment success creates
// it (ActivateFromSignupOrder), via /api/subscribe/confirm or the
// payment_intent.succeeded webhook, whichever lands first.
func (d *Deps) handleSubscribePaymentIntent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	d.Metrics.CheckoutStarted.WithLabelValues("subscribe").Inc()

	var req subscribePaymentIntentRequest
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

	req.Email = strings.TrimSpace(req.Email)
	req.FirstName = strings.TrimSpace(req.FirstName)
	req.LastName = strings.TrimSpace(req.LastName)
	req.Line1 = strings.TrimSpace(req.Line1)
	req.City = strings.TrimSpace(req.City)
	req.State = strings.TrimSpace(req.State)
	req.PostalCode = strings.TrimSpace(req.PostalCode)
	if req.Email == "" || req.FirstName == "" || req.LastName == "" ||
		req.Line1 == "" || req.City == "" || req.State == "" || req.PostalCode == "" {
		JSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error": "Fill in your email, name, and full shipping address to continue.",
		})
		return
	}
	if req.Country == "" {
		req.Country = "US"
	}

	// Phase 1: load plan + price, find-or-create customer, save address, and
	// price shipping + tax exactly like retail checkout (tx).
	var (
		plan          *domain.SubscriptionPlan
		customer      *domain.Customer
		addr          *domain.Address
		unit          int
		shippingCents int
		taxCents      int
		shipMethod    *domain.ShippingMethod
	)
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
		unit = p.Amount
		if plan.DiscountPct > 0 {
			unit = unit - (unit * plan.DiscountPct / 100)
		}

		customer, txErr = d.CustomerService.GetCustomerByEmail(ctx, tx, req.Email)
		if txErr != nil {
			if !errors.Is(txErr, app.ErrCustomerNotFound) {
				return fmt.Errorf("lookup customer: %w", txErr)
			}
			customer, txErr = d.CustomerService.CreateRetail(ctx, tx, req.Email, req.FirstName, req.LastName, nil)
			if txErr != nil {
				return fmt.Errorf("create guest customer: %w", txErr)
			}
		}

		var line2 *string
		if l2 := strings.TrimSpace(req.Line2); l2 != "" {
			line2 = &l2
		}
		actor := app.Actor{
			Type: domain.AuditActorTypeCustomer,
			ID:   &customer.ID,
			Name: customer.Email,
		}
		params := store.CreateAddressParams{
			CustomerID:  &customer.ID,
			FirstName:   req.FirstName,
			LastName:    req.LastName,
			Line1:       req.Line1,
			Line2:       line2,
			City:        req.City,
			State:       req.State,
			PostalCode:  req.PostalCode,
			CountryCode: req.Country,
		}
		// Reuse an existing matching address rather than minting a new row on
		// every PI (re)creation — this endpoint fires on each address-field
		// blur, so without dedup a single signup litters the address book.
		addr, txErr = d.findOrCreateAddress(ctx, tx, customer.ID, params, actor)
		if txErr != nil {
			return fmt.Errorf("create address: %w", txErr)
		}

		// Tax + shipping, matching retail checkout. The subscription plan
		// discount is already baked into `unit`, so the taxable subtotal is
		// the discounted line total.
		subtotal := unit * quantity
		variant, txErr := d.CatalogService.GetVariant(ctx, tx, variantID)
		if txErr != nil {
			return fmt.Errorf("get variant for tax: %w", txErr)
		}
		product, txErr := d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
		if txErr != nil {
			return fmt.Errorf("get product for tax: %w", txErr)
		}
		taxLines := []domain.TaxLineItem{{LineIndex: 0, Subtotal: subtotal, TaxExempt: product.TaxExempt}}
		isWholesale := customer.AccountType == domain.AccountTypeWholesale
		taxResult, txErr := d.CheckoutService.CalculateTax(ctx, tx, taxLines, customer.TaxExempt, isWholesale, addr.State)
		if txErr != nil {
			return fmt.Errorf("calculate tax: %w", txErr)
		}
		taxCents = taxResult.TaxTotal

		shipCents, shipCfg, txErr := d.CheckoutService.CalculateShipping(ctx, tx, subtotal, addr.PostalCode)
		if txErr != nil {
			return fmt.Errorf("calculate shipping: %w", txErr)
		}
		shippingCents = shipCents

		// No fulfillment UI on the subscribe form, so resolve the method from
		// the customer's saved preference (nil for new customers → first
		// eligible for local zips, nil otherwise = standard shipped).
		eligible := shipCfg.EligibleLocalMethods(addr.PostalCode)
		shipMethod = resolveLocalMethod(eligible, "", customer.PreferredLocalFulfillment)

		return nil
	})
	if err != nil {
		logger.Error("subscribe payment-intent: phase 1", "error", err)
		switch {
		case errors.Is(err, app.ErrSubscriptionPlanNotFound), errors.Is(err, app.ErrSubscriptionPlanInactive):
			d.Metrics.CheckoutFailed.WithLabelValues("subscribe", "validation_error").Inc()
			JSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error": "That subscription plan is no longer available. Head back to the product page and pick a current one.",
			})
		case errors.Is(err, app.ErrPriceNotFound):
			d.Metrics.CheckoutFailed.WithLabelValues("subscribe", "validation_error").Inc()
			JSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error": "We couldn't price that item. Head back to the product page and try again.",
			})
		default:
			d.Metrics.CheckoutFailed.WithLabelValues("subscribe", "internal_error").Inc()
			JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to prepare payment"})
		}
		return
	}

	totalCents := unit*quantity + shippingCents + taxCents

	// Phase 2: ensure Stripe customer + create PaymentIntent (external — no tx).
	// The Stripe customer is required so the payment method is saved for
	// future renewal charges.
	stripeCustomerID := ""
	if customer.StripeCustomerID != nil {
		stripeCustomerID = *customer.StripeCustomerID
	}
	newStripeCustomer := false
	if stripeCustomerID == "" {
		stripeCust, stripeErr := d.PaymentProvider.CreateCustomer(ctx, payments.CreateCustomerRequest{
			Email: customer.Email,
			Name:  req.FirstName + " " + req.LastName,
		})
		if stripeErr != nil {
			logger.Error("subscribe create stripe customer", "error", stripeErr)
			d.Metrics.CheckoutFailed.WithLabelValues("subscribe", "internal_error").Inc()
			JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create payment customer"})
			return
		}
		stripeCustomerID = stripeCust.ID
		newStripeCustomer = true
	}

	pi, err := d.PaymentProvider.CreatePaymentIntent(ctx, payments.CreatePaymentIntentRequest{
		AmountCents:      int64(totalCents),
		Currency:         "usd",
		CustomerID:       stripeCustomerID,
		SetupFutureUsage: "off_session",
		Metadata: map[string]string{
			"subscription_signup": "true",
			"plan_id":             planID.String(),
			"variant_id":          variantID.String(),
			"customer_id":         customer.ID.String(),
		},
		ShippingAddress: &payments.ShippingAddress{
			Name:       addr.FirstName + " " + addr.LastName,
			Line1:      addr.Line1,
			Line2:      ptrToString(addr.Line2),
			City:       addr.City,
			State:      addr.State,
			PostalCode: addr.PostalCode,
			Country:    addr.CountryCode,
		},
	})
	if err != nil {
		logger.Error("subscribe create PI", "error", err)
		d.Metrics.CheckoutFailed.WithLabelValues("subscribe", "internal_error").Inc()
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create payment"})
		return
	}

	// Phase 3: pre-create the first order in pending+awaiting and link it to
	// the PI (tx). From here on, payment success alone is enough for the
	// webhook to confirm the order and activate the subscription — no return
	// trip through the customer's browser required.
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		if newStripeCustomer {
			if txErr := d.CustomerService.LinkStripeCustomerID(ctx, tx, customer.ID, stripeCustomerID); txErr != nil {
				return fmt.Errorf("save stripe customer id: %w", txErr)
			}
		}

		order, txErr := d.CheckoutService.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID: customer.ID,
			Items: []app.CartItem{{
				VariantID: variantID,
				Quantity:  quantity,
				UnitPrice: unit,
			}},
			ShippingAddressID: addr.ID,
			BillingAddressID:  addr.ID,
			CurrencyCode:      "USD",
			ShippingCents:     shippingCents,
			TaxCents:          taxCents,
			ShippingMethod:    shipMethod,
			Metadata:          app.SubscriptionSignupOrderMetadata(planID, pi.ID),
		}, app.Actor{
			Type: domain.AuditActorTypeCustomer,
			ID:   &customer.ID,
			Name: "subscription checkout",
		})
		if txErr != nil {
			return fmt.Errorf("place signup order: %w", txErr)
		}
		if _, txErr := d.OrderService.UpdateStripePaymentIntentID(ctx, tx, order.ID, pi.ID); txErr != nil {
			return fmt.Errorf("link payment intent: %w", txErr)
		}
		return nil
	})
	if err != nil {
		logger.Error("subscribe payment-intent: phase 3", "error", err, "payment_intent_id", pi.ID)
		// Best-effort cancel the orphaned PI so the customer can retry. If
		// the cancel call fails too, Stripe auto-cancels after 48h.
		if cancelErr := d.PaymentProvider.CancelPaymentIntent(ctx, pi.ID); cancelErr != nil {
			logger.Warn("orphaned payment intent cancel failed", "payment_intent_id", pi.ID, "error", cancelErr)
		}
		d.Metrics.CheckoutFailed.WithLabelValues("subscribe", "internal_error").Inc()
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to prepare order"})
		return
	}

	// The client is abandoning its previous PI (address edited after the
	// payment element mounted). Cancel it best-effort; the
	// payment_intent.canceled webhook cancels its pre-created order. Stripe
	// refuses to cancel succeeded PIs, so a stale or malicious ID can't
	// claw back a real payment.
	if prev := strings.TrimSpace(req.PreviousPaymentIntentID); prev != "" && prev != pi.ID {
		if cancelErr := d.PaymentProvider.CancelPaymentIntent(ctx, prev); cancelErr != nil {
			logger.Warn("cancel previous subscribe PI failed", "payment_intent_id", prev, "error", cancelErr)
		}
	}

	// Return the full breakdown so the subscribe form can show what's actually
	// being charged (item + shipping + tax), not just the item price.
	shippingLabel := ""
	if shippingCents == 0 {
		shippingLabel = "Free"
	}
	JSON(w, http.StatusOK, checkoutPaymentIntentResponse{
		ClientSecret:  pi.ClientSecret,
		Amount:        totalCents,
		Currency:      "usd",
		Subtotal:      unit * quantity,
		TaxTotal:      taxCents,
		ShippingTotal: shippingCents,
		ShippingLabel: shippingLabel,
	})
}

// findOrCreateAddress reuses a customer's existing address that matches the
// submitted shipping fields (line1/line2/city/state/zip/country, case- and
// space-insensitive) instead of creating a duplicate. The subscribe
// payment-intent endpoint runs on every address-field blur, so creating a new
// row each time would fill a returning subscriber's address book with copies.
func (d *Deps) findOrCreateAddress(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, p store.CreateAddressParams, actor app.Actor) (*domain.Address, error) {
	existing, err := d.CustomerService.ListAddresses(ctx, tx, customerID)
	if err == nil {
		for i := range existing {
			a := existing[i]
			if addressMatches(a, p) {
				return &a, nil
			}
		}
	}
	return d.CustomerService.CreateAddress(ctx, tx, p, actor)
}

// addressMatches reports whether a saved address is the same shipping
// destination as the submitted params, ignoring case and surrounding space.
func addressMatches(a domain.Address, p store.CreateAddressParams) bool {
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	return norm(a.Line1) == norm(p.Line1) &&
		norm(ptrToString(a.Line2)) == norm(ptrToString(p.Line2)) &&
		norm(a.City) == norm(p.City) &&
		norm(a.State) == norm(p.State) &&
		norm(a.PostalCode) == norm(p.PostalCode) &&
		norm(a.CountryCode) == norm(p.CountryCode)
}

// handleSubscribeConfirm finalizes a subscription signup after payment. The
// order was pre-created at PaymentIntent time; this endpoint only verifies the
// PI and drives the state forward. Idempotent — if the
// payment_intent.succeeded webhook already confirmed the order and activated
// the subscription, this returns the existing IDs.
func (d *Deps) handleSubscribeConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	var req subscribeConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if strings.TrimSpace(req.PaymentIntentID) == "" {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "payment_intent_id is required"})
		return
	}

	// Verify the payment intent's status (external call — outside transaction).
	pi, err := d.PaymentProvider.GetPaymentIntent(ctx, req.PaymentIntentID)
	if err != nil {
		logger.Error("subscribe get PI", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify payment"})
		return
	}

	var resp subscribeConfirmResponse

	switch pi.Status {
	case payments.PaymentIntentStatusSucceeded:
		err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			order, transitioned, txErr := d.CheckoutService.ConfirmCheckoutPayment(ctx, tx, req.PaymentIntentID, app.Actor{
				Type: domain.AuditActorTypeSystem,
				Name: "subscribe_confirm",
			})
			if txErr != nil {
				return txErr
			}
			resp.OrderID = order.ID.String()
			resp.Status = "active"

			if !transitioned {
				// The webhook (or a concurrent confirm) won the race — the
				// subscription already exists and is stamped on the order.
				if order.SubscriptionID != nil {
					resp.SubscriptionID = order.SubscriptionID.String()
				}
				return nil
			}

			sub, txErr := d.SubscriptionService.ActivateFromSignupOrder(ctx, tx, order, app.Actor{
				Type: domain.AuditActorTypeSystem,
				Name: "subscribe_confirm",
			})
			if txErr != nil {
				return fmt.Errorf("activate signup subscription: %w", txErr)
			}
			resp.SubscriptionID = sub.ID.String()

			if _, txErr := d.RiverClient.InsertTx(ctx, tx, jobs.SubscriptionConfirmEmailArgs{
				SubscriptionID: sub.ID,
				CustomerID:     sub.CustomerID,
			}, nil); txErr != nil {
				return fmt.Errorf("enqueue subscription confirm email: %w", txErr)
			}
			return nil
		})
	case payments.PaymentIntentStatusProcessing:
		// Async path: the order stays pending+awaiting and the
		// payment_intent.succeeded webhook will confirm it and activate the
		// subscription once the payment settles.
		err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			// scoping: the Stripe PaymentIntent ID is a server-issued secret
			// the buyer's browser just received, so it acts as the ownership
			// token here (same as retail checkout confirm).
			order, txErr := d.OrderService.GetOrderByStripePaymentIntentIDAsStaff(ctx, tx, req.PaymentIntentID)
			if txErr != nil {
				return fmt.Errorf("get order for processing pi: %w", txErr)
			}
			resp.OrderID = order.ID.String()
			resp.Status = "processing"
			return nil
		})
	default:
		d.Metrics.CheckoutFailed.WithLabelValues("subscribe", "payment_failed").Inc()
		JSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "payment has not succeeded"})
		return
	}

	if err != nil {
		logger.Error("subscribe confirm", "error", err, "payment_intent_id", req.PaymentIntentID)
		if errors.Is(err, app.ErrOrderNotFound) {
			JSON(w, http.StatusNotFound, map[string]string{"error": "no order found for this payment"})
			return
		}
		d.Metrics.CheckoutFailed.WithLabelValues("subscribe", "internal_error").Inc()
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to finalize subscription"})
		return
	}

	d.Metrics.CheckoutCompleted.WithLabelValues("subscribe").Inc()
	JSON(w, http.StatusOK, resp)
}
