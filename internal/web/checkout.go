package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/logging"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// --- Request/Response types ---

type checkoutCartItem struct {
	VariantID    string `json:"variant_id"`
	ProductTitle string `json:"product_title"`
	SKU          string `json:"sku"`
	Quantity     int    `json:"quantity"`
	UnitPrice    int    `json:"unit_price"`
	LineTotal    int    `json:"line_total"`
}

type checkoutCartResponse struct {
	CartID   string             `json:"cart_id"`
	Items    []checkoutCartItem `json:"items"`
	Subtotal int                `json:"subtotal"`
	Currency string             `json:"currency"`
}

type checkoutAddressRequest struct {
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

type checkoutAddressResponse struct {
	AddressID  string `json:"address_id"`
	CustomerID string `json:"customer_id"`
}

type checkoutApplyCouponRequest struct {
	CartID string `json:"cart_id"`
	Code   string `json:"code"`
}

type checkoutApplyCouponResponse struct {
	Valid          bool   `json:"valid"`
	DiscountName   string `json:"discount_name,omitempty"`
	DiscountType   string `json:"discount_type,omitempty"`
	DiscountValue  int    `json:"discount_value,omitempty"`
	ErrorMessage   string `json:"error_message,omitempty"`
}

type checkoutRemoveCouponRequest struct {
	CartID string `json:"cart_id"`
}

type checkoutPaymentIntentRequest struct {
	CartID     string `json:"cart_id"`
	AddressID  string `json:"address_id"`
	CustomerID string `json:"customer_id"`
}

type checkoutPaymentIntentResponse struct {
	ClientSecret   string `json:"client_secret"`
	Amount         int    `json:"amount"`
	Currency       string `json:"currency"`
	Subtotal       int    `json:"subtotal"`
	DiscountTotal  int    `json:"discount_total"`
	DiscountName   string `json:"discount_name,omitempty"`
	CouponCode     string `json:"coupon_code,omitempty"`
	TaxTotal       int    `json:"tax_total"`
	TaxLabel       string `json:"tax_label,omitempty"`
}

type checkoutConfirmRequest struct {
	CartID          string `json:"cart_id"`
	CustomerID      string `json:"customer_id"`
	AddressID       string `json:"address_id"`
	PaymentIntentID string `json:"payment_intent_id"`
}

type checkoutConfirmResponse struct {
	OrderID     string `json:"order_id"`
	OrderNumber string `json:"order_number"`
}

// --- Handlers ---

// handleCheckoutPage renders the checkout page with the Svelte mount point.
func (d *Deps) handleCheckoutPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cartID := getCartID(r)

	cartIDStr := ""
	if cartID != nil {
		cartIDStr = cartID.String()
	}

	stripeKey := os.Getenv("STRIPE_PUBLISHABLE_KEY")
	cartCount := d.cartItemCountFromCookie(r)

	props := storefront.CheckoutPageProps{
		CartID:    cartIDStr,
		StripeKey: stripeKey,
		CartCount: cartCount,
	}

	if IsHTMX(r) {
		storefront.CheckoutContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.CheckoutPage(props).Render(ctx, w) //nolint:errcheck
}

// handleCheckoutCart returns cart contents for the checkout UI.
func (d *Deps) handleCheckoutCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	cartID := getCartID(r)
	if cartID == nil {
		JSON(w, http.StatusOK, checkoutCartResponse{Items: []checkoutCartItem{}})
		return
	}

	var resp checkoutCartResponse

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		cart, txErr := d.CartService.GetOrCreateCart(ctx, tx, cartID)
		if txErr != nil {
			return txErr
		}
		resp.CartID = cart.ID.String()
		resp.Currency = cart.CurrencyCode

		items, txErr := d.CartService.ListItems(ctx, tx, cart.ID)
		if txErr != nil {
			return txErr
		}

		for _, ci := range items {
			variant, vErr := d.CatalogService.GetVariant(ctx, tx, ci.VariantID)
			if vErr != nil {
				return vErr
			}
			product, pErr := d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
			if pErr != nil {
				return pErr
			}

			lineTotal := ci.UnitPrice * ci.Quantity
			resp.Subtotal += lineTotal

			resp.Items = append(resp.Items, checkoutCartItem{
				VariantID:    ci.VariantID.String(),
				ProductTitle: product.Title,
				SKU:          variant.SKU,
				Quantity:     ci.Quantity,
				UnitPrice:    ci.UnitPrice,
				LineTotal:    lineTotal,
			})
		}
		return nil
	})
	if err != nil {
		logger.Error("checkout cart", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load cart"})
		return
	}

	if resp.Items == nil {
		resp.Items = []checkoutCartItem{}
	}

	JSON(w, http.StatusOK, resp)
}

