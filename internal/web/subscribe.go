package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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

// parseSubscribeLines reads a signup off the /subscribe page's query string:
// any number of `line=<plan>:<variant>:<qty>` params (the picker's shape),
// plus the legacy trio (`plan_id`, `variant_id`, `quantity`) that older links
// still carry. Both can appear together, so the legacy trio is folded into the
// same slice rather than left on the request's top-level fields, which is what
// normalizeSubscribeLines refuses combined with `lines`.
//
// The error is the message shown to whoever typed or followed the URL.
func parseSubscribeLines(q url.Values) ([]subscribeLine, error) {
	var raw []subscribeLineRequest

	planID := strings.TrimSpace(q.Get("plan_id"))
	variantID := strings.TrimSpace(q.Get("variant_id"))
	if planID != "" || variantID != "" {
		quantity := 1
		if qs := q.Get("quantity"); qs != "" {
			n, err := strconv.Atoi(qs)
			if err != nil {
				return nil, errors.New("invalid quantity")
			}
			quantity = n
		}
		raw = append(raw, subscribeLineRequest{
			PlanID: planID, VariantID: variantID, Quantity: quantity,
		})
	}

	for _, s := range q["line"] {
		parts := strings.SplitN(s, ":", 3)
		if len(parts) != 3 {
			return nil, errors.New("invalid line")
		}
		quantity, err := strconv.Atoi(parts[2])
		if err != nil {
			return nil, errors.New("invalid line")
		}
		raw = append(raw, subscribeLineRequest{PlanID: parts[0], VariantID: parts[1], Quantity: quantity})
	}

	if len(raw) == 0 {
		return nil, errors.New("choose at least one item to subscribe to")
	}
	return normalizeSubscribeLines(subscribePaymentIntentRequest{Lines: raw})
}

