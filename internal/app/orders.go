package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// OrderService contains business logic for orders and carts.
type OrderService struct {
	orders        *store.OrderStore
	audit         *audit.AuditWriter
	metrics       *metrics.Registry
	customers     *store.CustomerStore     // populated via WithEmail; required for Send* methods
	customerUsers *store.CustomerUserStore // populated via WithEmail; resolves extra recipients on wholesale accounts
	catalog       *store.CatalogStore      // populated via WithEmail; required for Send* methods
	subscriptions *store.SubscriptionStore // populated via WithEmail; used by SendRenewalReceiptEmail to show next-charge date
	shipments     *store.ShippingStore     // populated via WithShipments; required for SendOrderShippedEmail
	email         EmailEnv                 // populated via WithEmail; required for Send* methods
	discounts     *store.DiscountStore     // populated via WithDiscounts; required for CancelOrder coupon release
	enqueuer      JobEnqueuer              // populated via WithEnqueuer; required for ready-for-pickup / out-for-delivery email enqueue
	pricing       *store.PricingStore      // populated via WithPricing; required for ChangeLineItemVariant price-match guard
	// orderActions signs the switch-to-pickup link in order confirmations.
	// Optional: without it the email offers the switch by reply instead.
	orderActions *auth.OrderActionSigner
}

// WithOrderActionSigner wires the signer used to mint one-click order links in
// transactional email. Without it, confirmations still send — they just ask the
// customer to reply rather than printing a link nothing could verify.
func (s *OrderService) WithOrderActionSigner(signer *auth.OrderActionSigner) *OrderService {
	s.orderActions = signer
	return s
}

// NewOrderService creates a new OrderService.
func NewOrderService(orders *store.OrderStore, audit *audit.AuditWriter, metrics *metrics.Registry) *OrderService {
	return &OrderService{
		orders:  orders,
		audit:   audit,
		metrics: metrics,
	}
}

// WithEmail attaches email-send environment and the supporting stores required
// for Send* methods. Must be called before any Send* method is invoked; safe
// to call at wiring time.
func (s *OrderService) WithEmail(env EmailEnv, customers *store.CustomerStore, customerUsers *store.CustomerUserStore, catalog *store.CatalogStore, subscriptions *store.SubscriptionStore) *OrderService {
	s.email = env
	s.customers = customers
	s.customerUsers = customerUsers
	s.catalog = catalog
	s.subscriptions = subscriptions
	return s
}

// WithShipments attaches the shipments store used by SendOrderShippedEmail to
// load the shipment record being announced. Must be called at wiring time
// before any "order shipped" email is enqueued.
func (s *OrderService) WithShipments(shipments *store.ShippingStore) *OrderService {
	s.shipments = shipments
	return s
}

// WithDiscounts attaches the discount store used by CancelOrder to release a
// coupon redemption when the order is cancelled. Must be called at wiring
// time before any cancellation path runs.
func (s *OrderService) WithDiscounts(discounts *store.DiscountStore) *OrderService {
	s.discounts = discounts
	return s
}

// WithPricing wires the pricing store used by ChangeLineItemVariant to
// enforce the same-price guard when swapping a line item's variant.
func (s *OrderService) WithPricing(pricing *store.PricingStore) *OrderService {
	s.pricing = pricing
	return s
}

// WithEnqueuer wires the job enqueuer used by the local fulfillment
// transitions (MarkReadyForPickup / MarkOutForDelivery) to send the matching
// notification email. Must be set at wiring time before those methods run.
func (s *OrderService) WithEnqueuer(e JobEnqueuer) *OrderService {
	s.enqueuer = e
	return s
}

// --- State machine helpers ---

func canCancelOrder(status domain.OrderStatus) bool {
	return status == domain.OrderStatusPending || status == domain.OrderStatusConfirmed
}

func canRefundOrder(status domain.OrderStatus, paymentStatus domain.PaymentStatus) bool {
	return (status == domain.OrderStatusComplete || status == domain.OrderStatusConfirmed) &&
		paymentStatus == domain.PaymentStatusCaptured
}

// canEditOrderLineItems reports whether staff may make line-item-level edits
// (e.g., grind swap). The bag must not be packed yet and the order must not
// be in a terminal state.
func canEditOrderLineItems(o *domain.Order) bool {
	if o.Status == domain.OrderStatusCancelled || o.Status == domain.OrderStatusRefunded {
		return false
	}
	switch o.FulfillmentStatus {
	case domain.FulfillmentStatusFulfilled,
		domain.FulfillmentStatusShipped,
		domain.FulfillmentStatusDelivered:
		return false
	}
	return true
}

// --- Query methods ---

// GetOrderAsStaff returns an order by ID.
func (s *OrderService) GetOrderAsStaff(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Order, error) {
	o, err := s.orders.GetOrderByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order: %w", err)
	}
	return o, nil
}

// GetOrderByNumberAsStaff returns an order by its number.
func (s *OrderService) GetOrderByNumberAsStaff(ctx context.Context, tx pgx.Tx, number string) (*domain.Order, error) {
	o, err := s.orders.GetOrderByNumberAsStaff(ctx, tx, number)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order by number: %w", err)
	}
	return o, nil
}

// ListOrders returns orders matching the given filter.
func (s *OrderService) ListOrders(ctx context.Context, tx pgx.Tx, f store.OrderFilter) ([]domain.Order, error) {
	orders, err := s.orders.ListOrders(ctx, tx, f)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	return orders, nil
}

// CountOrders returns the number of orders matching the given filter.
func (s *OrderService) CountOrders(ctx context.Context, tx pgx.Tx, f store.OrderFilter) (int, error) {
	count, err := s.orders.CountOrders(ctx, tx, f)
	if err != nil {
		return 0, fmt.Errorf("count orders: %w", err)
	}
	return count, nil
}

// CountOrdersByView returns per-tab counts for the admin orders list. A non-nil
// channel scopes the counts to that sales channel (retail or wholesale).
func (s *OrderService) CountOrdersByView(ctx context.Context, tx pgx.Tx, search string, channel *domain.OrderChannel) (store.OrderViewCounts, error) {
	c, err := s.orders.CountOrdersByView(ctx, tx, search, channel)
	if err != nil {
		return store.OrderViewCounts{}, fmt.Errorf("count orders by view: %w", err)
	}
	return c, nil
}

