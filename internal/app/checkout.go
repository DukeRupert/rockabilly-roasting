package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/platform/tax"
	"github.com/dukerupert/hiri/internal/store"
)

// CheckoutService orchestrates the checkout flow: cart validation, payment, and order creation.
type CheckoutService struct {
	orders    *store.OrderStore
	customers *store.CustomerStore
	discounts *store.DiscountStore
	settings  *store.SettingsStore
	shipping  *store.ShippingStore
	payments  payments.Provider
	audit     *audit.AuditWriter
	metrics   *metrics.Registry

	// Optional — required by ConfirmCheckoutPayment.
	carts    *store.CartStore
	enqueuer JobEnqueuer

	// Optional — lets a postponement carry the run's planned route onto the new
	// day. Nil simply leaves routes alone, which is what every caller that has
	// no route store wants.
	routes *store.RouteStore

	// merchantTZ is the zone the local-delivery cutoff is judged in. Nil falls
	// back to UTC, which only misplaces the cutoff in dev — production wires
	// MERCHANT_TIMEZONE.
	merchantTZ *time.Location

	// pricing resolves what a line costs. Required: PlaceOrder prices its own
	// lines rather than believing the ones it was handed.
	pricing *PricingService
}

// WithMerchantTZ sets the zone used to resolve local-delivery dates against the
// order-by cutoff.
func (s *CheckoutService) WithMerchantTZ(loc *time.Location) *CheckoutService {
	s.merchantTZ = loc
	return s
}

// NewCheckoutService creates a new CheckoutService.
func NewCheckoutService(
	orders *store.OrderStore,
	customers *store.CustomerStore,
	discounts *store.DiscountStore,
	settings *store.SettingsStore,
	shipping *store.ShippingStore,
	payments payments.Provider,
	pricing *PricingService,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *CheckoutService {
	return &CheckoutService{
		orders:    orders,
		customers: customers,
		discounts: discounts,
		settings:  settings,
		shipping:  shipping,
		payments:  payments,
		pricing:   pricing,
		audit:     audit,
		metrics:   metrics,
	}
}

// WithDeliveryRoutes lets postponements reconcile the planned delivery route
// for a run that moves.
//
// Optional rather than a constructor argument because it is needed by exactly
// one pair of methods, and a nil store means "no routes to worry about" rather
// than a broken service — the postponement itself still works.
func (s *CheckoutService) WithDeliveryRoutes(routes *store.RouteStore) *CheckoutService {
	s.routes = routes
	return s
}

// WithCheckoutConfirmDeps wires the cart store and job enqueuer required by
// ConfirmCheckoutPayment. Must be called at startup before any checkout
// confirmation paths run.
func (s *CheckoutService) WithCheckoutConfirmDeps(carts *store.CartStore, enqueuer JobEnqueuer) *CheckoutService {
	s.carts = carts
	s.enqueuer = enqueuer
	return s
}

// GetShippingConfig returns the merchant's shipping configuration so callers
// can render rate, threshold, and local-zone messaging.
func (s *CheckoutService) GetShippingConfig(ctx context.Context, tx pgx.Tx) (*domain.ShippingConfig, error) {
	return s.shipping.GetConfig(ctx, tx)
}

// UpdateShippingConfig persists an edited shipping configuration and records
// the change in the audit log inside the caller's transaction.
func (s *CheckoutService) UpdateShippingConfig(ctx context.Context, tx pgx.Tx, cfg domain.ShippingConfig, actor Actor) error {
	before, err := s.shipping.GetConfig(ctx, tx)
	if err != nil {
		return fmt.Errorf("load current shipping config: %w", err)
	}
	if err := s.shipping.UpdateConfig(ctx, tx, cfg); err != nil {
		return err
	}
	return s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditShippingConfigUpdated,
		ResourceType: "shipping_config",
		ResourceID:   uuid.Nil,
		After: map[string]any{
			"flat_rate_cents":           cfg.FlatRateCents,
			"free_shipping_threshold":   cfg.FreeShippingThreshold,
			"local_zip_codes":           cfg.LocalZipCodes,
			"local_delivery_enabled":    cfg.LocalDeliveryEnabled,
			"local_pickup_enabled":      cfg.LocalPickupEnabled,
			"local_pickup_instructions": cfg.LocalPickupInstructions,
			"local_delivery_weekdays":   weekdayNames(cfg.LocalDeliveryWeekdays),
			"local_delivery_cutoff":     cfg.CutoffLabel(),
			"origin_name":               cfg.OriginName,
			"origin_street1":            cfg.OriginStreet1,
			"origin_street2":            cfg.OriginStreet2,
			"origin_city":               cfg.OriginCity,
			"origin_state":              cfg.OriginState,
			"origin_zip":                cfg.OriginZip,
			"origin_country":            cfg.OriginCountry,
			"origin_email":              cfg.OriginEmail,
			"origin_phone":              cfg.OriginPhone,
			"tare_weight_oz":            cfg.TareWeightOz,
		},
		Metadata: map[string]any{
			"before": map[string]any{
				"flat_rate_cents":           before.FlatRateCents,
				"free_shipping_threshold":   before.FreeShippingThreshold,
				"local_zip_codes":           before.LocalZipCodes,
				"local_delivery_enabled":    before.LocalDeliveryEnabled,
				"local_pickup_enabled":      before.LocalPickupEnabled,
				"local_pickup_instructions": before.LocalPickupInstructions,
				"local_delivery_weekdays":   weekdayNames(before.LocalDeliveryWeekdays),
				"local_delivery_cutoff":     before.CutoffLabel(),
				"origin_name":               before.OriginName,
				"origin_street1":            before.OriginStreet1,
				"origin_street2":            before.OriginStreet2,
				"origin_city":               before.OriginCity,
				"origin_state":              before.OriginState,
				"origin_zip":                before.OriginZip,
				"origin_country":            before.OriginCountry,
				"origin_email":              before.OriginEmail,
				"origin_phone":              before.OriginPhone,
				"tare_weight_oz":            before.TareWeightOz,
			},
		},
	})
}