// handleCheckoutAddress validates and saves a shipping address, creating a guest customer if needed.
func (d *Deps) handleCheckoutAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	var req checkoutAddressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	validationErrors := map[string]string{}
	if strings.TrimSpace(req.Email) == "" {
		validationErrors["email"] = "Email is required"
	}
	if strings.TrimSpace(req.FirstName) == "" {
		validationErrors["first_name"] = "First name is required"
	}
	if strings.TrimSpace(req.LastName) == "" {
		validationErrors["last_name"] = "Last name is required"
	}
	if strings.TrimSpace(req.Line1) == "" {
		validationErrors["line1"] = "Address is required"
	}
	if strings.TrimSpace(req.City) == "" {
		validationErrors["city"] = "City is required"
	}
	if strings.TrimSpace(req.State) == "" {
		validationErrors["state"] = "State is required"
	}
	if strings.TrimSpace(req.PostalCode) == "" {
		validationErrors["postal_code"] = "Postal code is required"
	}
	if strings.TrimSpace(req.Country) == "" {
		req.Country = "US"
	}

	if len(validationErrors) > 0 {
		JSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": validationErrors})
		return
	}

	var resp checkoutAddressResponse

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		// Find or create guest customer by email
		customer, txErr := d.CustomerService.GetCustomerByEmail(ctx, tx, req.Email)
		if txErr != nil {
			if !errors.Is(txErr, app.ErrCustomerNotFound) {
				return fmt.Errorf("lookup customer: %w", txErr)
			}
			customer, txErr = d.CustomerService.CreateRetail(ctx, tx, req.Email, req.FirstName, req.LastName)
			if txErr != nil {
				return fmt.Errorf("create guest customer: %w", txErr)
			}
		}
		resp.CustomerID = customer.ID.String()

		var line2 *string
		if req.Line2 != "" {
			line2 = &req.Line2
		}
		actor := app.Actor{
			Type: domain.AuditActorTypeCustomer,
			ID:   &customer.ID,
			Name: customer.Email,
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
		}, actor)
		if txErr != nil {
			return fmt.Errorf("create address: %w", txErr)
		}
		resp.AddressID = addr.ID.String()

		return nil
	})
	if err != nil {
		logger.Error("checkout address", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save address"})
		return
	}

	JSON(w, http.StatusOK, resp)
}

// handleCheckoutApplyCoupon validates and applies a coupon code to the cart.
func (d *Deps) handleCheckoutApplyCoupon(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	var req checkoutApplyCouponRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	code := strings.TrimSpace(req.Code)
	if code == "" {
		JSON(w, http.StatusOK, checkoutApplyCouponResponse{
			Valid:        false,
			ErrorMessage: "Please enter a coupon code.",
		})
		return
	}

	cartID, err := uuid.Parse(req.CartID)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid cart_id"})
		return
	}

	var resp checkoutApplyCouponResponse

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		discount, txErr := d.CheckoutService.ApplyCoupon(ctx, tx, code)
		if txErr != nil {
			switch {
			case errors.Is(txErr, app.ErrCouponNotFound):
				d.Metrics.CouponRejected.WithLabelValues("not_found").Inc()
				resp = checkoutApplyCouponResponse{ErrorMessage: "That code doesn't look right."}
			case errors.Is(txErr, app.ErrCouponAlreadyUsed):
				d.Metrics.CouponRejected.WithLabelValues("already_used").Inc()
				resp = checkoutApplyCouponResponse{ErrorMessage: "That code has already been used."}
			case errors.Is(txErr, app.ErrDiscountNotActive):
				d.Metrics.CouponRejected.WithLabelValues("not_active").Inc()
				resp = checkoutApplyCouponResponse{ErrorMessage: "That code is no longer active."}
			case errors.Is(txErr, app.ErrDiscountExpired):
				d.Metrics.CouponRejected.WithLabelValues("expired").Inc()
				resp = checkoutApplyCouponResponse{ErrorMessage: "That code has expired."}
			default:
				return txErr
			}
			return nil
		}

		// Look up the coupon code to get its ID for cart storage.
		coupon, txErr := d.CheckoutService.GetCouponCodeByCode(ctx, tx, code)
		if txErr != nil {
			return fmt.Errorf("get coupon code: %w", txErr)
		}

		// Apply to cart.
		_, txErr = d.OrderService.UpdateCartDiscount(ctx, tx, cartID, &discount.ID, &coupon.ID)
		if txErr != nil {
			return fmt.Errorf("update cart discount: %w", txErr)
		}

		d.Metrics.CouponApplied.Inc()
		resp = checkoutApplyCouponResponse{
			Valid:         true,
			DiscountName:  discount.Name,
			DiscountType:  string(discount.Type),
			DiscountValue: discount.Value,
		}
		return nil
	})
	if err != nil {
		logger.Error("apply coupon", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to apply coupon"})
		return
	}

	JSON(w, http.StatusOK, resp)
}

