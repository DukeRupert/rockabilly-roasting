package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// OrderService contains business logic for orders and carts.
type OrderService struct {
	orders        *store.OrderStore
	audit         *audit.AuditWriter
	metrics       *metrics.Registry
	customers     *store.CustomerStore     // populated via WithEmail; required for Send* methods
	catalog       *store.CatalogStore      // populated via WithEmail; required for Send* methods
	subscriptions *store.SubscriptionStore // populated via WithEmail; used by SendRenewalReceiptEmail to show next-charge date
	email         EmailEnv                 // populated via WithEmail; required for Send* methods
	discounts     *store.DiscountStore     // populated via WithDiscounts; required for CancelOrder coupon release
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
func (s *OrderService) WithEmail(env EmailEnv, customers *store.CustomerStore, catalog *store.CatalogStore, subscriptions *store.SubscriptionStore) *OrderService {
	s.email = env
	s.customers = customers
	s.catalog = catalog
	s.subscriptions = subscriptions
	return s
}

// WithDiscounts attaches the discount store used by CancelOrder to release a
// coupon redemption when the order is cancelled. Must be called at wiring
// time before any cancellation path runs.
func (s *OrderService) WithDiscounts(discounts *store.DiscountStore) *OrderService {
	s.discounts = discounts
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

// TopProducts returns the top-N products in [from, to), ranked by the chosen
// metric. Cancelled and refunded orders are excluded.
func (s *OrderService) TopProducts(ctx context.Context, tx pgx.Tx, from, to time.Time, sort store.TopProductsSort, limit int) ([]domain.ProductSales, error) {
	rows, err := s.orders.TopProducts(ctx, tx, from, to, sort, limit)
	if err != nil {
		return nil, fmt.Errorf("top products: %w", err)
	}
	return rows, nil
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