// weekdayNames renders delivery weekdays as names for the audit log. Raw
// integers would make a staff member reading the log translate Go's weekday
// numbering in their head to see that the route changed.
func weekdayNames(days []time.Weekday) []string {
	out := make([]string, 0, len(days))
	for _, d := range days {
		out = append(out, d.String())
	}
	return out
}

// CalculateShipping returns the shipping cost in cents for a given subtotal
// and destination zip, using the merchant's shipping configuration.
func (s *CheckoutService) CalculateShipping(ctx context.Context, tx pgx.Tx, subtotalCents int, shipToZip string) (int, *domain.ShippingConfig, error) {
	cfg, err := s.GetShippingConfig(ctx, tx)
	if err != nil {
		return 0, nil, fmt.Errorf("get shipping config: %w", err)
	}
	return cfg.Calculate(subtotalCents, shipToZip), cfg, nil
}

// shipsTogetherWindow bounds how far back OpenShipmentTo looks. An order that
// has sat unpacked longer than this is held up on something, not waiting on
// the next packing pass, so a new order should not assume it rides along.
const shipsTogetherWindow = 72 * time.Hour

// OpenShipmentTo returns the customer's most recent paid, unpacked, mailed
// order to addressID — the box a new order to that address will be packed
// into — or nil when there is none.
//
// The subscribe form takes one product per signup, so a customer who wants
// three coffees on subscription places three orders in a row. Each priced its
// own flat-rate shipping, and one box went out carrying three shipping
// charges. A second order to the same address, placed while the first is still
// on the shelf, is packed with it, and its shipping is already paid. Callers
// use a non-nil result to waive the new order's shipping.
//
// Local delivery and pickup orders never count: they are free to begin with,
// and a mailed order does not ride in a delivery van.
func (s *CheckoutService) OpenShipmentTo(ctx context.Context, tx pgx.Tx, customerID, addressID uuid.UUID, now time.Time) (*domain.Order, error) {
	shipped := domain.ShippingMethodShipped
	since := now.Add(-shipsTogetherWindow)
	orders, err := s.orders.ListOrders(ctx, tx, store.OrderFilter{
		CustomerID:          &customerID,
		ShippingAddressID:   &addressID,
		Statuses:            []domain.OrderStatus{domain.OrderStatusConfirmed},
		PaymentStatuses:     []domain.PaymentStatus{domain.PaymentStatusCaptured},
		FulfillmentStatuses: []domain.FulfillmentStatus{domain.FulfillmentStatusUnfulfilled},
		ShippingMethod:      &shipped,
		PlacedFrom:          &since,
		Limit:               1,
	})
	if err != nil {
		return nil, fmt.Errorf("list open shipments: %w", err)
	}
	if len(orders) == 0 {
		return nil, nil
	}
	return &orders[0], nil
}