// handleCheckoutRemoveCoupon removes the applied coupon from the cart.
func (d *Deps) handleCheckoutRemoveCoupon(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	var req checkoutRemoveCouponRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	cartID, err := uuid.Parse(req.CartID)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid cart_id"})
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.OrderService.UpdateCartDiscount(ctx, tx, cartID, nil, nil)
		return txErr
	})
	if err != nil {
		logger.Error("remove coupon", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove coupon"})
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// handleCheckoutPaymentIntent creates a Stripe PaymentIntent for the cart total.
func (d *Deps) handleCheckoutPaymentIntent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	d.Metrics.CheckoutStarted.WithLabelValues("retail").Inc()

	var req checkoutPaymentIntentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	cartID, err := uuid.Parse(req.CartID)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid cart_id"})
		return
	}
	addressID, err := uuid.Parse(req.AddressID)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid address_id"})
		return
	}
	customerID, err := uuid.Parse(req.CustomerID)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid customer_id"})
		return
	}

	var subtotal int
	var discountTotal int
	var discountName string
	var couponCode string
	var taxTotal int
	var taxLabel string
	var shippingAddr *domain.Address

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		items, txErr := d.CartService.ListItems(ctx, tx, cartID)
		if txErr != nil {
			return fmt.Errorf("list cart items: %w", txErr)
		}
		if len(items) == 0 {
			return app.ErrCartEmpty
		}

		// Build subtotal and tax line items.
		taxLineItems := make([]domain.TaxLineItem, len(items))
		for i, ci := range items {
			lineTotal := ci.UnitPrice * ci.Quantity
			subtotal += lineTotal

			variant, vErr := d.CatalogService.GetVariant(ctx, tx, ci.VariantID)
			if vErr != nil {
				return fmt.Errorf("get variant for tax: %w", vErr)
			}
			product, pErr := d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
			if pErr != nil {
				return fmt.Errorf("get product for tax: %w", pErr)
			}

			taxLineItems[i] = domain.TaxLineItem{
				LineIndex: i,
				Subtotal:  lineTotal,
				TaxExempt: product.TaxExempt,
			}
		}

		// Check for applied coupon on cart.
		cart, txErr := d.CartService.GetCart(ctx, tx, cartID)
		if txErr != nil {
			return fmt.Errorf("get cart: %w", txErr)
		}
		if cart.AppliedCouponCodeID != nil {
			cc, ccErr := d.CheckoutService.GetCouponCodeByID(ctx, tx, *cart.AppliedCouponCodeID)
			if ccErr == nil && cc.RedeemedAt == nil {
				discount, dErr := d.DiscountService.GetDiscount(ctx, tx, cc.DiscountID)
				if dErr == nil && discount.Active {
					discountTotal = calculateDiscountAmount(discount, subtotal)
					discountName = discount.Name
					couponCode = cc.Code
				}
			}
		}

		// Tax is on discounted subtotal.
		discountedSubtotal := subtotal - discountTotal
		for i := range taxLineItems {
			if discountTotal > 0 && discountedSubtotal > 0 {
				// Proportionally reduce each line's taxable amount.
				ratio := float64(discountedSubtotal) / float64(subtotal)
				taxLineItems[i].Subtotal = int(float64(taxLineItems[i].Subtotal) * ratio)
			}
		}

		customer, txErr := d.CustomerService.GetCustomer(ctx, tx, customerID)
		if txErr != nil {
			return fmt.Errorf("get customer for tax: %w", txErr)
		}

		isWholesale := customer.AccountType == domain.AccountTypeWholesale
		taxResult, txErr := d.CheckoutService.CalculateTax(ctx, tx, taxLineItems, customer.TaxExempt, isWholesale)
		if txErr != nil {
			return fmt.Errorf("calculate tax: %w", txErr)
		}
		taxTotal = taxResult.TaxTotal
		taxLabel = taxResult.Label

		shippingAddr, txErr = d.CustomerService.GetAddressByID(ctx, tx, addressID)
		if txErr != nil {
			return fmt.Errorf("get address: %w", txErr)
		}

		return nil
	})
	if err != nil {
		logger.Error("checkout payment-intent", "error", err)
		if errors.Is(err, app.ErrCartEmpty) {
			JSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "cart is empty"})
			return
		}
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to prepare payment"})
		return
	}

	totalCents := subtotal - discountTotal + taxTotal

	// Create Stripe PaymentIntent (external call — outside transaction)
	pi, err := d.PaymentProvider.CreatePaymentIntent(ctx, payments.CreatePaymentIntentRequest{
		AmountCents: int64(totalCents),
		Currency:    "usd",
		Metadata: map[string]string{
			"cart_id":     cartID.String(),
			"customer_id": customerID.String(),
			"address_id":  addressID.String(),
		},
		ShippingAddress: &payments.ShippingAddress{
			Name:       shippingAddr.FirstName + " " + shippingAddr.LastName,
			Line1:      shippingAddr.Line1,
			Line2:      ptrToString(shippingAddr.Line2),
			City:       shippingAddr.City,
			State:      shippingAddr.State,
			PostalCode: shippingAddr.PostalCode,
			Country:    shippingAddr.CountryCode,
		},
	})
	if err != nil {
		logger.Error("create payment intent", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create payment"})
		return
	}

	JSON(w, http.StatusOK, checkoutPaymentIntentResponse{
		ClientSecret:  pi.ClientSecret,
		Amount:        totalCents,
		Currency:      "usd",
		Subtotal:      subtotal,
		DiscountTotal: discountTotal,
		DiscountName:  discountName,
		CouponCode:    couponCode,
		TaxTotal:      taxTotal,
		TaxLabel:      taxLabel,
	})
}

