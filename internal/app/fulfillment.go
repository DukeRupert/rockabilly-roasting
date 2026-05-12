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
	boxPresets    *store.BoxPresetStore
	labelProvider shipping.LabelProvider
	audit         *audit.AuditWriter
	metrics       *metrics.Registry
}

// NewFulfillmentService creates a new FulfillmentService.
func NewFulfillmentService(
	fulfillment *store.FulfillmentStore,
	shipments *store.ShippingStore,
	orders *store.OrderStore,
	boxPresets *store.BoxPresetStore,
	labelProvider shipping.LabelProvider,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *FulfillmentService {
	return &FulfillmentService{
		fulfillment:   fulfillment,
		shipments:     shipments,
		orders:        orders,
		boxPresets:    boxPresets,
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

// --- Box presets ---

// ListBoxPresets returns all box presets in display order.
func (s *FulfillmentService) ListBoxPresets(ctx context.Context, tx pgx.Tx) ([]domain.BoxPreset, error) {
	return s.boxPresets.List(ctx, tx)
}

// ListBoxPresetsForSelection returns presets sorted by capacity ascending —
// the order needed for SelectBoxForWeight.
func (s *FulfillmentService) ListBoxPresetsForSelection(ctx context.Context, tx pgx.Tx) ([]domain.BoxPreset, error) {
	return s.boxPresets.ListByMaxWeightAsc(ctx, tx)
}

// GetBoxPreset returns a box preset by ID.
func (s *FulfillmentService) GetBoxPreset(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.BoxPreset, error) {
	return s.boxPresets.GetByID(ctx, tx, id)
}

// BoxPresetInput is the validated form data for create/update.
type BoxPresetInput struct {
	Name        string
	LengthIn    float64
	WidthIn     float64
	HeightIn    float64
	MaxWeightOz float64
	SortOrder   int
}

func (in BoxPresetInput) validate() error {
	if in.Name == "" {
		return ErrBoxPresetNameRequired
	}
	if in.LengthIn <= 0 || in.WidthIn <= 0 || in.HeightIn <= 0 {
		return ErrBoxPresetDimensionsInvalid
	}
	if in.MaxWeightOz <= 0 {
		return ErrBoxPresetMaxWeightInvalid
	}
	return nil
}

// CreateBoxPreset inserts a new preset and records an audit entry.
func (s *FulfillmentService) CreateBoxPreset(ctx context.Context, tx pgx.Tx, in BoxPresetInput, actor Actor) (*domain.BoxPreset, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	preset, err := s.boxPresets.Create(ctx, tx, store.CreateBoxPresetParams{
		Name:        in.Name,
		LengthIn:    in.LengthIn,
		WidthIn:     in.WidthIn,
		HeightIn:    in.HeightIn,
		MaxWeightOz: in.MaxWeightOz,
		SortOrder:   in.SortOrder,
	})
	if err != nil {
		return nil, err
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditBoxPresetCreated,
		ResourceType: "box_preset",
		ResourceID:   preset.ID,
		After:        preset,
	}); err != nil {
		return nil, fmt.Errorf("audit box preset created: %w", err)
	}
	return preset, nil
}

// UpdateBoxPreset persists changes to a preset and records an audit entry.
func (s *FulfillmentService) UpdateBoxPreset(ctx context.Context, tx pgx.Tx, id uuid.UUID, in BoxPresetInput, actor Actor) (*domain.BoxPreset, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	after, err := s.boxPresets.Update(ctx, tx, store.UpdateBoxPresetParams{
		ID:          id,
		Name:        in.Name,
		LengthIn:    in.LengthIn,
		WidthIn:     in.WidthIn,
		HeightIn:    in.HeightIn,
		MaxWeightOz: in.MaxWeightOz,
		SortOrder:   in.SortOrder,
	})
	if err != nil {
		return nil, err
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditBoxPresetUpdated,
		ResourceType: "box_preset",
		ResourceID:   id,
		After:        after,
	}); err != nil {
		return nil, fmt.Errorf("audit box preset updated: %w", err)
	}
	return after, nil
}

// DeleteBoxPreset removes a preset and records an audit entry.
func (s *FulfillmentService) DeleteBoxPreset(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) error {
	before, err := s.boxPresets.GetByID(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := s.boxPresets.Delete(ctx, tx, id); err != nil {
		return err
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditBoxPresetDeleted,
		ResourceType: "box_preset",
		ResourceID:   id,
		After:        before,
	}); err != nil {
		return fmt.Errorf("audit box preset deleted: %w", err)
	}
	return nil
}