// taxCalculatorForConfig returns the TaxCalculator for the store's configuration.
//
// Wholesale used to be short-circuited to NoneCalculator here regardless of
// config — a blanket "B2B is never taxed" that was true of a catalog which is
// entirely bagged coffee, and stops being true the moment the shop invoices a
// cafe for a grinder or a repair. Taxability is a property of what is being
// sold, not of which channel sold it, so both channels now run the same
// per-line rule: products.tax_exempt decides, and the flat rate applies only
// inside the nexus state. With every product currently exempt, this changes no
// existing order's total — it starts mattering when staff flip a SKU taxable.
//
// Customer-level exemption (a reseller permit on file) is handled separately,
// by TaxOrder.CustomerExempt, which is how a resale account stays untaxed even
// on a taxable SKU.
func taxCalculatorForConfig(cfg *domain.TaxConfig) tax.TaxCalculator {
	switch cfg.Mode {
	case domain.TaxModeFlatRate:
		// Single-nexus WA merchant. If a second nexus state is added,
		// promote Jurisdiction to a store_settings column.
		return &tax.FlatRateCalculator{
			Rate:         cfg.Rate,
			Label:        cfg.Label,
			Jurisdiction: "WA",
		}
	case domain.TaxModeStripeTax:
		// Stripe Tax not yet implemented — fall back to none.
		return &tax.NoneCalculator{}
	default:
		return &tax.NoneCalculator{}
	}
}

// CalculateTax computes tax for the given line items using the store's tax configuration.
// shippingState is the 2-letter state code of the ship-to address; pass "" if unknown
// (flat-rate with a Jurisdiction will return zero in that case).
func (s *CheckoutService) CalculateTax(ctx context.Context, tx pgx.Tx, items []domain.TaxLineItem, customerExempt bool, shippingState string) (*domain.TaxResult, error) {
	cfg, err := s.settings.GetTaxConfig(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("get tax config: %w", err)
	}

	calculator := taxCalculatorForConfig(cfg)
	result, err := calculator.Calculate(ctx, tax.TaxOrder{
		CustomerExempt: customerExempt,
		ShippingState:  shippingState,
		LineItems:      items,
	})
	if err != nil {
		return nil, fmt.Errorf("calculate tax: %w", err)
	}

	return &result, nil
}

// CartItem represents an item in the checkout cart with pre-resolved pricing.
type CartItem struct {
	VariantID uuid.UUID
	Quantity  int
	UnitPrice int
}

// refuseUntrustableLines rejects a line whose quantity or price could not belong
// to a real order.
//
// Neither of today's callers lets a client name a line. The retail caller builds
// Items from cart rows the server owns, and CartService guards quantity when a
// line is added; the subscribe caller touches no cart at all, and builds a single
// line from a bounds-checked quantity and a price it looks up. So no request
// shape reaches the bad cases.
//
// So this is defence in depth rather than a hole that was open. The nearest
// thing to a live path is plan.DiscountPct, which the subscribe handler takes
// off the total as unit - (unit * DiscountPct / 100): above 100% that is a
// negative unit, and nothing in the schema forbids it — discount_pct has no
// CHECK. What forbids it is the layer above, in two places out of three:
// admin_plans.go bounds it 0-100 on create and on update, and
// SubscriptionService.UpdatePlanDiscount bounds it again in the service. Only
// CreatePlan has no service-layer bound, so the handler is the whole guard
// there. Reaching this from a plan would take direct SQL.
//
// It is here rather than at the callers because the next one to build Items some
// other way will not know any of the above. A guard where the numbers are
// consumed outlives the assumptions of whoever supplies them.
//
// The two are not symmetrical. A zero quantity charges nothing for goods the
// order would still ship. A negative unit price is worse than a free line: it is
// a credit, so a second line at -n cancels the first and a large enough one pays
// for the rest of the cart, which is a discount the merchant never authorised and
// no coupon record explains.
//
// Refused per line rather than on the total, because a total that comes out
// positive says nothing about how it got there: 2x5000 and 1x-9000 sum to an
// unremarkable 1000.
func refuseUntrustableLines(items []CartItem) error {
	for _, item := range items {
		if item.Quantity <= 0 {
			return ErrInvalidQuantity
		}
		if item.UnitPrice < 0 {
			return ErrInvalidPrice
		}
	}
	return nil
}

// refuseForeignAddresses rejects an order that names an address belonging to
// somebody else.
//
// This is the ownership half of the same problem: the address ids arrive from
// the caller, and until now they were copied into the order unexamined, so
// anyone who could reach the checkout endpoint could post another customer's
// address id and have it attached to — and shipped to — their own order, and
// read back off the order afterwards.
//
// Enforced the way this codebase enforces customer ownership elsewhere, by
// scoping the read rather than comparing ids afterwards: CustomerStore.GetAddress
// takes customerID as a parameter, so an address that is not this customer's
// simply does not come back. Not-found rather than forbidden is what the scoped
// query naturally yields, and is also what should be reported — from the caller's
// side an address they do not own is one that does not exist, and saying
// otherwise would confirm the id to whoever guessed it.
//
// This guards PlaceOrder only, which is the one order-creating path that takes
// address ids from its caller. The others derive theirs — CreateManualOrder and
// both renewal sites each say so where they are — and nothing here makes the
// guarantee repo-wide.
//
// The sentinel reaches the client as a 404 now: both PlaceOrder callers route
// their terminal error through Error(), so respond.go decides the status. They
// used to hand-roll it and answer everything but a spent coupon with a 500,
// which reported a refused address as a fault.
func (s *CheckoutService) refuseForeignAddresses(ctx context.Context, tx pgx.Tx, customerID, shippingID, billingID uuid.UUID) error {
	if err := s.requireOwnedAddress(ctx, tx, customerID, shippingID); err != nil {
		return err
	}
	// One address for both is the common case, and the second read would be the
	// same row. Shipping is checked first, so a foreign billing address is still
	// caught when the two differ.
	if billingID == shippingID {
		return nil
	}
	return s.requireOwnedAddress(ctx, tx, customerID, billingID)
}

