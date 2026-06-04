package web

import (
	"context"
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
	"github.com/dukerupert/hiri/internal/platform/auth"
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
	Phone      string `json:"phone,omitempty"`
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
	// Local fulfillment options derived from the saved zip + merchant config.
	// EligibleLocalMethods is empty for non-local zips; otherwise it contains
	// any combination of "local_delivery" and "pickup". The Svelte client uses
	// it to render a method radio + the supporting copy fields.
	EligibleLocalMethods    []string `json:"eligible_local_methods"`
	LocalPickupInstructions string   `json:"local_pickup_instructions,omitempty"`
	LocalDeliveryDays       string   `json:"local_delivery_days,omitempty"`
	// PreferredLocalFulfillment is the customer's saved choice ("pickup" or
	// "local_delivery"), or "" if they haven't set one. Used as the default
	// selection so repeat customers don't re-pick at every checkout.
	PreferredLocalFulfillment string `json:"preferred_local_fulfillment,omitempty"`
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
	CartID         string `json:"cart_id"`
	AddressID      string `json:"address_id"`
	CustomerID     string `json:"customer_id"`
	ShippingMethod string `json:"shipping_method,omitempty"` // "pickup" | "local_delivery" | "" (auto)
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
	ShippingTotal  int    `json:"shipping_total"`
	ShippingLabel  string `json:"shipping_label,omitempty"`
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
	Redirect    string `json:"redirect"`
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

	// Build the begin_checkout payload from cart contents. Best-effort — if the
	// cart can't be loaded we still render the page; just no GA event fires.
	if cartID != nil {
		if analytics, err := d.loadCheckoutAnalytics(ctx, *cartID); err == nil && analytics != nil {
			props.Analytics = analytics
		} else if err != nil {
			logging.FromContext(ctx).Warn("checkout page: load analytics", "error", err)
		}
	}

	if IsHTMX(r) {
		storefront.CheckoutContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.CheckoutPage(props).Render(ctx, w) //nolint:errcheck
}

// loadCheckoutAnalytics resolves cart items into a GA4 begin_checkout payload.
// Returns nil (no error) when the cart is empty.
func (d *Deps) loadCheckoutAnalytics(ctx context.Context, cartID uuid.UUID) (*storefront.CheckoutAnalytics, error) {
	var analytics storefront.CheckoutAnalytics
	analytics.Currency = "USD"

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		items, txErr := d.CartService.ListItems(ctx, tx, cartID)
		if txErr != nil {
			return fmt.Errorf("list cart items: %w", txErr)
		}
		if len(items) == 0 {
			return nil
		}
		productCache := map[uuid.UUID]*domain.Product{}
		out := make([]storefront.CheckoutAnalyticsItem, 0, len(items))
		var subtotal int
		for _, ci := range items {
			variant, vErr := d.CatalogService.GetVariant(ctx, tx, ci.VariantID)
			if vErr != nil {
				return fmt.Errorf("get variant: %w", vErr)
			}
			product, ok := productCache[variant.ProductID]
			if !ok {
				product, vErr = d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
				if vErr != nil {
					return fmt.Errorf("get product: %w", vErr)
				}
				productCache[variant.ProductID] = product
			}
			price, pErr := d.PricingService.GetBasePrice(ctx, tx, ci.VariantID, "USD")
			var unitCents int
			if pErr == nil && price != nil {
				unitCents = price.Amount
			}
			subtotal += unitCents * ci.Quantity
			out = append(out, storefront.CheckoutAnalyticsItem{
				ItemID:      product.ID.String(),
				ItemName:    product.Title,
				ItemVariant: variant.SKU,
				PriceCents:  unitCents,
				Quantity:    ci.Quantity,
			})
		}
		analytics.ValueCents = subtotal
		analytics.Items = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(analytics.Items) == 0 {
		return nil, nil
	}
	return &analytics, nil
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

	trimmedPhone := strings.TrimSpace(req.Phone)

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		// Find or create guest customer by email
		customer, txErr := d.CustomerService.GetCustomerByEmail(ctx, tx, req.Email)
		if txErr != nil {
			if !errors.Is(txErr, app.ErrCustomerNotFound) {
				return fmt.Errorf("lookup customer: %w", txErr)
			}
			var phone *string
			if trimmedPhone != "" {
				phone = &trimmedPhone
			}
			customer, txErr = d.CustomerService.CreateRetail(ctx, tx, req.Email, req.FirstName, req.LastName, phone)
			if txErr != nil {
				return fmt.Errorf("create guest customer: %w", txErr)
			}
		} else if trimmedPhone != "" {
			// Returning customer: only mutate the saved phone when the request
			// is from an authenticated session whose customer matches the
			// looked-up email. Guests typing someone else's email must not be
			// able to overwrite that customer's notification number.
			if sessionCustomer, ok := auth.CustomerFromContext(ctx); ok && sessionCustomer.ID == customer.ID {
				if customer.Phone == nil || *customer.Phone != trimmedPhone {
					actor := app.Actor{
						Type: domain.AuditActorTypeCustomer,
						ID:   &customer.ID,
						Name: customer.Email,
					}
					if _, phoneErr := d.CustomerService.UpdatePhone(ctx, tx, customer.ID, &trimmedPhone, actor); phoneErr != nil {
						return fmt.Errorf("update phone: %w", phoneErr)
					}
				}
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

		// Local fulfillment eligibility for this address. Populated for any
		// local zip (even with one option enabled) so the Svelte client can
		// render the corresponding copy without re-fetching the config.
		cfg, txErr := d.CheckoutService.GetShippingConfig(ctx, tx)
		if txErr != nil {
			return fmt.Errorf("get shipping config: %w", txErr)
		}
		methods := cfg.EligibleLocalMethods(addr.PostalCode)
		resp.EligibleLocalMethods = make([]string, 0, len(methods))
		for _, m := range methods {
			resp.EligibleLocalMethods = append(resp.EligibleLocalMethods, string(m))
		}
		if len(methods) > 0 {
			resp.LocalPickupInstructions = cfg.LocalPickupInstructions
			resp.LocalDeliveryDays = cfg.LocalDeliveryDays
			if customer.PreferredLocalFulfillment != nil {
				resp.PreferredLocalFulfillment = string(*customer.PreferredLocalFulfillment)
			}
		}

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

// handleCheckoutPaymentIntent creates a Stripe PaymentIntent and a pending
// order linked to it. The order is created BEFORE the customer authorizes
// payment so async/redirect-based methods (Klarna, Affirm) have an order to
// transition once the webhook fires — even if the customer never returns to
// the redirect-back URL. The order starts in status=pending,
// payment_status=awaiting and is moved forward by ConfirmCheckoutPayment
// from either the /checkout/confirm endpoint or the
// payment_intent.succeeded webhook (whichever wins the race).
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

	// Phase 1: load cart + compute totals (tx, no external calls).
	var (
		subtotal      int
		discountTotal int
		discountName  string
		couponCode    string
		taxTotal      int
		taxLabel      string
		shippingTotal int
		shippingLabel string
		shippingAddr  *domain.Address
		orderItems    []app.CartItem
		chosenMethod  *domain.ShippingMethod
	)

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		items, txErr := d.CartService.ListItems(ctx, tx, cartID)
		if txErr != nil {
			return fmt.Errorf("list cart items: %w", txErr)
		}
		if len(items) == 0 {
			return app.ErrCartEmpty
		}

		// Build subtotal, tax line items, and the order-items snapshot.
		taxLineItems := make([]domain.TaxLineItem, len(items))
		orderItems = make([]app.CartItem, len(items))
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
			orderItems[i] = app.CartItem{
				VariantID: ci.VariantID,
				Quantity:  ci.Quantity,
				UnitPrice: ci.UnitPrice,
			}
		}

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
				ratio := float64(discountedSubtotal) / float64(subtotal)
				taxLineItems[i].Subtotal = int(float64(taxLineItems[i].Subtotal) * ratio)
			}
		}

		customer, txErr := d.CustomerService.GetCustomer(ctx, tx, customerID)
		if txErr != nil {
			return fmt.Errorf("get customer for tax: %w", txErr)
		}

		// scoping: addressID comes from client-submitted JSON and is not scoped to customerID.
		// Impact is limited (tax calc + order creation use the address; content is not echoed back
		// to the client), but worth tightening post-launch. Tracked as follow-up.
		shippingAddr, txErr = d.CustomerService.GetAddressByIDAsStaff(ctx, tx, addressID)
		if txErr != nil {
			return fmt.Errorf("get address: %w", txErr)
		}

		// Guard the fields tax + shipping depend on. A blank state means tax
		// can't be computed and a blank ZIP breaks shipping/local eligibility —
		// surface that as a fixable error pointing back to the address step
		// instead of failing the calculation with an opaque 500 downstream.
		if !addressShippable(shippingAddr) {
			return app.ErrAddressIncomplete
		}

		isWholesale := customer.AccountType == domain.AccountTypeWholesale
		taxResult, txErr := d.CheckoutService.CalculateTax(ctx, tx, taxLineItems, customer.TaxExempt, isWholesale, shippingAddr.State)
		if txErr != nil {
			return fmt.Errorf("calculate tax: %w", txErr)
		}
		taxTotal = taxResult.TaxTotal
		taxLabel = taxResult.Label

		shipCents, shipCfg, txErr := d.CheckoutService.CalculateShipping(ctx, tx, discountedSubtotal, shippingAddr.PostalCode)
		if txErr != nil {
			return fmt.Errorf("calculate shipping: %w", txErr)
		}
		shippingTotal = shipCents

		// Resolve the chosen local fulfillment method. The Svelte client sends
		// a value when the zip is local; validate against the merchant config
		// instead of trusting the input. If the customer has a saved preference
		// and didn't send a method, fall back to it before defaulting to the
		// first eligible option.
		eligible := shipCfg.EligibleLocalMethods(shippingAddr.PostalCode)
		chosenMethod = resolveLocalMethod(eligible, req.ShippingMethod, customer.PreferredLocalFulfillment)
		shippingLabel = shippingDisplayLabel(shipCfg, shipCents, shippingAddr.PostalCode, chosenMethod)

		return nil
	})
	if err != nil {
		logger.Error("checkout payment-intent: phase 1", "error", err)
		switch {
		case errors.Is(err, app.ErrCartEmpty):
			d.Metrics.CheckoutFailed.WithLabelValues("retail", "validation_error").Inc()
			JSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error": "Your cart is empty. Add something before checking out.",
				"code":  "cart_empty",
			})
		case errors.Is(err, app.ErrAddressIncomplete):
			d.Metrics.CheckoutFailed.WithLabelValues("retail", "address_incomplete").Inc()
			JSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error": "Your shipping address is missing its street, city, state, or ZIP. Go back and finish it so we can price shipping and tax.",
				"code":  "address_incomplete",
			})
		case errors.Is(err, app.ErrAddressNotFound):
			d.Metrics.CheckoutFailed.WithLabelValues("retail", "address_incomplete").Inc()
			JSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error": "We couldn't find that shipping address. Go back and re-enter it.",
				"code":  "address_incomplete",
			})
		default:
			d.Metrics.CheckoutFailed.WithLabelValues("retail", "internal_error").Inc()
			JSON(w, http.StatusInternalServerError, map[string]string{
				"error": "Something went wrong preparing your payment. Please try again — if it keeps happening, your cart is saved.",
			})
		}
		return
	}

	totalCents := subtotal - discountTotal + shippingTotal + taxTotal

	// Phase 2: create Stripe PaymentIntent (external — no tx).
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

	// Phase 3: place the order in pending+awaiting and link it to the PI.
	// The cart_id is stashed on order.Metadata so ConfirmCheckoutPayment can
	// find and delete the cart later. If a coupon was applied at Phase 1,
	// we redeem it inside PlaceOrder — releasing it on order cancellation
	// (handled by CancelOrder) is the path back if payment never completes.
	var couponCodePtr *string
	if couponCode != "" {
		couponCodePtr = &couponCode
	}
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		order, txErr := d.CheckoutService.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID:        customerID,
			Items:             orderItems,
			ShippingAddressID: addressID,
			BillingAddressID:  addressID,
			CurrencyCode:      "USD",
			CouponCode:        couponCodePtr,
			ShippingCents:     shippingTotal,
			TaxCents:          taxTotal,
			ShippingMethod:    chosenMethod,
			Metadata: map[string]any{
				"cart_id":           cartID.String(),
				"payment_intent_id": pi.ID,
			},
		}, app.Actor{
			Type: domain.AuditActorTypeCustomer,
			ID:   &customerID,
			Name: "guest checkout",
		})
		if txErr != nil {
			return fmt.Errorf("place order: %w", txErr)
		}
		if _, txErr := d.OrderService.UpdateStripePaymentIntentID(ctx, tx, order.ID, pi.ID); txErr != nil {
			return fmt.Errorf("link payment intent: %w", txErr)
		}
		return nil
	})
	if err != nil {
		logger.Error("checkout payment-intent: phase 3", "error", err, "payment_intent_id", pi.ID)
		// Best-effort cancel the orphaned PI so the customer can retry. If
		// the cancel call fails too, Stripe auto-cancels after 48h.
		if cancelErr := d.PaymentProvider.CancelPaymentIntent(ctx, pi.ID); cancelErr != nil {
			logger.Warn("orphaned payment intent cancel failed", "payment_intent_id", pi.ID, "error", cancelErr)
		}
		reason := "internal_error"
		if errors.Is(err, app.ErrCouponAlreadyRedeemed) || errors.Is(err, app.ErrCouponAlreadyUsed) {
			reason = "coupon_redeemed"
			JSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "coupon was just used by another customer"})
		} else {
			JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to place order"})
		}
		d.Metrics.CheckoutFailed.WithLabelValues("retail", reason).Inc()
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
		ShippingTotal: shippingTotal,
		ShippingLabel: shippingLabel,
	})
}