// CountFulfillmentViews returns per-tab counts for the admin fulfillment queue.
// A nil channel spans both channels; pass a channel to scope the counts to the
// retail or wholesale fulfillment queue.
func (s *OrderService) CountFulfillmentViews(ctx context.Context, tx pgx.Tx, channel *domain.OrderChannel) (store.FulfillmentViewCounts, error) {
	c, err := s.orders.CountFulfillmentViews(ctx, tx, channel)
	if err != nil {
		return store.FulfillmentViewCounts{}, fmt.Errorf("count fulfillment views: %w", err)
	}
	return c, nil
}

// SumOrderRevenue returns the total revenue (in cents) for orders matching the filter.
func (s *OrderService) SumOrderRevenue(ctx context.Context, tx pgx.Tx, f store.OrderFilter) (int, error) {
	total, err := s.orders.SumOrderRevenue(ctx, tx, f)
	if err != nil {
		return 0, fmt.Errorf("sum order revenue: %w", err)
	}
	return total, nil
}

// RevenueByDay returns daily revenue (cents) and order counts in [from, to),
// bucketed in the merchant's local timezone. Cancelled and refunded orders are
// excluded. Days with zero orders are not returned.
func (s *OrderService) RevenueByDay(ctx context.Context, tx pgx.Tx, from, to time.Time, tz *time.Location) ([]domain.DailyRevenue, error) {
	rows, err := s.orders.RevenueByDay(ctx, tx, from, to, tz)
	if err != nil {
		return nil, fmt.Errorf("revenue by day: %w", err)
	}
	return rows, nil
}

// CountActiveCustomers returns the number of distinct customers who placed an
// order in each of the three activity windows, plus the equivalent prior
// windows. A non-nil channel scopes the counts to retail or wholesale.
func (s *OrderService) CountActiveCustomers(ctx context.Context, tx pgx.Tx, w store.ActiveCustomerWindows, channel *domain.OrderChannel) (store.ActiveCustomerCounts, error) {
	c, err := s.orders.CountActiveCustomers(ctx, tx, w, channel)
	if err != nil {
		return store.ActiveCustomerCounts{}, fmt.Errorf("count active customers: %w", err)
	}
	return c, nil
}

// TopProducts returns the top-N products in [from, to), ranked by the chosen
// metric. Cancelled and refunded orders are excluded.
func (s *OrderService) TopProducts(ctx context.Context, tx pgx.Tx, from, to time.Time, sort store.TopProductsSort, limit int) ([]domain.ProductSales, error) {
	rows, err := s.orders.TopProducts(ctx, tx, from, to, sort, limit)
	if err != nil {
		return nil, fmt.Errorf("top products: %w", err)
	}
	return rows, nil
}

// ListDeliveryLoad returns per-product unit and weight totals across the
// orders matching the filter — the delivery load list's rollup. Read-only; no
// audit record.
func (s *OrderService) ListDeliveryLoad(ctx context.Context, tx pgx.Tx, f store.OrderFilter) ([]domain.DeliveryLoadLine, error) {
	lines, err := s.orders.ListDeliveryLoad(ctx, tx, f)
	if err != nil {
		return nil, fmt.Errorf("list delivery load: %w", err)
	}
	return lines, nil
}

// ListLineItems returns all line items for an order.
func (s *OrderService) ListLineItems(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]domain.LineItem, error) {
	items, err := s.orders.ListLineItems(ctx, tx, orderID)
	if err != nil {
		return nil, fmt.Errorf("list line items: %w", err)
	}
	return items, nil
}

// ListAdjustments returns all adjustments for an order.
func (s *OrderService) ListAdjustments(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]domain.Adjustment, error) {
	adjs, err := s.orders.ListAdjustmentsByOrder(ctx, tx, orderID)
	if err != nil {
		return nil, fmt.Errorf("list adjustments: %w", err)
	}
	return adjs, nil
}

// GetOrderByStripePaymentIntentIDAsStaff returns an order by its Stripe PaymentIntent ID.
func (s *OrderService) GetOrderByStripePaymentIntentIDAsStaff(ctx context.Context, tx pgx.Tx, intentID string) (*domain.Order, error) {
	o, err := s.orders.GetOrderByStripePaymentIntentIDAsStaff(ctx, tx, intentID)
	if err != nil {
		return nil, fmt.Errorf("get order by stripe PI: %w", err)
	}
	return o, nil
}

// GetOrderByStripePaymentIntentIDForUpdate is the row-locking variant for
// flows that race on the same order — webhook + redirect-back, or two
// concurrent webhook deliveries. Callers must already be inside a transaction.
func (s *OrderService) GetOrderByStripePaymentIntentIDForUpdate(ctx context.Context, tx pgx.Tx, intentID string) (*domain.Order, error) {
	o, err := s.orders.GetOrderByStripePaymentIntentIDForUpdate(ctx, tx, intentID)
	if err != nil {
		return nil, fmt.Errorf("get order by stripe PI (for update): %w", err)
	}
	return o, nil
}

// ListAbandonedOrderIDs returns the IDs of pre-paid-intent orders that have
// been sitting in pending+awaiting longer than olderThan. The cleanup worker
// uses this to find orders whose customer never finished payment so they can
// be cancelled (releasing any held coupon).
func (s *OrderService) ListAbandonedOrderIDs(ctx context.Context, tx pgx.Tx, olderThan time.Time, limit int) ([]uuid.UUID, error) {
	ids, err := s.orders.ListAbandonedOrderIDs(ctx, tx, olderThan, limit)
	if err != nil {
		return nil, fmt.Errorf("list abandoned order ids: %w", err)
	}
	return ids, nil
}

// ListOrderIDsToAutoDeliver returns the IDs of orders still in
// fulfillment_status='shipped' whose ship time is older than olderThan. The
// auto-deliver sweep uses this to find orders the carrier never reported
// delivered for (legacy orders predating the shipping integration, or live
// orders that missed a tracking webhook) so they can be marked delivered.
func (s *OrderService) ListOrderIDsToAutoDeliver(ctx context.Context, tx pgx.Tx, olderThan time.Time, limit int) ([]uuid.UUID, error) {
	ids, err := s.orders.ListShippedOrderIDsDeliveredBefore(ctx, tx, olderThan, limit)
	if err != nil {
		return nil, fmt.Errorf("list orders to auto-deliver: %w", err)
	}
	return ids, nil
}