// requireOwnedAddress takes the customer before the address, matching its only
// caller. Both are uuid.UUID, so a transposition would compile and silently
// check nothing — one order, stated once.
func (s *CheckoutService) requireOwnedAddress(ctx context.Context, tx pgx.Tx, customerID, addressID uuid.UUID) error {
	if _, err := s.customers.GetAddress(ctx, tx, addressID, customerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAddressNotFound
		}
		return fmt.Errorf("verify address ownership: %w", err)
	}
	return nil
}

// PlaceOrderParams holds all input needed to place an order.
type PlaceOrderParams struct {
	CustomerID        uuid.UUID
	Items             []CartItem
	ShippingAddressID uuid.UUID
	BillingAddressID  uuid.UUID
	CurrencyCode      string
	CouponCode        *string
	SubscriptionID    *uuid.UUID
	// PlanDiscountPct is the subscription plan's percentage off for a signup
	// order, and zero for retail. Named here because the service prices the
	// lines itself and cannot read a plan off a subscription that the webhook
	// has not created yet.
	PlanDiscountPct int
	// BasePrice is PriceLinesParams.BasePrice. Set for a subscription signup,
	// whose lines are priced from the base price as every renewal is, and for
	// retail checkout, whose cart was.
	BasePrice     bool
	ShippingCents int
	TaxCents      int
	// ShippingMethod records the chosen fulfillment channel. For retail
	// checkout this is set to pickup or local_delivery when the ship-to zip
	// is local and the customer picked one; otherwise nil and downstream
	// code treats the order as standard "shipped".
	ShippingMethod *domain.ShippingMethod
	Notes          *string
	Metadata       map[string]any
}

