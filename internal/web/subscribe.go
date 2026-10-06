package web

import (
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
	// Lines is every item in the signup, each on its own plan. The three
	// top-level fields (plan, variant, quantity) are the one-line form the
	// endpoint took before a signup could carry several; a body uses one form or
	// the other, and normalizeSubscribeLines refuses both at once.
	Lines      []subscribeLineRequest `json:"lines,omitempty"`
	PlanID     string                 `json:"plan_id"`
	VariantID  string                 `json:"variant_id"`
	Quantity   int                    `json:"quantity"`
	Email      string                 `json:"email"`
	FirstName  string                 `json:"first_name"`
	LastName   string                 `json:"last_name"`
	Line1      string                 `json:"line1"`
	Line2      string                 `json:"line2,omitempty"`
	City       string                 `json:"city"`
	State      string                 `json:"state"`
	PostalCode string                 `json:"postal_code"`
	Country    string                 `json:"country"`
	// PreviousPaymentIntentID lets the client hand back the PI it is
	// abandoning (address edited after the payment element mounted) so we
	// can cancel it — which in turn cancels its pre-created order via the
	// payment_intent.canceled webhook.
	PreviousPaymentIntentID string `json:"previous_payment_intent_id,omitempty"`
}

// subscribeLineRequest is one item in a signup: a variant, the plan it renews
// on, and how many.
type subscribeLineRequest struct {
	PlanID    string `json:"plan_id"`
	VariantID string `json:"variant_id"`
	Quantity  int    `json:"quantity"`
}

// subscribeLine is a signup line once its ids are parsed.
type subscribeLine struct {
	PlanID    uuid.UUID
	VariantID uuid.UUID
	Quantity  int
}

// Caps on a signup, the same bounds CreateSubscription enforces per row.
const (
	maxSubscribeLines    = 10
	maxSubscribeQuantity = 10
)

// normalizeSubscribeLines turns a signup body into its lines: the legacy
// top-level fields wrapped as one line, duplicates merged, then the caps.
//
// Duplicates are the same variant and plan; they are one thing asked for
// twice, and become one line of the summed quantity. The same variant on
// another plan is a different thing and stays a separate line. The caps are checked after merging, so two lines of 6 are a
// line of 12 and are refused — never clamped to a quantity nobody asked for.
//
// The error is the message the client is shown.
func normalizeSubscribeLines(req subscribePaymentIntentRequest) ([]subscribeLine, error) {
	legacy := req.PlanID != "" || req.VariantID != ""
	raw := req.Lines
	switch {
	case len(raw) > 0 && legacy:
		return nil, errors.New("send either lines or plan_id and variant_id, not both")
	case len(raw) == 0 && !legacy:
		return nil, errors.New("choose at least one item to subscribe to")
	case len(raw) == 0:
		raw = []subscribeLineRequest{{
			PlanID: req.PlanID, VariantID: req.VariantID, Quantity: req.Quantity,
		}}
	}

	type key struct{ variant, plan uuid.UUID }
	var out []subscribeLine
	index := map[key]int{}
	for _, r := range raw {
		planID, err := uuid.Parse(r.PlanID)
		if err != nil {
			return nil, errors.New("invalid plan_id")
		}
		variantID, err := uuid.Parse(r.VariantID)
		if err != nil {
			return nil, errors.New("invalid variant_id")
		}
		if r.Quantity < 1 {
			// Before merging, so a negative line cannot quietly shrink its twin.
			return nil, fmt.Errorf("quantity must be between 1 and %d", maxSubscribeQuantity)
		}

		k := key{variant: variantID, plan: planID}
		if i, seen := index[k]; seen {
			out[i].Quantity += r.Quantity
			continue
		}
		index[k] = len(out)
		out = append(out, subscribeLine{PlanID: planID, VariantID: variantID, Quantity: r.Quantity})
	}

	if len(out) > maxSubscribeLines {
		return nil, fmt.Errorf("a subscription can hold at most %d items", maxSubscribeLines)
	}
	for _, l := range out {
		if l.Quantity > maxSubscribeQuantity {
			return nil, fmt.Errorf("quantity must be between 1 and %d", maxSubscribeQuantity)
		}
	}
	return out, nil
}

// signupLine is a parsed line with the plan it signs up for. Its order line
// (what it costs) and its cart item (what the order records) are both built
// from this one plan value, so the discount a line is priced at and the plan
// it is recorded against cannot come from two different plans.
type signupLine struct {
	subscribeLine
	plan *domain.SubscriptionPlan
}