// handleCheckoutConfirm finalizes the order after Stripe payment succeeds.
func (d *Deps) handleCheckoutConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)
	checkoutStart := time.Now()

	var req checkoutConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	cartID, err := uuid.Parse(req.CartID)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid cart_id"})
		return
	}
	customerID, err := uuid.Parse(req.CustomerID)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid customer_id"})
		return
	}
	addressID, err := uuid.Parse(req.AddressID)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid address_id"})
		return
	}

	// Verify the payment intent succeeded (external call — outside transaction)
	pi, err := d.PaymentProvider.GetPaymentIntent(ctx, req.PaymentIntentID)
	if err != nil {
		logger.Error("get payment intent", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify payment"})
		return
	}
	if pi.Status != payments.PaymentIntentStatusSucceeded {
		d.Metrics.CheckoutFailed.WithLabelValues("retail", "payment_failed").Inc()
		d.Metrics.CheckoutDuration.WithLabelValues("retail").Observe(time.Since(checkoutStart).Seconds())
		JSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "payment has not succeeded"})
		return
	}

	var resp checkoutConfirmResponse

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		items, txErr := d.CartService.ListItems(ctx, tx, cartID)
		if txErr != nil {
			return fmt.Errorf("list cart items: %w", txErr)
		}
		if len(items) == 0 {
			return app.ErrCartEmpty
		}

		subtotal := 0
		orderItems := make([]app.CartItem, len(items))
		taxLineItems := make([]domain.TaxLineItem, len(items))
		for i, ci := range items {
			orderItems[i] = app.CartItem{
				VariantID: ci.VariantID,
				Quantity:  ci.Quantity,
				UnitPrice: ci.UnitPrice,
			}
			lineTotal := ci.UnitPrice * ci.Quantity
			subtotal += lineTotal

			variant, vErr := d.CatalogService.GetVariant(ctx, tx, ci.VariantID)
			if vErr != nil {
				return fmt.Errorf("get variant for tax: %w", vErr)
			}
			product, pErr := d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
			if pErr != nil {
				return fmt.Errorf("get product for tax: %w", pErr)
			}

			taxLineItems[i] = domain.TaxLineItem{
				LineIndex: i,
				Subtotal:  lineTotal,
				TaxExempt: product.TaxExempt,
			}
		}

		// Check for applied coupon on cart.
		var couponCode *string
		discountTotal := 0
		cart, txErr := d.CartService.GetCart(ctx, tx, cartID)
		if txErr != nil {
			return fmt.Errorf("get cart: %w", txErr)
		}
		if cart.AppliedCouponCodeID != nil {
			cc, ccErr := d.CheckoutService.GetCouponCodeByID(ctx, tx, *cart.AppliedCouponCodeID)
			if ccErr == nil && cc.RedeemedAt == nil {
				discount, dErr := d.DiscountService.GetDiscount(ctx, tx, cc.DiscountID)
				if dErr == nil && discount.Active {
					discountTotal = calculateDiscountAmount(discount, subtotal)
					couponCode = &cc.Code
				}
			}
		}

		// Tax is on discounted subtotal.
		discountedSubtotal := subtotal - discountTotal
		for i := range taxLineItems {
			if discountTotal > 0 && discountedSubtotal > 0 {
				ratio := float64(discountedSubtotal) / float64(subtotal)
				taxLineItems[i].Subtotal = int(float64(taxLineItems[i].Subtotal) * ratio)
			}
		}

		customer, txErr := d.CustomerService.GetCustomer(ctx, tx, customerID)
		if txErr != nil {
			return fmt.Errorf("get customer for tax: %w", txErr)
		}
		isWholesale := customer.AccountType == domain.AccountTypeWholesale

		taxResult, txErr := d.CheckoutService.CalculateTax(ctx, tx, taxLineItems, customer.TaxExempt, isWholesale)
		if txErr != nil {
			return fmt.Errorf("calculate tax: %w", txErr)
		}

		order, txErr := d.CheckoutService.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID:        customerID,
			Items:             orderItems,
			ShippingAddressID: addressID,
			BillingAddressID:  addressID,
			CurrencyCode:      "USD",
			CouponCode:        couponCode,
			ShippingCents:     0,
			TaxCents:          taxResult.TaxTotal,
		}, app.Actor{
			Type: "customer",
			ID:   &customerID,
			Name: "guest checkout",
		})
		if txErr != nil {
			return fmt.Errorf("place order: %w", txErr)
		}

		_, txErr = d.OrderService.UpdateStripePaymentIntentID(ctx, tx, order.ID, req.PaymentIntentID)
		if txErr != nil {
			return fmt.Errorf("update stripe payment intent: %w", txErr)
		}

		_, txErr = d.OrderService.UpdatePaymentStatus(ctx, tx, order.ID, domain.PaymentStatusCaptured, app.Actor{
			Type: "system",
			Name: "checkout",
		})
		if txErr != nil {
			return fmt.Errorf("update payment status: %w", txErr)
		}

		txErr = d.CartService.DeleteCart(ctx, tx, cartID)
		if txErr != nil {
			return fmt.Errorf("delete cart: %w", txErr)
		}

		// Enqueue order confirmation email in the same transaction.
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.OrderConfirmEmailArgs{
			OrderID:    order.ID,
			CustomerID: customerID,
		}, nil)
		if txErr != nil {
			return fmt.Errorf("enqueue order confirm email: %w", txErr)
		}

		resp.OrderID = order.ID.String()
		resp.OrderNumber = order.Number

		return nil
	})
	if err != nil {
		logger.Error("checkout confirm", "error", err)
		reason := classifyCheckoutError(err)
		d.Metrics.CheckoutFailed.WithLabelValues("retail", reason).Inc()
		d.Metrics.CheckoutDuration.WithLabelValues("retail").Observe(time.Since(checkoutStart).Seconds())
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create order"})
		return
	}

	d.Metrics.CheckoutCompleted.WithLabelValues("retail").Inc()
	d.Metrics.CheckoutDuration.WithLabelValues("retail").Observe(time.Since(checkoutStart).Seconds())

	// Clear cart cookie
	http.SetCookie(w, &http.Cookie{
		Name:   cartCookieName,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	JSON(w, http.StatusOK, resp)
}

// calculateDiscountAmount computes the discount amount in cents.
func calculateDiscountAmount(d *domain.Discount, subtotal int) int {
	switch d.Type {
	case domain.DiscountTypePercentage:
		return subtotal * d.Value / 100
	case domain.DiscountTypeFixedAmount:
		if d.Value > subtotal {
			return subtotal
		}
		return d.Value
	default:
		return 0
	}
}

func ptrToString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// classifyCheckoutError maps checkout errors to failure_reason metric labels.
func classifyCheckoutError(err error) string {
	switch {
	case errors.Is(err, app.ErrPaymentFailed):
		return "payment_failed"
	case errors.Is(err, app.ErrCouponAlreadyRedeemed):
		return "coupon_redeemed"
	case errors.Is(err, app.ErrInsufficientStock):
		return "inventory_unavailable"
	case errors.Is(err, app.ErrCartEmpty):
		return "validation_error"
	default:
		return "internal_error"
	}
}