// PlaceOrder creates a new order from the given parameters within the provided transaction.
//
// A coupon passed here is validated and priced into the order, and the order
// records which coupon it was placed with, but the code is NOT redeemed —
// ConfirmCheckoutPayment redeems it when the payment is captured. See the
// comment there for why.
func (s *CheckoutService) PlaceOrder(ctx context.Context, tx pgx.Tx, p PlaceOrderParams, actor Actor) (*domain.Order, error) {
	if len(p.Items) == 0 {
		return nil, ErrCartEmpty
	}

	if err := refuseUntrustableLines(p.Items); err != nil {
		return nil, err
	}

	// Verify customer exists.
	_, err := s.customers.GetByID(ctx, tx, p.CustomerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("get customer: %w", err)
	}

	// Both addresses must belong to the customer this order is for. Checked
	// after the customer is known to exist, so a bad customer id reports itself
	// rather than surfacing as a missing address.
	if err := s.refuseForeignAddresses(ctx, tx, p.CustomerID, p.ShippingAddressID, p.BillingAddressID); err != nil {
		return nil, err
	}

	// The subtotal is what the catalog says these lines cost, not what the
	// caller totalled. See refusePricesThatMoved: the caller quoted the payment
	// provider from PriceLines, and this refuses the order rather than write one
	// whose total disagrees with the amount being charged.
	subtotal, err := s.refusePricesThatMoved(ctx, tx, p)
	if err != nil {
		return nil, err
	}

	// Apply coupon/discount if provided.
	var appliedDiscount *domain.Discount
	var coupon *domain.CouponCode
	discountAmount := 0

	if p.CouponCode != nil {
		coupon, err = s.discounts.GetCouponCodeByCode(ctx, tx, *p.CouponCode)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrCouponNotFound
			}
			return nil, fmt.Errorf("get coupon code: %w", err)
		}

		if coupon.RedeemedAt != nil {
			return nil, ErrCouponAlreadyUsed
		}

		appliedDiscount, err = s.discounts.GetByID(ctx, tx, coupon.DiscountID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrDiscountNotFound
			}
			return nil, fmt.Errorf("get discount: %w", err)
		}

		if !appliedDiscount.Active {
			return nil, ErrDiscountNotActive
		}

		now := time.Now()
		if appliedDiscount.ExpiresAt != nil && now.After(*appliedDiscount.ExpiresAt) {
			return nil, ErrDiscountExpired
		}

		if appliedDiscount.MinimumOrderCents != nil && subtotal < *appliedDiscount.MinimumOrderCents {
			return nil, ErrMinimumOrderNotMet
		}

		discountAmount = calculateDiscount(appliedDiscount, subtotal)
	}

	total := subtotal - discountAmount + p.ShippingCents + p.TaxCents

	orderNumber := generateOrderNumber()
	customerID := p.CustomerID

	// Resolved here rather than trusting anything the client sent: the customer
	// may have sat on the checkout page across the cutoff, in which case the
	// date quoted on the payment step is stale and this is the real one.
	placedAt := time.Now()
	scheduledDelivery, deliveryRun := scheduleLocalDelivery(ctx, tx, s.shipping, p.ShippingMethod, placedAt, s.merchantTZ)

	// Record the coupon on the order so capture knows what to redeem. Copied
	// rather than written through, because the caller's map is its own.
	metadata := p.Metadata
	if coupon != nil {
		metadata = maps.Clone(p.Metadata)
		if metadata == nil {
			metadata = map[string]any{}
		}
		metadata[orderMetadataCouponCodeID] = coupon.ID.String()
	}

	order, err := s.orders.CreateOrder(ctx, tx, store.CreateOrderParams{
		Number:                orderNumber,
		CustomerID:            &customerID,
		Status:                domain.OrderStatusPending,
		PaymentStatus:         domain.PaymentStatusAwaiting,
		FulfillmentStatus:     domain.FulfillmentStatusUnfulfilled,
		CurrencyCode:          p.CurrencyCode,
		Subtotal:              subtotal,
		DiscountTotal:         discountAmount,
		ShippingTotal:         p.ShippingCents,
		TaxTotal:              p.TaxCents,
		Total:                 total,
		ShippingAddressID:     p.ShippingAddressID,
		BillingAddressID:      p.BillingAddressID,
		SubscriptionID:        p.SubscriptionID,
		ShippingMethod:        p.ShippingMethod,
		ScheduledDeliveryDate: scheduledDelivery,
		DeliveryRunDate:       deliveryRun,
		Notes:                 p.Notes,
		Metadata:              metadata,
		PlacedAt:              placedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}

	// Create line items.
	for _, item := range p.Items {
		lineSubtotal := item.UnitPrice * item.Quantity
		_, err := s.orders.CreateLineItem(ctx, tx, store.CreateLineItemParams{
			OrderID:   order.ID,
			VariantID: item.VariantID,
			Quantity:  item.Quantity,
			UnitPrice: item.UnitPrice,
			Subtotal:  lineSubtotal,
			Total:     lineSubtotal,
		})
		if err != nil {
			return nil, fmt.Errorf("create line item: %w", err)
		}
	}

	// Create the discount adjustment. The coupon is not redeemed here: the
	// payment step re-runs /api/checkout/payment-intent on every shipping or
	// pricing change and each run places a fresh order, so redeeming at
	// placement spent the code on an order the customer was still paying for.
	// ConfirmCheckoutPayment redeems it at capture instead.
	if appliedDiscount != nil && coupon != nil {
		_, err := s.orders.CreateAdjustment(ctx, tx, store.CreateAdjustmentParams{
			OrderID:    order.ID,
			Label:      appliedDiscount.Name,
			Amount:     -discountAmount,
			SourceType: "discount",
			SourceID:   appliedDiscount.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("create discount adjustment: %w", err)
		}
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderCreated,
		ResourceType: "order",
		ResourceID:   order.ID,
		After:        order,
	}); err != nil {
		return nil, fmt.Errorf("audit order created: %w", err)
	}

	return order, nil
}

// GetCouponCodeByID returns a coupon code by ID.
func (s *CheckoutService) GetCouponCodeByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.CouponCode, error) {
	coupon, err := s.discounts.GetCouponCodeByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCouponNotFound
		}
		return nil, fmt.Errorf("get coupon code: %w", err)
	}
	return coupon, nil
}

// GetCouponCodeByCode returns a coupon code by its code string.
func (s *CheckoutService) GetCouponCodeByCode(ctx context.Context, tx pgx.Tx, code string) (*domain.CouponCode, error) {
	coupon, err := s.discounts.GetCouponCodeByCode(ctx, tx, code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCouponNotFound
		}
		return nil, fmt.Errorf("get coupon code: %w", err)
	}
	return coupon, nil
}

