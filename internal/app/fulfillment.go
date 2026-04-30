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

// GetShipment returns a shipment by ID.
func (s *FulfillmentService) GetShipment(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Shipment, error) {
	shipment, err := s.shipments.GetShipmentByIDAsStaff(ctx, tx, id)
	if err != nil {
		return nil, fmt.Errorf("get shipment: %w", err)
	}
	return shipment, nil
}

// ListShipmentsByOrder returns all shipments for an order.
func (s *FulfillmentService) ListShipmentsByOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]domain.Shipment, error) {
	shipments, err := s.shipments.ListShipmentsByOrder(ctx, tx, orderID)
	if err != nil {
		return nil, fmt.Errorf("list shipments by order: %w", err)
	}
	return shipments, nil
}

// CreateShipmentLabel calls the external label provider to create a shipping
// label, then persists the shipment record in the database. The external API
// call happens BEFORE the transaction — if it fails, nothing is written.
func (s *FulfillmentService) CreateShipmentLabel(
	ctx context.Context,
	tx pgx.Tx,
	req shipping.LabelRequest,
	orderID uuid.UUID,
	actor Actor,
) (*domain.Shipment, error) {
	// External API call — outside transaction scope per architecture rules.
	result, err := s.labelProvider.CreateLabel(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("create label: %w", err)
	}

	labelURL := result.LabelURL
	lengthIn := req.LengthIn
	widthIn := req.WidthIn
	heightIn := req.HeightIn
	shipment, err := s.shipments.CreateShipment(ctx, tx, store.CreateShipmentParams{
		OrderID:        orderID,
		Status:         domain.ShipmentStatusLabelCreated,
		Provider:       "easypost",
		TrackingNumber: result.TrackingNumber,
		LabelURL:       &labelURL,
		CarrierName:    result.CarrierName,
		ServiceName:    result.ServiceName,
		LabelCostCents: result.RateCents,
		LabelCurrency:  result.Currency,
		WeightOz:       req.WeightOz,
		LengthIn:       &lengthIn,
		WidthIn:        &widthIn,
		HeightIn:       &heightIn,
		CreatedBy:      *actor.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("insert shipment: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditShipmentLabelCreated,
		ResourceType: "shipment",
		ResourceID:   shipment.ID,
		After:        shipment,
	}); err != nil {
		return nil, fmt.Errorf("audit shipment label created: %w", err)
	}

	return shipment, nil
}

// UpdateShipmentLabel stores the R2 key and format for a shipment's label.
// Called after the label has been fetched from EasyPost and uploaded to R2.
func (s *FulfillmentService) UpdateShipmentLabel(ctx context.Context, tx pgx.Tx, shipmentID uuid.UUID, r2Key, format string) error {
	if err := s.shipments.UpdateShipmentLabel(ctx, tx, shipmentID, r2Key, format); err != nil {
		return fmt.Errorf("update shipment label: %w", err)
	}
	return nil
}

// GetShipmentLabelKey returns the R2 object key for a shipment's label.
func (s *FulfillmentService) GetShipmentLabelKey(ctx context.Context, tx pgx.Tx, shipmentID uuid.UUID) (*string, error) {
	key, err := s.shipments.GetShipmentLabelKey(ctx, tx, shipmentID)
	if err != nil {
		return nil, fmt.Errorf("get shipment label key: %w", err)
	}
	return key, nil
}
