package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// ShippingStore provides database access for shipping config and shipments.
type ShippingStore struct{}

// NewShippingStore creates a new ShippingStore.
func NewShippingStore() *ShippingStore {
	return &ShippingStore{}
}

// --- Shipping Config ---

// GetConfig returns the shipping configuration.
func (s *ShippingStore) GetConfig(ctx context.Context, tx pgx.Tx) (*domain.ShippingConfig, error) {
	row, err := sqlcgen.New(tx).GetShippingConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("get shipping config: %w", err)
	}
	return &domain.ShippingConfig{
		FlatRateCents:         int(row.FlatRateCents),
		FreeShippingThreshold: int32PtrToIntPtr(row.FreeShippingThreshold),
		Currency:              row.Currency,
	}, nil
}

// UpdateConfig updates the shipping configuration.
func (s *ShippingStore) UpdateConfig(ctx context.Context, tx pgx.Tx, flatRateCents int, freeShippingThreshold *int, currency string) error {
	err := sqlcgen.New(tx).UpdateShippingConfig(ctx, sqlcgen.UpdateShippingConfigParams{
		FlatRateCents:         int32(flatRateCents),
		FreeShippingThreshold: intPtrToInt32Ptr(freeShippingThreshold),
		Currency:              currency,
	})
	if err != nil {
		return fmt.Errorf("update shipping config: %w", err)
	}
	return nil
}

// --- Shipments ---

// CreateShipmentParams holds the fields needed to create a shipment.
type CreateShipmentParams struct {
	OrderID        uuid.UUID
	Status         domain.ShipmentStatus
	Provider       string
	TrackingNumber string
	LabelURL       string
	CarrierName    string
	ServiceName    string
	LabelCostCents int
	LabelCurrency  string
	WeightOz       float64
	LengthIn       float64
	WidthIn        float64
	HeightIn       float64
	CreatedBy      uuid.UUID
}

// CreateShipment inserts a new shipment and returns it.
func (s *ShippingStore) CreateShipment(ctx context.Context, tx pgx.Tx, p CreateShipmentParams) (*domain.Shipment, error) {
	row, err := sqlcgen.New(tx).CreateShipment(ctx, sqlcgen.CreateShipmentParams{
		ID:             uuid.New(),
		OrderID:        p.OrderID,
		Status:         string(p.Status),
		Provider:       p.Provider,
		TrackingNumber: p.TrackingNumber,
		LabelUrl:       p.LabelURL,
		CarrierName:    p.CarrierName,
		ServiceName:    p.ServiceName,
		LabelCostCents: int32(p.LabelCostCents),
		LabelCurrency:  p.LabelCurrency,
		WeightOz:       float64ToNumeric(p.WeightOz),
		LengthIn:       float64ToNumeric(p.LengthIn),
		WidthIn:        float64ToNumeric(p.WidthIn),
		HeightIn:       float64ToNumeric(p.HeightIn),
		CreatedBy:      p.CreatedBy,
	})
	if err != nil {
		return nil, fmt.Errorf("insert shipment: %w", err)
	}
	return shipmentFromRow(row), nil
}

// GetShipmentByIDAsStaff returns a shipment by ID.
func (s *ShippingStore) GetShipmentByIDAsStaff(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Shipment, error) {
	row, err := sqlcgen.New(tx).GetShipmentByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get shipment %s: %w", id, err)
	}
	return shipmentFromRow(row), nil
}

// ListShipmentsByOrder returns all shipments for an order.
func (s *ShippingStore) ListShipmentsByOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]domain.Shipment, error) {
	rows, err := sqlcgen.New(tx).ListShipmentsByOrder(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("list shipments: %w", err)
	}
	shipments := make([]domain.Shipment, len(rows))
	for i, r := range rows {
		shipments[i] = *shipmentFromRow(r)
	}
	return shipments, nil
}

// UpdateShipmentStatus updates a shipment's status and returns it.
func (s *ShippingStore) UpdateShipmentStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.ShipmentStatus) (*domain.Shipment, error) {
	row, err := sqlcgen.New(tx).UpdateShipmentStatus(ctx, sqlcgen.UpdateShipmentStatusParams{
		ID:     id,
		Status: string(status),
	})
	if err != nil {
		return nil, fmt.Errorf("update shipment status: %w", err)
	}
	return shipmentFromRow(row), nil
}

// UpdateShipmentTracking updates tracking info and returns the shipment.
func (s *ShippingStore) UpdateShipmentTracking(ctx context.Context, tx pgx.Tx, id uuid.UUID, trackingNumber, labelURL string) (*domain.Shipment, error) {
	row, err := sqlcgen.New(tx).UpdateShipmentTracking(ctx, sqlcgen.UpdateShipmentTrackingParams{
		ID:             id,
		TrackingNumber: trackingNumber,
		LabelUrl:       labelURL,
	})
	if err != nil {
		return nil, fmt.Errorf("update shipment tracking: %w", err)
	}
	return shipmentFromRow(row), nil
}

// UpdateShipmentDelivered marks a shipment as delivered.
func (s *ShippingStore) UpdateShipmentDelivered(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).UpdateShipmentDelivered(ctx, id); err != nil {
		return fmt.Errorf("mark shipment delivered: %w", err)
	}
	return nil
}

// UpdateShipmentLabel sets the R2 key and format for a shipment's label.
func (s *ShippingStore) UpdateShipmentLabel(ctx context.Context, tx pgx.Tx, id uuid.UUID, r2Key, format string) error {
	err := sqlcgen.New(tx).UpdateShipmentLabel(ctx, sqlcgen.UpdateShipmentLabelParams{
		ID:          id,
		LabelR2Key:  &r2Key,
		LabelFormat: &format,
	})
	if err != nil {
		return fmt.Errorf("update shipment label: %w", err)
	}
	return nil
}

// GetShipmentLabelKey returns the R2 object key for a shipment's label.
func (s *ShippingStore) GetShipmentLabelKey(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*string, error) {
	key, err := sqlcgen.New(tx).GetShipmentLabelKey(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get shipment label key: %w", err)
	}
	return key, nil
}

// --- Row converters ---

func shipmentFromRow(r sqlcgen.Shipment) *domain.Shipment {
	return &domain.Shipment{
		ID:             r.ID,
		OrderID:        r.OrderID,
		Status:         domain.ShipmentStatus(r.Status),
		Provider:       r.Provider,
		TrackingNumber: r.TrackingNumber,
		LabelURL:       r.LabelUrl,
		CarrierName:    r.CarrierName,
		ServiceName:    r.ServiceName,
		LabelCostCents: int(r.LabelCostCents),
		LabelCurrency:  r.LabelCurrency,
		WeightOz:       numericToFloat64(r.WeightOz),
		LengthIn:       numericToFloat64(r.LengthIn),
		WidthIn:        numericToFloat64(r.WidthIn),
		HeightIn:       numericToFloat64(r.HeightIn),
		CreatedBy:      r.CreatedBy,
		CreatedAt:      r.CreatedAt,
		LabelCreatedAt: timestampFromPG(r.LabelCreatedAt),
		ShippedAt:      timestampFromPG(r.ShippedAt),
		DeliveredAt:    timestampFromPG(r.DeliveredAt),
		LabelR2Key:     r.LabelR2Key,
		LabelFormat:    r.LabelFormat,
	}
}