// ApplyCoupon validates a coupon code against the cart subtotal and returns
// its associated discount for preview. The code lookup is case-insensitive
// (handled in SQL), so customers can type codes however they like.
func (s *CheckoutService) ApplyCoupon(ctx context.Context, tx pgx.Tx, code string, subtotalCents int) (*domain.Discount, error) {
	coupon, err := s.discounts.GetCouponCodeByCode(ctx, tx, strings.TrimSpace(code))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCouponNotFound
		}
		return nil, fmt.Errorf("get coupon code: %w", err)
	}

	if coupon.RedeemedAt != nil {
		return nil, ErrCouponAlreadyUsed
	}

	discount, err := s.discounts.GetByID(ctx, tx, coupon.DiscountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDiscountNotFound
		}
		return nil, fmt.Errorf("get discount: %w", err)
	}

	if !discount.Active {
		return nil, ErrDiscountNotActive
	}

	now := time.Now()
	if discount.ExpiresAt != nil && now.After(*discount.ExpiresAt) {
		return nil, ErrDiscountExpired
	}

	if discount.MinimumOrderCents != nil && subtotalCents < *discount.MinimumOrderCents {
		return nil, ErrMinimumOrderNotMet
	}

	return discount, nil
}

// calculateDiscount computes the discount amount based on discount type.
func calculateDiscount(d *domain.Discount, subtotal int) int {
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

// ConfirmCheckoutPayment performs the idempotent state transition for a
// successfully-paid checkout: payment_status awaiting → captured, status
// pending → confirmed, deletes the cart referenced in order.Metadata, and
// enqueues the order-confirmation email. Looks up the order by Stripe PI ID
// using a row-level lock so concurrent callers (e.g. the redirect-back path
// and the payment_intent.succeeded webhook) serialize cleanly.
//
// Returns (order, transitioned, error). When transitioned is false the order
// was already past the awaiting-payment state and no side effects fired —
// callers should treat that as a successful no-op (idempotent re-entry).
//
// Caller must be inside an open transaction. The coupon the order was placed
// with — if any — is redeemed here rather than at placement; see
// redeemOrderCoupon.
func (s *CheckoutService) ConfirmCheckoutPayment(ctx context.Context, tx pgx.Tx, paymentIntentID string, actor Actor) (*domain.Order, bool, error) {
	order, err := s.orders.GetOrderByStripePaymentIntentIDForUpdate(ctx, tx, paymentIntentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrOrderNotFound
		}
		return nil, false, fmt.Errorf("get order for confirm: %w", err)
	}

	// Never resurrect a cancelled order. The PI can outlive the order row
	// (abandoned-order cleanup, manual admin cancel), and a late payment
	// success against a cancelled order must surface as "not handled" so
	// callers log it loudly instead of silently recording a capture.
	if order.Status == domain.OrderStatusCancelled {
		return order, false, nil
	}

	// Idempotent guard: if payment has already moved past awaiting we have
	// nothing to do. The first caller (whichever raced and won) handled
	// state + side effects; the second now sees the post-transition state.
	//
	// 'failed' is treated like awaiting: a failed attempt on a still-live
	// PaymentIntent (card declined, customer fixed the number and retried
	// the same PI) is a normal Stripe lifecycle, and the success that
	// follows must still drive the order forward.
	if order.PaymentStatus != domain.PaymentStatusAwaiting && order.PaymentStatus != domain.PaymentStatusFailed {
		return order, false, nil
	}

	order, err = s.orders.UpdateOrderPaymentStatus(ctx, tx, order.ID, domain.PaymentStatusCaptured)
	if err != nil {
		return nil, false, fmt.Errorf("update payment status captured: %w", err)
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderPaymentCaptured,
		ResourceType: "order",
		ResourceID:   order.ID,
		After:        order,
	}); err != nil {
		return nil, false, fmt.Errorf("audit payment captured: %w", err)
	}

	if order.Status == domain.OrderStatusPending {
		order, err = s.orders.UpdateOrderStatus(ctx, tx, order.ID, domain.OrderStatusConfirmed)
		if err != nil {
			return nil, false, fmt.Errorf("update order status confirmed: %w", err)
		}
		if err := s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    actor.Type,
			ActorID:      actor.ID,
			ActorName:    actor.Name,
			Action:       audit.AuditOrderStatusChanged,
			ResourceType: "order",
			ResourceID:   order.ID,
			After:        order,
			Metadata:     map[string]any{"new_status": string(domain.OrderStatusConfirmed)},
		}); err != nil {
			return nil, false, fmt.Errorf("audit order confirmed: %w", err)
		}
	}

	if err := s.redeemOrderCoupon(ctx, tx, order, actor); err != nil {
		return nil, false, err
	}

	// Delete the cart that was used to place this order. The cart_id is
	// stashed in order.Metadata at PlaceOrder time. Best-effort: a missing
	// or unparseable cart_id is not fatal here (the cart will be GC'd on
	// session expiry anyway).
	if s.carts != nil {
		if cartID, ok := cartIDFromMetadata(order.Metadata); ok {
			if err := s.carts.DeleteCart(ctx, tx, cartID); err != nil {
				return nil, false, fmt.Errorf("delete cart: %w", err)
			}
		}
	}

	if order.CustomerID != nil && s.enqueuer != nil {
		if err := s.enqueuer.EnqueueOrderConfirm(ctx, tx, order.ID, *order.CustomerID); err != nil {
			return nil, false, fmt.Errorf("enqueue order confirm email: %w", err)
		}
	}

	return order, true, nil
}