// UpdateOrderStatus updates the order's status and records an audit entry.
func (s *OrderService) UpdateOrderStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.OrderStatus, actor Actor) (*domain.Order, error) {
	o, err := s.orders.UpdateOrderStatus(ctx, tx, id, status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("update order status: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderStatusChanged,
		ResourceType: "order",
		ResourceID:   id,
		After:        o,
		Metadata:     map[string]any{"new_status": string(status)},
	}); err != nil {
		return nil, fmt.Errorf("audit order status changed: %w", err)
	}

	return o, nil
}

// --- Mutation methods ---

// CancelOrder cancels an order if allowed by the state machine and releases
// any coupon redemption tied to it. Idempotent: returns the order unchanged
// when it is already in cancelled state. Used by admin cancellation,
// payment_intent.canceled webhook, payment_intent.payment_failed webhook,
// and the abandoned-checkout cleanup job.
func (s *OrderService) CancelOrder(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*domain.Order, error) {
	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for cancel: %w", err)
	}

	// Idempotent: skip if already cancelled.
	if order.Status == domain.OrderStatusCancelled {
		return order, nil
	}

	if !canCancelOrder(order.Status) {
		return nil, ErrOrderNotCancellable
	}

	order, err = s.orders.UpdateOrderStatus(ctx, tx, id, domain.OrderStatusCancelled)
	if err != nil {
		return nil, fmt.Errorf("cancel order: %w", err)
	}

	// Release any coupon redemption tied to this order so the code can be
	// used again. Skipped if no discount store is wired (older test setups).
	if s.discounts != nil {
		if err := s.discounts.ReleaseCouponCodeByOrderID(ctx, tx, id); err != nil {
			return nil, fmt.Errorf("release coupon: %w", err)
		}
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderCancelled,
		ResourceType: "order",
		ResourceID:   id,
		After:        order,
	}); err != nil {
		return nil, fmt.Errorf("audit order cancelled: %w", err)
	}

	return order, nil
}

// RefundOrder marks an order as refunded if allowed by the state machine.
// The actual Stripe refund call must happen BEFORE this method is called.
func (s *OrderService) RefundOrder(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*domain.Order, error) {
	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for refund: %w", err)
	}

	if !canRefundOrder(order.Status, order.PaymentStatus) {
		return nil, ErrOrderNotRefundable
	}

	order, err = s.orders.UpdateOrderStatus(ctx, tx, id, domain.OrderStatusRefunded)
	if err != nil {
		return nil, fmt.Errorf("refund order status: %w", err)
	}

	order, err = s.orders.UpdateOrderPaymentStatus(ctx, tx, id, domain.PaymentStatusRefunded)
	if err != nil {
		return nil, fmt.Errorf("refund order payment status: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderRefunded,
		ResourceType: "order",
		ResourceID:   id,
		After:        order,
	}); err != nil {
		return nil, fmt.Errorf("audit order refunded: %w", err)
	}

	return order, nil
}

// FulfillOrder marks an order as fulfilled and moves it to processing.
func (s *OrderService) FulfillOrder(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*domain.Order, error) {
	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for fulfill: %w", err)
	}

	if order.FulfillmentStatus == domain.FulfillmentStatusFulfilled ||
		order.FulfillmentStatus == domain.FulfillmentStatusShipped ||
		order.FulfillmentStatus == domain.FulfillmentStatusDelivered {
		return nil, ErrOrderAlreadyFulfilled
	}

	order, err = s.orders.UpdateOrderFulfillmentStatus(ctx, tx, id, domain.FulfillmentStatusFulfilled)
	if err != nil {
		return nil, fmt.Errorf("fulfill order: %w", err)
	}

	order, err = s.orders.UpdateOrderStatus(ctx, tx, id, domain.OrderStatusProcessing)
	if err != nil {
		return nil, fmt.Errorf("fulfill order status: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderFulfilled,
		ResourceType: "order",
		ResourceID:   id,
		After:        order,
	}); err != nil {
		return nil, fmt.Errorf("audit order fulfilled: %w", err)
	}

	return order, nil
}

// ChangeLineItemVariant swaps a line item to a sibling variant on the same
// product, e.g., switching grind from Whole Bean to Drip after the order has
// been placed. The line item's unit price and totals are preserved — the new
// variant must have the same base price (in the order's currency) as the
// current variant, so any subscription/wholesale discount baked into the
// existing unit price stays mathematically consistent. Records an audit
// entry capturing the old and new variant IDs and SKUs.
func (s *OrderService) ChangeLineItemVariant(ctx context.Context, tx pgx.Tx, orderID, lineItemID, newVariantID uuid.UUID, actor Actor) (*domain.LineItem, error) {
	if s.catalog == nil || s.pricing == nil {
		return nil, fmt.Errorf("change line item variant: service not wired with catalog/pricing")
	}

	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order: %w", err)
	}
	if !canEditOrderLineItems(order) {
		return nil, ErrOrderNotEditable
	}

	li, err := s.orders.GetLineItem(ctx, tx, lineItemID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrLineItemNotFound
		}
		return nil, fmt.Errorf("get line item: %w", err)
	}
	if li.OrderID != orderID {
		return nil, ErrLineItemNotInOrder
	}
	if li.VariantID == newVariantID {
		return li, nil
	}

	oldVariant, err := s.catalog.GetVariantByID(ctx, tx, li.VariantID)
	if err != nil {
		return nil, fmt.Errorf("get current variant: %w", err)
	}
	newVariant, err := s.catalog.GetVariantByID(ctx, tx, newVariantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVariantNotFound
		}
		return nil, fmt.Errorf("get new variant: %w", err)
	}
	if newVariant.ProductID != oldVariant.ProductID {
		return nil, ErrVariantNotOnSameProduct
	}
	if newVariant.ArchivedAt != nil {
		return nil, ErrVariantArchived
	}

	oldPrice, err := s.pricing.GetBasePrice(ctx, tx, oldVariant.ID, order.CurrencyCode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPriceNotFound
		}
		return nil, fmt.Errorf("get current variant price: %w", err)
	}
	newPrice, err := s.pricing.GetBasePrice(ctx, tx, newVariantID, order.CurrencyCode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPriceNotFound
		}
		return nil, fmt.Errorf("get new variant price: %w", err)
	}
	if newPrice.Amount != oldPrice.Amount {
		return nil, ErrVariantPriceMismatch
	}

	updated, err := s.orders.UpdateLineItemVariant(ctx, tx, lineItemID, newVariantID)
	if err != nil {
		return nil, fmt.Errorf("update line item variant: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderLineItemVariantChanged,
		ResourceType: "order",
		ResourceID:   orderID,
		After:        updated,
		Metadata: map[string]any{
			"line_item_id":    lineItemID,
			"old_variant_id":  oldVariant.ID,
			"new_variant_id":  newVariant.ID,
			"old_variant_sku": oldVariant.SKU,
			"new_variant_sku": newVariant.SKU,
		},
	}); err != nil {
		return nil, fmt.Errorf("audit line item variant changed: %w", err)
	}

	return updated, nil
}