func (l signupLine) orderLine() app.OrderLine {
	return app.OrderLine{
		VariantID:       l.VariantID,
		Quantity:        l.Quantity,
		PlanDiscountPct: l.plan.DiscountPct,
	}
}

func (l signupLine) cartItem(unitPrice int) app.CartItem {
	planID := l.plan.ID
	return app.CartItem{
		VariantID: l.VariantID,
		Quantity:  l.Quantity,
		UnitPrice: unitPrice,
		// The plan's discount is part of what this line costs, so the service
		// has to be told about it or it would price the line at full price and
		// refuse the order as one whose price moved.
		PlanDiscountPct:    l.plan.DiscountPct,
		SubscriptionPlanID: &planID,
	}
}

// subscribePaymentIntentResponse is the retail breakdown plus each line's
// price, so the form can show what every item costs after repricing. The form
// matches a line by variant.
type subscribePaymentIntentResponse struct {
	checkoutPaymentIntentResponse
	Lines []subscribeLineResponse `json:"lines"`
}

type subscribeLineResponse struct {
	VariantID string `json:"variant_id"`
	UnitPrice int    `json:"unit_price"`
	Quantity  int    `json:"quantity"`
	Subtotal  int    `json:"subtotal"`
}

type subscribeConfirmRequest struct {
	PaymentIntentID string `json:"payment_intent_id"`
}