// redeemOrderCoupon claims the coupon code this order was placed with.
//
// Redemption happens at capture, not at placement. The payment step re-runs
// /api/checkout/payment-intent whenever the shipping method or the pricing
// changes, and every run places a fresh order; redeeming inside PlaceOrder
// therefore spent the code on an order the same customer was still trying to
// pay for, and the next run — finding it spent — priced the cart without the
// discount and said nothing. Claiming it here means a superseded order never
// held the code at all.
//
// Two locks, doing two different jobs. The row lock this transition already
// holds is on the *order*, and all it buys is that two captures of one order
// serialize. What arbitrates two different orders reaching for one code is
// RedeemCouponCode's own optimistic UPDATE … WHERE redeemed_at IS NULL, which
// is also why the claim can come back empty below.
//
// Nothing holds the code between placement and capture, so two customers can
// both place an order on the same single-use code. The first to pay gets it.
// The second is not blocked: by the time this runs their money has already
// moved at Stripe, and stranding a paid order to protect a coupon helps
// nobody. The lost redemption is recorded in the audit log so the merchant can
// see that a single-use code went out twice.
func (s *CheckoutService) redeemOrderCoupon(ctx context.Context, tx pgx.Tx, order *domain.Order, actor Actor) error {
	couponID, ok := couponCodeIDFromMetadata(order.Metadata)
	if !ok {
		return nil
	}

	_, err := s.discounts.RedeemCouponCode(ctx, tx, couponID, order.CustomerID, order.ID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("redeem coupon code: %w", err)
	}

	// The claim can fail for two very different reasons, and the query cannot
	// tell them apart: somebody else holds the code, or this order already
	// claimed it on an earlier pass. The second is reachable — a late
	// payment_intent.payment_failed can knock a captured order back to failed,
	// and the next success re-enters this transition — and filing it as a loss
	// would tell the merchant a single-use code went out twice when it went to
	// exactly one order.
	held, getErr := s.discounts.GetCouponCodeByID(ctx, tx, couponID)
	if getErr != nil {
		return fmt.Errorf("get coupon code after failed redeem: %w", getErr)
	}
	if held.RedeemedByOrderID != nil && *held.RedeemedByOrderID == order.ID {
		return nil
	}

	if auditErr := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCouponRedemptionLost,
		ResourceType: "order",
		ResourceID:   order.ID,
		After:        order,
		Metadata: map[string]any{
			"coupon_code_id": couponID.String(),
			"discount_total": order.DiscountTotal,
		},
	}); auditErr != nil {
		return fmt.Errorf("audit coupon redemption lost: %w", auditErr)
	}
	return nil
}

// orderMetadataCouponCodeID is the order.Metadata key carrying the coupon the
// order was placed with. A column would be the honest home for it; this is the
// same route cart_id and payment_intent_id already take.
const orderMetadataCouponCodeID = "coupon_code_id"

// cartIDFromMetadata pulls a UUID from order.Metadata["cart_id"]. Returns
// (uuid.Nil, false) if the key is missing or unparseable.
func cartIDFromMetadata(metadata map[string]any) (uuid.UUID, bool) {
	return uuidFromMetadata(metadata, "cart_id")
}

// couponCodeIDFromMetadata pulls the coupon this order was placed with out of
// order.Metadata. Returns (uuid.Nil, false) when the order carried no coupon.
func couponCodeIDFromMetadata(metadata map[string]any) (uuid.UUID, bool) {
	return uuidFromMetadata(metadata, orderMetadataCouponCodeID)
}

