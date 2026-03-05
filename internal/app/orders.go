package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// OrderService contains business logic for orders and carts.
type OrderService struct {
	orders  *store.OrderStore
	audit   *audit.AuditWriter
	metrics *metrics.Registry
}

// NewOrderService creates a new OrderService.
func NewOrderService(orders *store.OrderStore, audit *audit.AuditWriter, metrics *metrics.Registry) *OrderService {
	return &OrderService{
		orders:  orders,
		audit:   audit,
		metrics: metrics,
	}
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

// GetOrder returns an order by ID.
func (s *OrderService) GetOrder(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Order, error) {
	o, err := s.orders.GetOrderByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order: %w", err)
	}
	return o, nil
}

// GetOrderByNumber returns an order by its number.
func (s *OrderService) GetOrderByNumber(ctx context.Context, tx pgx.Tx, number string) (*domain.Order, error) {
	o, err := s.orders.GetOrderByNumber(ctx, tx, number)
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

// --- Mutation methods ---

// CancelOrder cancels an order if allowed by the state machine.
func (s *OrderService) CancelOrder(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*domain.Order, error) {
	order, err := s.orders.GetOrderByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for cancel: %w", err)
	}

	if !canCancelOrder(order.Status) {
		return nil, ErrOrderNotCancellable
	}

	order, err = s.orders.UpdateOrderStatus(ctx, tx, id, domain.OrderStatusCancelled)
	if err != nil {
		return nil, fmt.Errorf("cancel order: %w", err)
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
	order, err := s.orders.GetOrderByID(ctx, tx, id)
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