// shippingDisplayLabel returns the customer-facing descriptor for a computed
// shipping line. The chosen method (set when the customer picked pickup vs
// delivery for a local zip) drives the wording so the receipt and confirmation
// page match the choice. Returns "" when the default "Shipping" label is
// sufficient.
func shippingDisplayLabel(cfg *domain.ShippingConfig, shippingCents int, shipToZip string, method *domain.ShippingMethod) string {
	if cfg == nil {
		return ""
	}
	if shippingCents > 0 {
		return ""
	}
	if method != nil {
		switch *method {
		case domain.ShippingMethodPickup:
			return "Free pickup at the shop"
		case domain.ShippingMethodLocalDelivery:
			return "Free local delivery"
		}
	}
	if cfg.IsLocal(shipToZip) {
		// Local zip but no method recorded — fall back to the more specific
		// label so the receipt still reads correctly. Only happens when
		// neither pickup nor delivery was offered (both toggles off).
		return "Free local delivery"
	}
	return "Free shipping"
}

// resolveLocalMethod picks the shipping method to stamp on a retail order.
// Priority: explicit client choice (must be in the eligible set), then the
// customer's saved preference (likewise validated), then the first eligible
// option. Returns nil for non-local addresses (eligible empty), which leaves
// the order's shipping_method NULL — i.e. standard "shipped" downstream.
func resolveLocalMethod(eligible []domain.ShippingMethod, requested string, preference *domain.ShippingMethod) *domain.ShippingMethod {
	if len(eligible) == 0 {
		return nil
	}
	contains := func(s domain.ShippingMethod) bool {
		for _, m := range eligible {
			if m == s {
				return true
			}
		}
		return false
	}
	if requested != "" {
		m := domain.ShippingMethod(requested)
		if contains(m) {
			return &m
		}
	}
	if preference != nil && contains(*preference) {
		m := *preference
		return &m
	}
	first := eligible[0]
	return &first
}