// uuidFromMetadata reads one UUID-valued key out of an order's metadata.
// Returns (uuid.Nil, false) if the key is missing or unparseable — metadata is
// JSONB and nothing enforces its shape, so every read has to survive garbage.
func uuidFromMetadata(metadata map[string]any, key string) (uuid.UUID, bool) {
	if metadata == nil {
		return uuid.Nil, false
	}
	raw, ok := metadata[key]
	if !ok {
		return uuid.Nil, false
	}
	s, ok := raw.(string)
	if !ok {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// ManualOrderItem is one line item for a manually-entered order.
type ManualOrderItem struct {
	VariantID uuid.UUID
	Quantity  int
	UnitPrice int
}

// CreateManualOrderParams holds all inputs for an admin-created order.
// Totals are admin-supplied (no cart, no tax/shipping calc, no coupon).
type CreateManualOrderParams struct {
	CustomerID            uuid.UUID
	Items                 []ManualOrderItem
	ShippingAddressID     uuid.UUID
	BillingAddressID      uuid.UUID
	CurrencyCode          string
	Subtotal              int
	DiscountTotal         int
	ShippingTotal         int
	TaxTotal              int
	Total                 int
	Status                domain.OrderStatus
	PaymentStatus         domain.PaymentStatus
	StripePaymentIntentID *string
	Notes                 *string
}

// CreateManualOrder creates an order from admin input, bypassing the cart and
// coupon flow. Used for reconciliation (e.g. payments processed by Stripe but
// missing from Hiri) and phone/email orders. Totals are accepted as-given;
// the admin is the source of truth.
//
// It carries no address-ownership check, unlike PlaceOrder, and does not need
// one: its caller resolves the address through FindOrCreateAddress against the
// customer it is creating the order for, so the id is server-derived rather than
// supplied. Said here because the two functions share a file and the same two
// address fields, and the absence would otherwise read as an oversight.
func (s *CheckoutService) CreateManualOrder(ctx context.Context, tx pgx.Tx, p CreateManualOrderParams, actor Actor) (*domain.Order, error) {
	if len(p.Items) == 0 {
		return nil, ErrCartEmpty
	}
	if _, err := s.customers.GetByID(ctx, tx, p.CustomerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("get customer: %w", err)
	}

	customerID := p.CustomerID
	order, err := s.orders.CreateOrder(ctx, tx, store.CreateOrderParams{
		Number:            generateOrderNumber(),
		CustomerID:        &customerID,
		Status:            p.Status,
		PaymentStatus:     p.PaymentStatus,
		FulfillmentStatus: domain.FulfillmentStatusUnfulfilled,
		CurrencyCode:      p.CurrencyCode,
		Subtotal:          p.Subtotal,
		DiscountTotal:     p.DiscountTotal,
		ShippingTotal:     p.ShippingTotal,
		TaxTotal:          p.TaxTotal,
		Total:             p.Total,
		ShippingAddressID: p.ShippingAddressID,
		BillingAddressID:  p.BillingAddressID,
		Notes:             p.Notes,
		Metadata:          map[string]any{"manual": true},
		PlacedAt:          time.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}

	if p.StripePaymentIntentID != nil && *p.StripePaymentIntentID != "" {
		if _, err := s.orders.UpdateOrderStripePaymentIntentID(ctx, tx, order.ID, *p.StripePaymentIntentID); err != nil {
			return nil, fmt.Errorf("set stripe payment intent id: %w", err)
		}
	}

	for _, item := range p.Items {
		lineSubtotal := item.UnitPrice * item.Quantity
		if _, err := s.orders.CreateLineItem(ctx, tx, store.CreateLineItemParams{
			OrderID:   order.ID,
			VariantID: item.VariantID,
			Quantity:  item.Quantity,
			UnitPrice: item.UnitPrice,
			Subtotal:  lineSubtotal,
			Total:     lineSubtotal,
		}); err != nil {
			return nil, fmt.Errorf("create line item: %w", err)
		}
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderCreated,
		ResourceType: "order",
		ResourceID:   order.ID,
		After:        order,
		Metadata:     map[string]any{"manual": true},
	}); err != nil {
		return nil, fmt.Errorf("audit manual order created: %w", err)
	}

	return order, nil
}

// generateOrderNumber creates a non-guessable order number using random bytes.
// Format: "ORD-XXXXXXXXXX" where X is uppercase alphanumeric (5 random bytes → 10 hex chars).
func generateOrderNumber() string {
	return newOrderNumber("ORD")
}

// newOrderNumber mints an order number under the given prefix. Every path that
// creates an order comes through here, because orders.number is UNIQUE and the
// collision is expensive in a specific way: the renewal paths charge the card in
// their own transaction before writing the order, so a number that will not
// insert means a customer charged with no order to show for it.
//
// Time alone is not enough of a key. `SUB-<unix millis>` is what the renewal
// paths used to mint, and River runs renewals concurrently against a due set the
// scheduler enqueues all at once, so two of them sharing a millisecond is a
// question of how many subscriptions the shop has rather than of luck.
func newOrderNumber(prefix string) string {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail in practice. If it ever does, nanoseconds
		// are a worse key than random bytes but a far better one than the
		// milliseconds this replaced.
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + strings.ToUpper(hex.EncodeToString(b))
}