// ShipOrder marks an order as shipped and moves it to complete.
func (s *OrderService) ShipOrder(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*domain.Order, error) {
	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for ship: %w", err)
	}

	if order.FulfillmentStatus != domain.FulfillmentStatusFulfilled {
		return nil, fmt.Errorf("order must be fulfilled before shipping: %w", ErrInvalidOrderStatus)
	}

	order, err = s.orders.UpdateOrderFulfillmentStatus(ctx, tx, id, domain.FulfillmentStatusShipped)
	if err != nil {
		return nil, fmt.Errorf("ship order: %w", err)
	}

	order, err = s.orders.UpdateOrderStatus(ctx, tx, id, domain.OrderStatusComplete)
	if err != nil {
		return nil, fmt.Errorf("ship order status: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderShipped,
		ResourceType: "order",
		ResourceID:   id,
		After:        order,
	}); err != nil {
		return nil, fmt.Errorf("audit order shipped: %w", err)
	}

	// Notify the customer with their tracking number. This used to be enqueued
	// by the Pirate Ship CSV tracking import; when that was removed in favour
	// of Shippo (6ba17ee) nothing took over, and no shipped email went out at
	// all. Marking shipped is the moment the customer's parcel leaves, so it
	// is the right hook — and re-running is safe because ShipOrder only
	// accepts an order still in "fulfilled".
	//
	// Skipped for guest orders (no account to mail) and for orders shipped
	// without a label: the email is built entirely around carrier, service and
	// tracking number, so with no shipment there is nothing to tell them.
	// canShipOrder deliberately allows that case, so it is not an error.
	if s.enqueuer != nil && s.shipments != nil && order.CustomerID != nil {
		shipment, err := s.latestTrackedShipment(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if shipment != nil {
			if err := s.enqueuer.EnqueueOrderShipped(ctx, tx, order.ID, *order.CustomerID, shipment.ID); err != nil {
				return nil, fmt.Errorf("enqueue order shipped email: %w", err)
			}
		}
	}

	return order, nil
}

// latestTrackedShipment returns the most recent shipment on an order that has
// a tracking number, or nil when the order has none. Shipments come back
// oldest-first, so the last match wins — a re-bought label supersedes the one
// it replaced.
func (s *OrderService) latestTrackedShipment(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (*domain.Shipment, error) {
	shipments, err := s.shipments.ListShipmentsByOrder(ctx, tx, orderID)
	if err != nil {
		return nil, fmt.Errorf("list shipments for shipped email: %w", err)
	}
	var latest *domain.Shipment
	for i := range shipments {
		if shipments[i].TrackingNumber != "" {
			latest = &shipments[i]
		}
	}
	return latest, nil
}

// RevertFulfillment moves an order from fulfilled back to unfulfilled when
// staff marked it by mistake. Order status flips processing → confirmed to
// stay consistent with FulfillOrder. Payment status is left untouched. Not
// allowed once the order has shipped, been delivered, picked up, or moved
// to a local-fulfillment state — those have their own undo paths.
func (s *OrderService) RevertFulfillment(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*domain.Order, error) {
	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for revert fulfillment: %w", err)
	}

	if order.FulfillmentStatus != domain.FulfillmentStatusFulfilled {
		return nil, ErrOrderFulfillmentNotRevertible
	}

	order, err = s.orders.UpdateOrderFulfillmentStatus(ctx, tx, id, domain.FulfillmentStatusUnfulfilled)
	if err != nil {
		return nil, fmt.Errorf("revert fulfillment: %w", err)
	}

	if order.Status == domain.OrderStatusProcessing {
		order, err = s.orders.UpdateOrderStatus(ctx, tx, id, domain.OrderStatusConfirmed)
		if err != nil {
			return nil, fmt.Errorf("revert fulfillment status: %w", err)
		}
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderFulfillmentReverted,
		ResourceType: "order",
		ResourceID:   id,
		After:        order,
	}); err != nil {
		return nil, fmt.Errorf("audit revert fulfillment: %w", err)
	}

	return order, nil
}

// RevertShipment moves an order from shipped back to fulfilled when staff
// marked it shipped by mistake. Order status flips complete → processing.
// The shipping label and customer notification email are NOT undone — staff
// must communicate any correction manually.
func (s *OrderService) RevertShipment(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*domain.Order, error) {
	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for revert shipment: %w", err)
	}

	if order.FulfillmentStatus != domain.FulfillmentStatusShipped {
		return nil, ErrOrderShipmentNotRevertible
	}

	order, err = s.orders.UpdateOrderFulfillmentStatus(ctx, tx, id, domain.FulfillmentStatusFulfilled)
	if err != nil {
		return nil, fmt.Errorf("revert shipment: %w", err)
	}

	if order.Status == domain.OrderStatusComplete {
		order, err = s.orders.UpdateOrderStatus(ctx, tx, id, domain.OrderStatusProcessing)
		if err != nil {
			return nil, fmt.Errorf("revert shipment status: %w", err)
		}
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderShipmentReverted,
		ResourceType: "order",
		ResourceID:   id,
		After:        order,
	}); err != nil {
		return nil, fmt.Errorf("audit revert shipment: %w", err)
	}

	return order, nil
}