// subscribeLineErrorMessage maps an error raised while resolving a signup's
// lines — whether from the page or from the payment-intent endpoint — to the
// message shown to the customer. Both handlers hit the same errors resolving
// the same lines (an inactive plan, a made-to-order variant with no recipe, an
// unpriced variant), so the wording lives once rather than drifting between
// the two places it is shown. ok is false for an error neither expects.
func subscribeLineErrorMessage(err error) (msg string, ok bool) {
	switch {
	case errors.Is(err, app.ErrSubscriptionPlanNotFound), errors.Is(err, app.ErrSubscriptionPlanInactive):
		return "That subscription plan is no longer available. Head back to the product page and pick a current one.", true
	case errors.Is(err, app.ErrPriceNotFound):
		return "We couldn't price that item. Head back to the product page and try again.", true
	case errors.Is(err, app.ErrVariantArchived):
		return "This item is no longer available. Head back to the product page and pick a current one.", true
	default:
		return "", false
	}
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
// matches a line by plan and variant together: one variant on two plans is
// two lines, and matched by variant alone both prices land on one.
type subscribePaymentIntentResponse struct {
	checkoutPaymentIntentResponse
	Lines []subscribeLineResponse `json:"lines"`
}

type subscribeLineResponse struct {
	PlanID    string `json:"plan_id"`
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

// handleSubscribePage renders the subscription signup page for a box of one
// or more lines.
func (d *Deps) handleSubscribePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	lines, err := parseSubscribeLines(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	pageLines := make([]storefront.SubscribeLineProps, len(lines))

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		plans := make(map[uuid.UUID]*domain.SubscriptionPlan)
		for i, line := range lines {
			plan, seen := plans[line.PlanID]
			if !seen {
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

			// Product context so the card leads with the product, not the plan.
			variant, txErr := d.CatalogService.GetVariant(ctx, tx, line.VariantID)
			if txErr != nil {
				return fmt.Errorf("get variant: %w", txErr)
			}
			if variant.ArchivedAt != nil {
				return app.ErrVariantArchived
			}
			product, txErr := d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
			if txErr != nil {
				return fmt.Errorf("get product: %w", txErr)
			}

			lp := storefront.SubscribeLineProps{
				PlanID:        line.PlanID,
				VariantID:     line.VariantID,
				Quantity:      line.Quantity,
				ProductTitle:  product.Title,
				ProductSlug:   product.Slug,
				PlanName:      plan.Name,
				Interval:      plan.Interval,
				IntervalCount: plan.IntervalCount,
				NextChargeAt:  d.SubscriptionService.NextRenewalDate(time.Now(), plan),
			}
			if label, lErr := d.CatalogService.VariantLabel(ctx, tx, line.VariantID); lErr == nil {
				lp.VariantLabel = label
			}
			if media, mErr := d.CatalogService.ListProductMedia(ctx, tx, variant.ProductID); mErr == nil && len(media) > 0 {
				lp.ThumbnailURL = d.MediaConfig.ProductImageURL(media[0].R2Key, mediapkg.VariantThumbnail)
			}

			price, txErr := d.PricingService.GetBasePrice(ctx, tx, line.VariantID, "USD")
			if txErr != nil {
				return txErr
			}
			lp.BasePrice = price.Amount

			pageLines[i] = lp
		}

		// One computation for what a line costs: the same PriceLines the
		// payment intent prices these lines with, so the page never quotes a
		// number the card is not charged. From the base, as the payment intent
		// and every renewal price a subscription, so it needs no customer: a
		// signed-out visitor and a signed-in one are quoted the same.
		orderLines := make([]app.OrderLine, len(lines))
		for i, l := range lines {
			orderLines[i] = app.OrderLine{
				VariantID: l.VariantID, Quantity: l.Quantity,
				PlanDiscountPct: plans[l.PlanID].DiscountPct,
			}
		}
		priced, txErr := d.CheckoutService.PriceLines(ctx, tx, app.PriceLinesParams{
			CustomerID: uuid.Nil, CurrencyCode: "USD", Lines: orderLines, BasePrice: true,
		})
		if txErr != nil {
			return txErr
		}
		for i, item := range priced.Items {
			pageLines[i].UnitPrice = item.UnitPrice
		}
		return nil
	})
	if err != nil {
		if msg, ok := subscribeLineErrorMessage(err); ok {
			http.Error(w, msg, http.StatusUnprocessableEntity)
			return
		}
		Error(w, r, err)
		return
	}

	subtotal := 0
	distinctPlans := map[uuid.UUID]bool{}
	for _, l := range pageLines {
		subtotal += l.UnitPrice * l.Quantity
		distinctPlans[l.PlanID] = true
	}

	linesJSON, jsonErr := json.Marshal(pageLines)
	if jsonErr != nil {
		Error(w, r, fmt.Errorf("marshal subscribe lines: %w", jsonErr))
		return
	}

	props := storefront.SubscribePageProps{
		Lines:      pageLines,
		LinesJSON:  string(linesJSON),
		Subtotal:   subtotal,
		MultiPlan:  len(distinctPlans) > 1,
		MerchantTZ: d.MerchantTZ,
		StripeKey:  os.Getenv("STRIPE_PUBLISHABLE_KEY"),
		CartCount:  d.cartItemCountFromCookie(r),
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

// subscribeCatalogVariant is one item the in-page picker can add: a variant
// with a price.
type subscribeCatalogVariant struct {
	VariantID    string `json:"variant_id"`
	ProductTitle string `json:"product_title"`
	VariantLabel string `json:"variant_label"`
	BasePrice    int    `json:"base_price"`
}

type subscribeCatalogPlan struct {
	PlanID      string `json:"plan_id"`
	Name        string `json:"name"`
	DiscountPct int    `json:"discount_pct"`
}

type subscribeCatalogResponse struct {
	Variants []subscribeCatalogVariant `json:"variants"`
	Plans    []subscribeCatalogPlan    `json:"plans"`
}

// handleSubscribeCatalog answers the "Add another item" picker: every
// subscribable variant that can be added without a trip through the builder,
// and every active plan to put it on. Module-gated at the route like its
// neighbours — a shop without subscriptions has no such endpoint.
func (d *Deps) handleSubscribeCatalog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	resp := subscribeCatalogResponse{
		Variants: []subscribeCatalogVariant{},
		Plans:    []subscribeCatalogPlan{},
	}
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		subscribable := true
		activeStatus := domain.ProductStatusActive
		// Paged to the end. A fixed limit would drop the rest of a larger
		// catalog from the picker with nothing on the page saying so.
		const pageSize = 200
		var products []domain.Product
		for offset := 0; ; offset += pageSize {
			page, txErr := d.CatalogService.ListProducts(ctx, tx, store.ProductFilter{
				Status:       &activeStatus,
				Subscribable: &subscribable,
				Limit:        pageSize,
				Offset:       offset,
				Visibility:   d.retailVisibility(),
			})
			if txErr != nil {
				return txErr
			}
			products = append(products, page...)
			if len(page) < pageSize {
				break
			}
		}

		for _, product := range products {
			// The retail channel's variants, as every storefront listing
			// reads them: a wholesale-only size is not something a retail
			// subscriber can be sold.
			variants, vErr := d.CatalogService.ListActiveVariantsForChannel(ctx, tx, product.ID, domain.ChannelRetail)
			if vErr != nil {
				return vErr
			}
			for _, variant := range variants {
				price, pErr := d.PricingService.GetBasePrice(ctx, tx, variant.ID, "USD")
				if pErr != nil {
					if errors.Is(pErr, app.ErrPriceNotFound) {
						continue
					}
					return pErr
				}
				label, lErr := d.CatalogService.VariantLabel(ctx, tx, variant.ID)
				if lErr != nil {
					label = ""
				}
				resp.Variants = append(resp.Variants, subscribeCatalogVariant{
					VariantID:    variant.ID.String(),
					ProductTitle: product.Title,
					VariantLabel: label,
					BasePrice:    price.Amount,
				})
			}
		}

		plans, pErr := d.SubscriptionService.ListActivePlans(ctx, tx)
		if pErr != nil {
			return pErr
		}
		for _, plan := range plans {
			resp.Plans = append(resp.Plans, subscribeCatalogPlan{
				PlanID: plan.ID.String(), Name: plan.Name, DiscountPct: plan.DiscountPct,
			})
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}
	JSON(w, http.StatusOK, resp)
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
		default:
			if msg, ok := subscribeLineErrorMessage(err); ok {
				d.Metrics.CheckoutFailed.WithLabelValues("subscribe", "validation_error").Inc()
				JSON(w, http.StatusUnprocessableEntity, map[string]string{"error": msg})
				return
			}
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
			// priced.Items is in the order signup was priced in, so index i is
			// the same line on both.
			PlanID:    signup[i].plan.ID.String(),
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