type subscribeConfirmResponse struct {
	// SubscriptionIDs is every subscription the signup started.
	// SubscriptionID is set only when there is exactly one, and stays for the
	// success screen written before a signup could carry several.
	SubscriptionIDs []string `json:"subscription_ids,omitempty"`
	SubscriptionID  string   `json:"subscription_id,omitempty"`
	OrderID         string   `json:"order_id"`
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

	lines, err := normalizeSubscribeLines(req)
	if err != nil {
		JSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
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

	// Phase 1: load plans + prices, find-or-create customer, save address, and
	// price shipping + tax exactly like retail checkout (tx).
	var (
		signup        []signupLine
		priced        *app.PricedLines
		customer      *domain.Customer
		addr          *domain.Address
		shippingCents int
		taxCents      int
		shipMethod    *domain.ShippingMethod
		// shipsWith is the open order this one will be packed with, when there
		// is one. Its shipping charge covers the box.
		shipsWith *domain.Order
	)
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		plans := make(map[uuid.UUID]*domain.SubscriptionPlan)
		signup = make([]signupLine, len(lines))
		for i, line := range lines {
			plan, ok := plans[line.PlanID]
			if !ok {
				var txErr error
				plan, txErr = d.SubscriptionService.GetPlan(ctx, tx, line.PlanID)
				if txErr != nil {
					return txErr
				}
				if !plan.IsActive {
					return app.ErrSubscriptionPlanInactive
				}
				plans[line.PlanID] = plan
			}
			signup[i] = signupLine{subscribeLine: line, plan: plan}
		}

		var txErr error
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
		addr, txErr = d.CustomerService.FindOrCreateAddress(ctx, tx, customer.ID, params, actor)
		if txErr != nil {
			return fmt.Errorf("create address: %w", txErr)
		}

		// What the lines cost, from the one function that decides that — the
		// same one PlaceOrder prices this order with in phase 3, so the amount
		// authorised and the subtotal the order records cannot drift apart.
		//
		// Each line from the base price, less its own plan's discount: BasePrice,
		// because that is what RenewalService charges for every box after this
		// one, whatever price list the customer is on. Priced here rather than
		// with the plans above only because the customer is known by now.
		orderLines := make([]app.OrderLine, len(signup))
		for i, l := range signup {
			orderLines[i] = l.orderLine()
		}
		priced, txErr = d.CheckoutService.PriceLines(ctx, tx, app.PriceLinesParams{
			CustomerID:   customer.ID,
			CurrencyCode: "USD",
			Lines:        orderLines,
			BasePrice:    true,
		})
		if txErr != nil {
			return txErr
		}

		// Tax per line, each on its own discounted subtotal and its own
		// product's exemption, matching retail checkout. One line built from
		// the order's subtotal would tax an exempt item as if it were taxable,
		// or the reverse.
		taxLines := make([]domain.TaxLineItem, len(priced.Items))
		for i, item := range priced.Items {
			variant, txErr := d.CatalogService.GetVariant(ctx, tx, item.VariantID)
			if txErr != nil {
				return fmt.Errorf("get variant for tax: %w", txErr)
			}
			product, txErr := d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
			if txErr != nil {
				return fmt.Errorf("get product for tax: %w", txErr)
			}
			taxLines[i] = domain.TaxLineItem{
				LineIndex: i,
				Subtotal:  item.UnitPrice * item.Quantity,
				TaxExempt: product.TaxExempt,
			}
		}
		taxResult, txErr := d.CheckoutService.CalculateTax(ctx, tx, taxLines, customer.TaxExempt, addr.State)
		if txErr != nil {
			return fmt.Errorf("calculate tax: %w", txErr)
		}
		taxCents = taxResult.TaxTotal

		// Shipping once, on the whole order. That is the point of putting
		// several items in one signup.
		shipCents, shipCfg, txErr := d.CheckoutService.CalculateShipping(ctx, tx, priced.Subtotal, addr.PostalCode)
		if txErr != nil {
			return fmt.Errorf("calculate shipping: %w", txErr)
		}
		shippingCents = shipCents

		// No fulfillment UI on the subscribe form, so resolve the method from
		// the customer's saved preference (nil for new customers → first
		// eligible for local zips, nil otherwise = standard shipped).
		eligible := shipCfg.EligibleLocalMethods(addr.PostalCode)
		shipMethod = resolveLocalMethod(eligible, "", customer.PreferredLocalFulfillment)

		// A signup holds every item it was given, but a customer can still
		// sign up again tomorrow while today's order is on the shelf. The two
		// go out in one parcel, so only the first pays for it: a mailed order
		// that will be packed with an order already on the shelf ships free.
		// ("Open box" here is that parcel, not the account page's box of
		// subscriptions that renew together.)
		if shippingCents > 0 && (shipMethod == nil || *shipMethod == domain.ShippingMethodShipped) {
			shipsWith, txErr = d.CheckoutService.OpenShipmentTo(ctx, tx, customer.ID, addr.ID, time.Now())
			if txErr != nil {
				return fmt.Errorf("find open shipment: %w", txErr)
			}
			if shipsWith != nil {
				shippingCents = 0
			}
		}

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
			recordRequestError(r.Context(), err, http.StatusInternalServerError)
			JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to prepare payment"})
		}
		return
	}

	totalCents := priced.Subtotal + shippingCents + taxCents

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
			recordRequestError(r.Context(), stripeErr, http.StatusInternalServerError)
			JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create payment customer"})
			return
		}
		stripeCustomerID = stripeCust.ID
		newStripeCustomer = true
	}

	// What each line signs up for lives on the order's lines; the intent says
	// only that this is a signup, for whom, and how many items. Nothing reads
	// the plan or variant off the intent.
	piMetadata := map[string]string{
		"subscription_signup": "true",
		"customer_id":         customer.ID.String(),
		"line_count":          strconv.Itoa(len(signup)),
	}

	pi, err := d.PaymentProvider.CreatePaymentIntent(ctx, payments.CreatePaymentIntentRequest{
		AmountCents:      int64(totalCents),
		Currency:         "usd",
		CustomerID:       stripeCustomerID,
		SetupFutureUsage: "off_session",
		Metadata:         piMetadata,
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
		recordRequestError(r.Context(), err, http.StatusInternalServerError)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create payment"})
		return
	}

	// Phase 3: pre-create the first order in pending+awaiting and link it to
	// the PI (tx). From here on, payment success alone is enough for the
	// webhook to confirm the order and activate the subscriptions — no return
	// trip through the customer's browser required.
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		if newStripeCustomer {
			if txErr := d.CustomerService.LinkStripeCustomerID(ctx, tx, customer.ID, stripeCustomerID); txErr != nil {
				return fmt.Errorf("save stripe customer id: %w", txErr)
			}
		}

		actor := app.Actor{
			Type: domain.AuditActorTypeCustomer,
			ID:   &customer.ID,
			Name: "subscription checkout",
		}
		metadata := app.SubscriptionSignupOrderMetadata(pi.ID)
		if shipsWith != nil {
			metadata[app.OrderMetaShipsWithOrder] = shipsWith.Number
		}
		items := make([]app.CartItem, len(signup))
		for i, l := range signup {
			items[i] = l.cartItem(priced.Items[i].UnitPrice)
		}
		order, txErr := d.CheckoutService.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID:        customer.ID,
			Items:             items,
			ShippingAddressID: addr.ID,
			BillingAddressID:  addr.ID,
			CurrencyCode:      "USD",
			ShippingCents:     shippingCents,
			TaxCents:          taxCents,
			ShippingMethod:    shipMethod,
			// Without BasePrice the service would resolve this customer's list
			// price, which is not what phase 1 quoted.
			BasePrice: true,
			Metadata:  metadata,
		}, actor)
		if txErr != nil {
			// Unwrapped for the reason the retail endpoint says at its own
			// PlaceOrder call: this error becomes the shopper's message.
			return txErr
		}
		if shipsWith != nil {
			// Staff read the internal note from the fulfillment queue; the
			// metadata key is for reports. Both say the same thing.
			note := "Ships with " + shipsWith.Number + " — shipping was charged on that order."
			if _, txErr := d.OrderService.SetOrderInternalNote(ctx, tx, order.ID, note, actor); txErr != nil {
				return fmt.Errorf("note ships-with order: %w", txErr)
			}
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
		// Same one rule as the retail endpoint. This path has no coupon, so it
		// has no exception either.
		d.failCheckout(w, r, "subscribe", err)
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
	// being charged (items + shipping + tax), and each line's own price.
	shippingLabel := ""
	if shippingCents == 0 {
		shippingLabel = "Free"
	}
	if shipsWith != nil {
		shippingLabel = "Free — ships with " + shipsWith.Number
	}
	lineResp := make([]subscribeLineResponse, len(priced.Items))
	for i, item := range priced.Items {
		lineResp[i] = subscribeLineResponse{
			VariantID: item.VariantID.String(),
			UnitPrice: item.UnitPrice,
			Quantity:  item.Quantity,
			Subtotal:  item.UnitPrice * item.Quantity,
		}
	}
	JSON(w, http.StatusOK, subscribePaymentIntentResponse{
		checkoutPaymentIntentResponse: checkoutPaymentIntentResponse{
			ClientSecret:  pi.ClientSecret,
			Amount:        totalCents,
			Currency:      "usd",
			Subtotal:      priced.Subtotal,
			TaxTotal:      taxCents,
			ShippingTotal: shippingCents,
			ShippingLabel: shippingLabel,
		},
		Lines: lineResp,
	})
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
		recordRequestError(r.Context(), err, http.StatusInternalServerError)
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
				// subscriptions already exist. Read through subscription_orders
				// rather than orders.subscription_id, which is null on a signup
				// that started several.
				existing, txErr := d.SubscriptionService.ListSubscriptionsByOrder(ctx, tx, order.ID)
				if txErr != nil {
					return fmt.Errorf("list signup subscriptions: %w", txErr)
				}
				for _, sub := range existing {
					resp.SubscriptionIDs = append(resp.SubscriptionIDs, sub.ID.String())
				}
				if len(resp.SubscriptionIDs) == 1 {
					resp.SubscriptionID = resp.SubscriptionIDs[0]
				}
				return nil
			}

			subs, txErr := d.SubscriptionService.ActivateFromSignupOrder(ctx, tx, order, app.Actor{
				Type: domain.AuditActorTypeSystem,
				Name: "subscribe_confirm",
			})
			if txErr != nil {
				return fmt.Errorf("activate signup subscription: %w", txErr)
			}
			for _, sub := range subs {
				resp.SubscriptionIDs = append(resp.SubscriptionIDs, sub.ID.String())
			}
			if len(subs) == 1 {
				resp.SubscriptionID = resp.SubscriptionIDs[0]
			}

			if _, txErr := d.RiverClient.InsertTx(ctx, tx, subscriptionConfirmEmail(subs), nil); txErr != nil {
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
		recordRequestError(r.Context(), err, http.StatusInternalServerError)
		JSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to finalize subscription"})
		return
	}

	d.Metrics.CheckoutCompleted.WithLabelValues("subscribe").Inc()
	JSON(w, http.StatusOK, resp)
}

// subscriptionConfirmEmail is the one confirmation job for a signup, naming
// every subscription it started. Both activation paths (confirm and webhook)
// enqueue it, so the customer gets one email whichever of them wins.
func subscriptionConfirmEmail(subs []*domain.Subscription) jobs.SubscriptionConfirmEmailArgs {
	args := jobs.SubscriptionConfirmEmailArgs{SubscriptionIDs: make([]uuid.UUID, len(subs))}
	for i, sub := range subs {
		args.SubscriptionIDs[i] = sub.ID
		args.CustomerID = sub.CustomerID
	}
	return args
}