// MarkReadyForPickup transitions a pickup order to the ready_for_pickup
// state and enqueues the "ready" notification. Allowed only for orders whose
// shipping_method is pickup; the previous fulfillment status must be
// unfulfilled or fulfilled (the latter accommodates a "fulfilled then made
// ready" two-step workflow if staff prefer that).
func (s *OrderService) MarkReadyForPickup(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*domain.Order, error) {
	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for ready-for-pickup: %w", err)
	}

	if order.ShippingMethod == nil || *order.ShippingMethod != domain.ShippingMethodPickup {
		return nil, fmt.Errorf("order is not a pickup order: %w", ErrInvalidOrderStatus)
	}
	switch order.FulfillmentStatus {
	case domain.FulfillmentStatusUnfulfilled, domain.FulfillmentStatusFulfilled:
		// allowed
	default:
		return nil, fmt.Errorf("order cannot be marked ready: %w", ErrInvalidOrderStatus)
	}

	order, err = s.orders.UpdateOrderFulfillmentStatus(ctx, tx, id, domain.FulfillmentStatusReadyForPickup)
	if err != nil {
		return nil, fmt.Errorf("set ready for pickup: %w", err)
	}
	if order.Status != domain.OrderStatusProcessing && order.Status != domain.OrderStatusComplete {
		order, err = s.orders.UpdateOrderStatus(ctx, tx, id, domain.OrderStatusProcessing)
		if err != nil {
			return nil, fmt.Errorf("ready-for-pickup status: %w", err)
		}
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderReadyForPickup,
		ResourceType: "order",
		ResourceID:   id,
		After:        order,
	}); err != nil {
		return nil, fmt.Errorf("audit ready-for-pickup: %w", err)
	}

	if s.enqueuer != nil && order.CustomerID != nil {
		if err := s.enqueuer.EnqueueOrderReadyForPickup(ctx, tx, order.ID, *order.CustomerID); err != nil {
			return nil, fmt.Errorf("enqueue ready-for-pickup email: %w", err)
		}
	}

	return order, nil
}

// SwitchToPickup moves a local-delivery order to pickup at the customer's
// request, following the one-click link in their confirmation email.
//
// changed is false when the order is already on pickup. That is a success, not
// an error: a customer who clicks the link twice, or whose mail client prefetches
// it, should see "you're set for pickup" rather than a failure page.
//
// No totals change. Local delivery and pickup are both free
// (ShippingConfig.CalculateForMethod), so the switch cannot alter what was
// charged — which is what makes it safe to expose without re-authentication.
//
// The order must still be unfulfilled. Once it has been packed, dispatched, or
// cancelled the answer is a phone call to the shop, not a database write.
func (s *OrderService) SwitchToPickup(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (_ *domain.Order, changed bool, err error) {
	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrOrderNotFound
		}
		return nil, false, fmt.Errorf("get order for switch-to-pickup: %w", err)
	}

	// Already switched — report success without touching the row.
	if order.ShippingMethod != nil && *order.ShippingMethod == domain.ShippingMethodPickup {
		return order, false, nil
	}
	if order.ShippingMethod == nil || *order.ShippingMethod != domain.ShippingMethodLocalDelivery {
		return nil, false, ErrOrderNotSwitchable
	}
	if order.FulfillmentStatus != domain.FulfillmentStatusUnfulfilled {
		return nil, false, ErrOrderNotSwitchable
	}
	if order.Status == domain.OrderStatusCancelled || order.Status == domain.OrderStatusRefunded {
		return nil, false, ErrOrderNotSwitchable
	}

	// Offering pickup for an order the shop cannot actually fulfill that way
	// would strand it in a queue nobody works. Checked against live config
	// rather than the emailed link, so turning pickup off in admin closes the
	// door on links already sitting in inboxes.
	if s.shipments == nil {
		return nil, false, ErrPickupUnavailable
	}
	cfg, err := s.shipments.GetConfig(ctx, tx)
	if err != nil {
		return nil, false, fmt.Errorf("get shipping config for switch-to-pickup: %w", err)
	}
	if !cfg.LocalPickupEnabled {
		return nil, false, ErrPickupUnavailable
	}

	updated, ok, err := s.orders.SwitchOrderToPickup(ctx, tx, id)
	if err != nil {
		return nil, false, fmt.Errorf("switch order to pickup: %w", err)
	}
	if !ok {
		// Lost a race with staff editing the method between the read above and
		// this write.
		return nil, false, ErrOrderNotSwitchable
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderSwitchedToPickup,
		ResourceType: "order",
		ResourceID:   id,
		After:        updated,
		Metadata: map[string]any{
			"from":                   string(domain.ShippingMethodLocalDelivery),
			"released_delivery_date": formatScheduledDate(order.ScheduledDeliveryDate),
			"via":                    "confirmation_email_link",
		},
	}); err != nil {
		return nil, false, fmt.Errorf("audit switch-to-pickup: %w", err)
	}

	return updated, true, nil
}

// formatScheduledDate renders a delivery date for the audit log, or "" when the
// order carried none. Recording the date the order gave up makes it possible to
// answer "what were they originally promised?" after the column has been
// cleared.
func formatScheduledDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

// MarkPickedUp transitions a ready_for_pickup order to delivered/complete.
// No email — the customer already knows; this is the staff bookkeeping
// counterpart to MarkReadyForPickup.
func (s *OrderService) MarkPickedUp(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*domain.Order, error) {
	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for picked-up: %w", err)
	}

	if order.FulfillmentStatus != domain.FulfillmentStatusReadyForPickup {
		return nil, fmt.Errorf("order is not ready for pickup: %w", ErrInvalidOrderStatus)
	}

	order, err = s.orders.UpdateOrderFulfillmentStatus(ctx, tx, id, domain.FulfillmentStatusDelivered)
	if err != nil {
		return nil, fmt.Errorf("set delivered: %w", err)
	}
	order, err = s.orders.UpdateOrderStatus(ctx, tx, id, domain.OrderStatusComplete)
	if err != nil {
		return nil, fmt.Errorf("complete order: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderPickedUp,
		ResourceType: "order",
		ResourceID:   id,
		After:        order,
	}); err != nil {
		return nil, fmt.Errorf("audit picked-up: %w", err)
	}

	return order, nil
}