// handleCheckoutConfirm transitions the pre-created order to confirmed when
// payment has succeeded. The order itself was created in the PI handler; this
// endpoint only drives the state transition. It accepts both `succeeded` and
// `processing` PI statuses — for async methods (Klarna), `processing` is
// expected and the webhook will finalize the order shortly after. Idempotent:
// safe to call multiple times for the same PI (returns the existing order).
func (d *Deps) handleCheckoutConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)
	checkoutStart := time.Now()

	var req checkoutConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Verify the payment intent's status (external call — outside transaction)
	pi, err := d.PaymentProvider.GetPaymentIntent(ctx, req.PaymentIntentID)
	if err != nil {
		logger.Error("get payment intent", "error", err)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify payment"})
		return
	}

	var resp checkoutConfirmResponse

	switch pi.Status {
	case payments.PaymentIntentStatusSucceeded:
		// Sync (card) path: drive the order forward now.
		err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			order, _, txErr := d.CheckoutService.ConfirmCheckoutPayment(ctx, tx, req.PaymentIntentID, app.Actor{
				Type: domain.AuditActorTypeSystem,
				Name: "checkout_confirm",
			})
			if txErr != nil {
				return txErr
			}
			resp.OrderID = order.ID.String()
			resp.OrderNumber = order.Number
			resp.Redirect = "/order/confirmed?number=" + order.Number
			return nil
		})
	case payments.PaymentIntentStatusProcessing:
		// Async (Klarna/BNPL) path: order stays in pending+awaiting; the
		// payment_intent.succeeded webhook will finalize it. Return the
		// confirmation redirect immediately so the customer's browser can
		// land on the confirmation page; that page is tolerant of orders
		// still in flight.
		err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			// scoping: guest checkout has no customerID to scope by; the Stripe
			// PaymentIntent ID is a server-issued secret the buyer's browser
			// just received, so it acts as the ownership token here.
			order, txErr := d.OrderService.GetOrderByStripePaymentIntentIDAsStaff(ctx, tx, req.PaymentIntentID)
			if txErr != nil {
				return fmt.Errorf("get order for processing pi: %w", txErr)
			}
			resp.OrderID = order.ID.String()
			resp.OrderNumber = order.Number
			resp.Redirect = "/order/confirmed?number=" + order.Number
			return nil
		})
	default:
		d.Metrics.CheckoutFailed.WithLabelValues("retail", "payment_failed").Inc()
		d.Metrics.CheckoutDuration.WithLabelValues("retail").Observe(time.Since(checkoutStart).Seconds())
		JSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "payment has not succeeded"})
		return
	}

	if err != nil {
		logger.Error("checkout confirm", "error", err, "payment_intent_id", req.PaymentIntentID)
		reason := classifyCheckoutError(err)
		d.Metrics.CheckoutFailed.WithLabelValues("retail", reason).Inc()
		d.Metrics.CheckoutDuration.WithLabelValues("retail").Observe(time.Since(checkoutStart).Seconds())
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to confirm order"})
		return
	}

	d.Metrics.CheckoutCompleted.WithLabelValues("retail").Inc()
	d.Metrics.CheckoutDuration.WithLabelValues("retail").Observe(time.Since(checkoutStart).Seconds())

	// Clear cart cookie regardless of sync/async — the cart row was deleted
	// inside ConfirmCheckoutPayment for sync, or will be by the webhook for
	// async. Either way the customer's session cart cookie should drop.
	http.SetCookie(w, &http.Cookie{
		Name:   cartCookieName,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	// Stash the just-placed order number so /order/confirmed can render the
	// GA4 purchase payload without exposing line items to anyone who knows or
	// guesses an order number.
	http.SetCookie(w, &http.Cookie{
		Name:     lastOrderCookieName,
		Value:    resp.OrderNumber,
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	JSON(w, http.StatusOK, resp)
}

// handleOrderConfirmed renders a server-side order confirmation page.
// This is the redirect target after the Svelte checkout completes, so the
// confirmation survives a page refresh.
//
// When the rr_last_order cookie matches the URL `?number=` param, the page
// loads the order's line items + totals and embeds them so the client can fire
// a GA4 `purchase` event. The cookie expires in 10 min and is cleared after
// one read so refreshes don't double-fire.
func (d *Deps) handleOrderConfirmed(w http.ResponseWriter, r *http.Request) {
	logger := logging.FromContext(r.Context())
	orderNumber := r.URL.Query().Get("number")
	if orderNumber == "" {
		http.Redirect(w, r, "/catalog", http.StatusSeeOther)
		return
	}

	props := storefront.OrderConfirmedProps{
		OrderNumber: orderNumber,
		CartCount:   0, // Cart was just cleared
	}

	cookieFound := false
	cookieMatches := false
	cookieValue := ""
	if c, err := r.Cookie(lastOrderCookieName); err == nil {
		cookieFound = true
		cookieValue = c.Value
		cookieMatches = c.Value == orderNumber
	}

	analyticsLoaded := false
	itemsCount := 0
	if cookieMatches {
		if analytics, loadErr := d.loadOrderAnalytics(r.Context(), orderNumber); loadErr == nil {
			props.Analytics = analytics
			analyticsLoaded = true
			itemsCount = len(analytics.Items)
			http.SetCookie(w, &http.Cookie{
				Name:   lastOrderCookieName,
				Value:  "",
				Path:   "/",
				MaxAge: -1,
			})
		} else {
			logger.Warn("GA_DEBUG order confirmed: load analytics", "error", loadErr, "order_number", orderNumber)
		}
	}

	logger.Info("GA_DEBUG order confirmed: render",
		"order_number", orderNumber,
		"cookie_found", cookieFound,
		"cookie_value", cookieValue,
		"cookie_matches", cookieMatches,
		"analytics_loaded", analyticsLoaded,
		"items_count", itemsCount,
		"is_htmx", IsHTMX(r),
		"user_agent", r.Header.Get("User-Agent"),
		"referer", r.Header.Get("Referer"),
	)

	if IsHTMX(r) {
		storefront.OrderConfirmedContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.OrderConfirmedPage(props).Render(r.Context(), w) //nolint:errcheck
}

// loadOrderAnalytics builds the GA4 purchase payload for a confirmed order.
// Resolves variants → products so each line item carries title + SKU + category.
func (d *Deps) loadOrderAnalytics(ctx context.Context, orderNumber string) (*storefront.OrderConfirmedAnalytics, error) {
	var analytics storefront.OrderConfirmedAnalytics

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		// scoping: guest-safe by design — the caller (handleOrderConfirmed) only
		// invokes this when the single-use rr_last_order cookie matches the order
		// number, so access is gated by the cookie rather than query scoping.
		order, txErr := d.OrderService.GetOrderByNumberAsStaff(ctx, tx, orderNumber)
		if txErr != nil {
			return fmt.Errorf("get order: %w", txErr)
		}
		lineItems, txErr := d.OrderService.ListLineItems(ctx, tx, order.ID)
		if txErr != nil {
			return fmt.Errorf("list line items: %w", txErr)
		}

		productCache := map[uuid.UUID]*domain.Product{}
		taxonCache := map[uuid.UUID]string{}
		items := make([]storefront.OrderConfirmedItem, 0, len(lineItems))
		for _, li := range lineItems {
			variant, vErr := d.CatalogService.GetVariant(ctx, tx, li.VariantID)
			if vErr != nil {
				return fmt.Errorf("get variant %s: %w", li.VariantID, vErr)
			}
			product, ok := productCache[variant.ProductID]
			if !ok {
				product, vErr = d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
				if vErr != nil {
					return fmt.Errorf("get product %s: %w", variant.ProductID, vErr)
				}
				productCache[variant.ProductID] = product
			}
			category, ok := taxonCache[product.TaxonID]
			if !ok {
				if taxon, tErr := d.CatalogService.GetTaxon(ctx, tx, product.TaxonID); tErr == nil {
					category = taxon.Name
				}
				taxonCache[product.TaxonID] = category
			}
			items = append(items, storefront.OrderConfirmedItem{
				ItemID:       product.ID.String(),
				ItemName:     product.Title,
				ItemVariant:  variant.SKU,
				ItemCategory: category,
				PriceCents:   li.UnitPrice,
				Quantity:     li.Quantity,
			})
		}

		analytics.TransactionID = order.Number
		analytics.Currency = order.CurrencyCode
		analytics.ValueCents = order.Total
		analytics.TaxCents = order.TaxTotal
		analytics.ShippingCents = order.ShippingTotal
		analytics.Items = items
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &analytics, nil
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

// addressShippable reports whether an address carries every field checkout
// needs to price the order: a street line, city, state, and ZIP. A returning
// customer can have an older address row missing one of these, and tax/shipping
// fail unhelpfully without them — we'd rather send the buyer back to fix it.
func addressShippable(a *domain.Address) bool {
	if a == nil {
		return false
	}
	return strings.TrimSpace(a.Line1) != "" &&
		strings.TrimSpace(a.City) != "" &&
		strings.TrimSpace(a.State) != "" &&
		strings.TrimSpace(a.PostalCode) != ""
}

// classifyCheckoutError maps checkout errors to failure_reason metric labels.
func classifyCheckoutError(err error) string {
	switch {
	case errors.Is(err, app.ErrPaymentFailed):
		return "payment_failed"
	case errors.Is(err, app.ErrPaymentAmountMismatch):
		return "payment_amount_mismatch"
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
