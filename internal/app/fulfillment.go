package app

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/shipping"
	"github.com/dukerupert/hiri/internal/store"
)

// FulfillmentService contains business logic for fulfillment and shipping.
type FulfillmentService struct {
	fulfillment   *store.FulfillmentStore
	shipments     *store.ShippingStore
	orders        *store.OrderStore
	labelProvider shipping.LabelProvider
	audit         *audit.AuditWriter
	metrics       *metrics.Registry
}

// NewFulfillmentService creates a new FulfillmentService.
func NewFulfillmentService(
	fulfillment *store.FulfillmentStore,
	shipments *store.ShippingStore,
	orders *store.OrderStore,
	labelProvider shipping.LabelProvider,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *FulfillmentService {
	return &FulfillmentService{
		fulfillment:   fulfillment,
		shipments:     shipments,
		orders:        orders,
		labelProvider: labelProvider,
		audit:         audit,
		metrics:       metrics,
	}
}

// ListShipmentsByOrder returns all shipments for an order.
func (s *FulfillmentService) ListShipmentsByOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]domain.Shipment, error) {
	shipments, err := s.shipments.ListShipmentsByOrder(ctx, tx, orderID)
	if err != nil {
		return nil, fmt.Errorf("list shipments by order: %w", err)
	}
	return shipments, nil
}