// MarkLocallyDelivered closes out a local delivery: the driver handed the
// coffee over at the door.
//
// This exists because neither sibling fits. MarkOrderDelivered guards on
// shipped/partially_shipped, and a van delivery is never "shipped" — there is
// no carrier and no tracking number. ReconcileDelivery recomputes from shipment
// rows, of which these orders have none. MarkPickedUp is the closest shape
// (ready_for_pickup → delivered + complete, audited, no email) and this is its
// delivery-side counterpart.
//
// Guarded to the pre-handoff fulfillment states so a double-tap on the driver's
// phone is a no-op rather than a second audit entry: callers treat
// ErrInvalidOrderStatus as "already done, carry on".
//
// No email. The customer just watched someone hand them a bag of coffee; a
// receipt confirming it arrived would be noise.
func (s *OrderService) MarkLocallyDelivered(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*domain.Order, error) {
	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for local delivery: %w", err)
	}

	switch order.FulfillmentStatus {
	case domain.FulfillmentStatusUnfulfilled,
		domain.FulfillmentStatusPartiallyFulfilled,
		domain.FulfillmentStatusFulfilled:
		// allowed — these are the states a queued delivery order sits in
	default:
		return nil, fmt.Errorf("order is not awaiting local delivery: %w", ErrInvalidOrderStatus)
	}

	from := order.FulfillmentStatus
	order, err = s.orders.UpdateOrderFulfillmentStatus(ctx, tx, id, domain.FulfillmentStatusDelivered)
	if err != nil {
		return nil, fmt.Errorf("set delivered: %w", err)
	}
	// Unlike the carrier path, order status advances too: a hand-delivered
	// order is finished, with nothing left to reconcile.
	order, err = s.orders.UpdateOrderStatus(ctx, tx, id, domain.OrderStatusComplete)
	if err != nil {
		return nil, fmt.Errorf("complete order: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderDelivered,
		ResourceType: "order",
		ResourceID:   id,
		After:        order,
		Metadata: map[string]any{
			"from_status": string(from),
			"to_status":   string(domain.FulfillmentStatusDelivered),
			"source":      "driver_route",
		},
	}); err != nil {
		return nil, fmt.Errorf("audit local delivery: %w", err)
	}

	return order, nil
}

// MarkOrderDelivered advances a shipped order to delivered. It is the blunt,
// signal-free transition used by the auto-deliver sweep: carrier delivery is
// never reported for these orders, so after a grace window the package is
// assumed arrived. ReconcileDelivery is the precise, tracking-driven
// counterpart for live shipments.
//
// Guarded to shipped/partially_shipped so a concurrent transition (e.g. a
// tracking webhook that reconciled the order between the sweep's list and its
// per-order update) is a no-op: callers treat ErrInvalidOrderStatus as "already
// handled, skip". Order status is left untouched — a shipped order is already
// complete, and delivery is a fulfillment-level fact.
func (s *OrderService) MarkOrderDelivered(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*domain.Order, error) {
	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for auto-deliver: %w", err)
	}

	switch order.FulfillmentStatus {
	case domain.FulfillmentStatusShipped, domain.FulfillmentStatusPartiallyShipped:
		// allowed
	default:
		return nil, fmt.Errorf("order is not shipped: %w", ErrInvalidOrderStatus)
	}

	from := order.FulfillmentStatus
	order, err = s.orders.UpdateOrderFulfillmentStatus(ctx, tx, id, domain.FulfillmentStatusDelivered)
	if err != nil {
		return nil, fmt.Errorf("set delivered: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderDelivered,
		ResourceType: "order",
		ResourceID:   id,
		After:        order,
		Metadata: map[string]any{
			"from_status": string(from),
			"to_status":   string(domain.FulfillmentStatusDelivered),
			"source":      "auto_deliver_sweep",
		},
	}); err != nil {
		return nil, fmt.Errorf("audit auto-deliver: %w", err)
	}

	return order, nil
}

// ReconcileDelivery recomputes an order's fulfillment status from its shipment
// rows after one of them reaches delivered. Called by the Shippo tracking path
// in the same transaction that marks a shipment delivered: when every shipment
// is delivered the order becomes delivered, when only some are it becomes
// partially_delivered.
//
// Only orders currently in shipped/partially_shipped are advanced — any other
// state (returned, already delivered, pre-ship) is left alone, making the call
// a safe no-op on replayed or out-of-order webhooks. Returns the order
// unchanged when there is nothing to do.
func (s *OrderService) ReconcileDelivery(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, actor Actor) (*domain.Order, error) {
	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for delivery reconcile: %w", err)
	}

	switch order.FulfillmentStatus {
	case domain.FulfillmentStatusShipped, domain.FulfillmentStatusPartiallyShipped:
		// advanceable
	default:
		return order, nil
	}

	shipments, err := s.shipments.ListShipmentsByOrder(ctx, tx, orderID)
	if err != nil {
		return nil, fmt.Errorf("list shipments for delivery reconcile: %w", err)
	}
	if len(shipments) == 0 {
		return order, nil
	}

	allDelivered := true
	for _, sh := range shipments {
		if sh.Status != domain.ShipmentStatusDelivered {
			allDelivered = false
			break
		}
	}

	target := domain.FulfillmentStatusPartiallyDelivered
	if allDelivered {
		target = domain.FulfillmentStatusDelivered
	}
	if order.FulfillmentStatus == target {
		return order, nil
	}

	from := order.FulfillmentStatus
	order, err = s.orders.UpdateOrderFulfillmentStatus(ctx, tx, orderID, target)
	if err != nil {
		return nil, fmt.Errorf("set delivered from reconcile: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderDelivered,
		ResourceType: "order",
		ResourceID:   orderID,
		After:        order,
		Metadata: map[string]any{
			"from_status":    string(from),
			"to_status":      string(target),
			"shipment_count": len(shipments),
			"source":         "shippo_tracking",
		},
	}); err != nil {
		return nil, fmt.Errorf("audit delivery reconcile: %w", err)
	}

	return order, nil
}

// MarkOutForDelivery transitions a local-delivery order from unfulfilled to
// shipped (reusing the existing terminal-shipping vocabulary) and enqueues
// the "out for delivery today" email. Local delivery doesn't get a tracking
// number, so the EnqueueOrderShipped path isn't suitable — the courier is the
// shop owner and the customer just needs to know it's coming.
func (s *OrderService) MarkOutForDelivery(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*domain.Order, error) {
	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for out-for-delivery: %w", err)
	}

	if order.ShippingMethod == nil || *order.ShippingMethod != domain.ShippingMethodLocalDelivery {
		return nil, fmt.Errorf("order is not a local delivery order: %w", ErrInvalidOrderStatus)
	}
	switch order.FulfillmentStatus {
	case domain.FulfillmentStatusUnfulfilled, domain.FulfillmentStatusFulfilled:
		// allowed
	default:
		return nil, fmt.Errorf("order cannot be marked out-for-delivery: %w", ErrInvalidOrderStatus)
	}

	order, err = s.orders.UpdateOrderFulfillmentStatus(ctx, tx, id, domain.FulfillmentStatusShipped)
	if err != nil {
		return nil, fmt.Errorf("set out-for-delivery: %w", err)
	}
	if order.Status != domain.OrderStatusComplete {
		order, err = s.orders.UpdateOrderStatus(ctx, tx, id, domain.OrderStatusComplete)
		if err != nil {
			return nil, fmt.Errorf("complete order: %w", err)
		}
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderOutForDelivery,
		ResourceType: "order",
		ResourceID:   id,
		After:        order,
	}); err != nil {
		return nil, fmt.Errorf("audit out-for-delivery: %w", err)
	}

	if s.enqueuer != nil && order.CustomerID != nil {
		if err := s.enqueuer.EnqueueOrderOutForDelivery(ctx, tx, order.ID, *order.CustomerID); err != nil {
			return nil, fmt.Errorf("enqueue out-for-delivery email: %w", err)
		}
	}

	return order, nil
}

// --- Bulk fulfillment verbs ---

// BatchOutcome reports the result of a bulk order operation. Succeeded and
// Failed together cover the full input ID set; their union has no duplicates.
type BatchOutcome struct {
	Succeeded []uuid.UUID
	Failed    []BatchFailure
}

// BatchFailure carries one per-order rejection from a bulk operation. Reason
// is human-readable and safe to surface to staff UI — short phrases like
// "already shipped" or "wrong shipping method", not raw error strings.
type BatchFailure struct {
	OrderID uuid.UUID
	Reason  string
}

// MarkReadyForPickupBatch applies MarkReadyForPickup to each ID independently.
// Same per-order independence and ordering guarantees as MarkPickedUpBatch.
// Each successful row enqueues a "your order is ready" email in its own tx.
func (s *OrderService) MarkReadyForPickupBatch(ctx context.Context, pool *pgxpool.Pool, ids []uuid.UUID, actor Actor) (BatchOutcome, error) {
	return s.runBulkOrderVerb(ctx, pool, ids, func(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
		_, err := s.MarkReadyForPickup(ctx, tx, id, actor)
		return err
	})
}

// MarkPickedUpBatch applies MarkPickedUp to each ID independently. Each order
// runs in its own transaction so one failure never poisons the rest; rejected
// orders end up in Failed with a short staff-facing reason. Input order is
// preserved in both Succeeded and Failed. An empty ids slice returns an empty
// outcome and nil error.
func (s *OrderService) MarkPickedUpBatch(ctx context.Context, pool *pgxpool.Pool, ids []uuid.UUID, actor Actor) (BatchOutcome, error) {
	return s.runBulkOrderVerb(ctx, pool, ids, func(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
		_, err := s.MarkPickedUp(ctx, tx, id, actor)
		return err
	})
}

// MarkOutForDeliveryBatch applies MarkOutForDelivery to each ID independently.
// Same per-order independence and ordering guarantees as MarkPickedUpBatch.
func (s *OrderService) MarkOutForDeliveryBatch(ctx context.Context, pool *pgxpool.Pool, ids []uuid.UUID, actor Actor) (BatchOutcome, error) {
	return s.runBulkOrderVerb(ctx, pool, ids, func(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
		_, err := s.MarkOutForDelivery(ctx, tx, id, actor)
		return err
	})
}

// runBulkOrderVerb is the shared loop body for the bulk fulfillment methods.
// It walks ids in order, opens a fresh transaction per id, invokes the
// per-order verb, and assembles a BatchOutcome. A single iteration's failure
// becomes a BatchFailure with a translated reason; the loop never short-
// circuits. The returned error is reserved for cases where the iteration
// could not even be attempted (e.g., a context cancellation).
func (s *OrderService) runBulkOrderVerb(
	ctx context.Context,
	pool *pgxpool.Pool,
	ids []uuid.UUID,
	verb func(ctx context.Context, tx pgx.Tx, id uuid.UUID) error,
) (BatchOutcome, error) {
	out := BatchOutcome{}
	if len(ids) == 0 {
		return out, nil
	}
	out.Succeeded = make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return out, fmt.Errorf("bulk order verb: %w", err)
		}
		err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
			return verb(ctx, tx, id)
		})
		if err != nil {
			out.Failed = append(out.Failed, BatchFailure{
				OrderID: id,
				Reason:  failureReasonFor(err),
			})
			continue
		}
		out.Succeeded = append(out.Succeeded, id)
	}
	return out, nil
}

// failureReasonFor translates a service-layer error into a short, staff-
// friendly phrase suitable for the batch-result UI. Known sentinels get
// canonical phrases; wrapped ErrInvalidOrderStatus messages take their
// human-readable prefix from the wrap (e.g., "order is not ready for
// pickup"); unknown errors fall back to err.Error() so nothing is silently
// swallowed.
func failureReasonFor(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrOrderNotFound) {
		return "order not found"
	}
	if errors.Is(err, ErrInvalidOrderStatus) {
		msg := err.Error()
		if i := strings.Index(msg, ": "); i > 0 {
			return msg[:i]
		}
		return "invalid status"
	}
	return err.Error()
}

// SwapLocalShippingMethod swaps an order's shipping method between
// local_delivery and local_pickup. Used when a customer changes their mind
// after placing the order ("I'll come pick it up instead of you delivering").
//
// Allowed only while the order hasn't physically left the shop yet — i.e.
// fulfillment status is unfulfilled or fulfilled, and the order isn't
// cancelled or refunded. Cross-method swaps to/from shipped are rejected;
// the carrier flow has its own label/tracking concerns and isn't safe to
// flip in a single click.
//
// No customer email is sent — staff are doing this because they already
// talked to the customer.
func (s *OrderService) SwapLocalShippingMethod(ctx context.Context, tx pgx.Tx, id uuid.UUID, target domain.ShippingMethod, actor Actor) (*domain.Order, error) {
	if target != domain.ShippingMethodPickup && target != domain.ShippingMethodLocalDelivery {
		return nil, fmt.Errorf("target must be pickup or local_delivery: %w", ErrInvalidOrderStatus)
	}

	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for shipping-method swap: %w", err)
	}

	if order.ShippingMethod == nil ||
		(*order.ShippingMethod != domain.ShippingMethodPickup && *order.ShippingMethod != domain.ShippingMethodLocalDelivery) {
		return nil, fmt.Errorf("order is not a local fulfillment order: %w", ErrInvalidOrderStatus)
	}
	if *order.ShippingMethod == target {
		return order, nil
	}
	if order.Status == domain.OrderStatusCancelled || order.Status == domain.OrderStatusRefunded {
		return nil, fmt.Errorf("cannot swap shipping method on cancelled/refunded order: %w", ErrInvalidOrderStatus)
	}
	switch order.FulfillmentStatus {
	case domain.FulfillmentStatusUnfulfilled, domain.FulfillmentStatusFulfilled:
		// allowed
	default:
		return nil, fmt.Errorf("order has already left the shop: %w", ErrInvalidOrderStatus)
	}

	previous := *order.ShippingMethod
	order, err = s.orders.UpdateOrderShippingMethod(ctx, tx, id, target)
	if err != nil {
		return nil, fmt.Errorf("set shipping method: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderShippingMethodChanged,
		ResourceType: "order",
		ResourceID:   id,
		After:        order,
		Metadata: map[string]any{
			"from": string(previous),
			"to":   string(target),
		},
	}); err != nil {
		return nil, fmt.Errorf("audit shipping-method swap: %w", err)
	}

	return order, nil
}

// ConvertLocalOrderToShipped moves a local-fulfillment order (pickup or
// local_delivery) onto the standard carrier "shipped" channel so staff can buy
// a label and mail it out — used when a customer who checked out as local turns
// out to need it posted instead.
//
// Unlike SwapLocalShippingMethod this is a deliberate, explicit one-way action:
// once the order is "shipped" the order page surfaces the rate/label flow.
// Shipping is comped — local orders carry no shipping line and staff make this
// change as a courtesy after talking to the customer, so we leave the order
// total untouched and do not re-charge or recompute tax. No customer email is
// sent. Valid only while the order is still in the shop (unfulfilled or
// fulfilled) and not cancelled/refunded.
func (s *OrderService) ConvertLocalOrderToShipped(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*domain.Order, error) {
	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for shipped conversion: %w", err)
	}

	if order.ShippingMethod == nil ||
		(*order.ShippingMethod != domain.ShippingMethodPickup && *order.ShippingMethod != domain.ShippingMethodLocalDelivery) {
		return nil, fmt.Errorf("order is not a local fulfillment order: %w", ErrInvalidOrderStatus)
	}
	if order.Status == domain.OrderStatusCancelled || order.Status == domain.OrderStatusRefunded {
		return nil, fmt.Errorf("cannot convert cancelled/refunded order to shipped: %w", ErrInvalidOrderStatus)
	}
	switch order.FulfillmentStatus {
	case domain.FulfillmentStatusUnfulfilled, domain.FulfillmentStatusFulfilled:
		// allowed
	default:
		return nil, fmt.Errorf("order has already left the shop: %w", ErrInvalidOrderStatus)
	}

	previous := *order.ShippingMethod
	order, err = s.orders.UpdateOrderShippingMethod(ctx, tx, id, domain.ShippingMethodShipped)
	if err != nil {
		return nil, fmt.Errorf("set shipping method: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderShippingMethodChanged,
		ResourceType: "order",
		ResourceID:   id,
		After:        order,
		Metadata: map[string]any{
			"from": string(previous),
			"to":   string(domain.ShippingMethodShipped),
		},
	}); err != nil {
		return nil, fmt.Errorf("audit shipped conversion: %w", err)
	}

	return order, nil
}

// UpdateFulfillmentStatus updates an order's fulfillment status.
func (s *OrderService) UpdateFulfillmentStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.FulfillmentStatus, actor Actor) (*domain.Order, error) {
	order, err := s.orders.UpdateOrderFulfillmentStatus(ctx, tx, id, status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("update fulfillment status: %w", err)
	}

	if status == domain.FulfillmentStatusFulfilled {
		if err := s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    actor.Type,
			ActorID:      actor.ID,
			ActorName:    actor.Name,
			Action:       audit.AuditOrderFulfilled,
			ResourceType: "order",
			ResourceID:   id,
			After:        order,
		}); err != nil {
			return nil, fmt.Errorf("audit order fulfilled: %w", err)
		}
	}

	return order, nil
}

// UpdatePaymentStatus updates an order's payment status.
func (s *OrderService) UpdatePaymentStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.PaymentStatus, actor Actor) (*domain.Order, error) {
	order, err := s.orders.UpdateOrderPaymentStatus(ctx, tx, id, status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("update payment status: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderStatusChanged,
		ResourceType: "order",
		ResourceID:   id,
		After:        order,
	}); err != nil {
		return nil, fmt.Errorf("audit payment status changed: %w", err)
	}

	return order, nil
}

// UpdateCartDiscount updates a cart's applied discount and coupon code.
func (s *OrderService) UpdateCartDiscount(ctx context.Context, tx pgx.Tx, cartID uuid.UUID, discountID, couponCodeID *uuid.UUID) (*domain.Cart, error) {
	cart, err := s.orders.UpdateCartDiscount(ctx, tx, cartID, discountID, couponCodeID)
	if err != nil {
		return nil, fmt.Errorf("update cart discount: %w", err)
	}
	return cart, nil
}

// UpdateStripePaymentIntentID sets the Stripe PaymentIntent ID on an order.
func (s *OrderService) UpdateStripePaymentIntentID(ctx context.Context, tx pgx.Tx, id uuid.UUID, intentID string) (*domain.Order, error) {
	order, err := s.orders.UpdateOrderStripePaymentIntentID(ctx, tx, id, intentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("update stripe payment intent id: %w", err)
	}
	return order, nil
}
