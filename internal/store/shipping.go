package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
		FlatRateCents:           int(row.FlatRateCents),
		FreeShippingThreshold:   int32PtrToIntPtr(row.FreeShippingThreshold),
		Currency:                row.Currency,
		LocalZipCodes:           row.LocalZipCodes,
		LocalDeliveryEnabled:    row.LocalDeliveryEnabled,
		LocalPickupEnabled:      row.LocalPickupEnabled,
		LocalPickupInstructions: row.LocalPickupInstructions,
		LocalDeliveryDays:       row.LocalDeliveryDays,
		OriginName:              row.OriginName,
		OriginStreet1:           row.OriginStreet1,
		OriginStreet2:           row.OriginStreet2,
		OriginCity:              row.OriginCity,
		OriginState:             row.OriginState,
		OriginZip:               row.OriginZip,
		OriginCountry:           row.OriginCountry,
		OriginEmail:             row.OriginEmail,
		OriginPhone:             row.OriginPhone,
		TareWeightOz:            numericToFloat64(row.TareWeightOz),
	}, nil
}

// UpdateConfig updates the shipping configuration.
func (s *ShippingStore) UpdateConfig(ctx context.Context, tx pgx.Tx, cfg domain.ShippingConfig) error {
	zips := cfg.LocalZipCodes
	if zips == nil {
		zips = []string{}
	}
	err := sqlcgen.New(tx).UpdateShippingConfig(ctx, sqlcgen.UpdateShippingConfigParams{
		FlatRateCents:           int32(cfg.FlatRateCents),
		FreeShippingThreshold:   intPtrToInt32Ptr(cfg.FreeShippingThreshold),
		Currency:                cfg.Currency,
		LocalZipCodes:           zips,
		OriginName:              cfg.OriginName,
		OriginStreet1:           cfg.OriginStreet1,
		OriginStreet2:           cfg.OriginStreet2,
		OriginCity:              cfg.OriginCity,
		OriginState:             cfg.OriginState,
		OriginZip:               cfg.OriginZip,
		OriginCountry:           cfg.OriginCountry,
		OriginEmail:             cfg.OriginEmail,
		OriginPhone:             cfg.OriginPhone,
		TareWeightOz:            float64ToNumeric(cfg.TareWeightOz),
		LocalDeliveryEnabled:    cfg.LocalDeliveryEnabled,
		LocalPickupEnabled:      cfg.LocalPickupEnabled,
		LocalPickupInstructions: cfg.LocalPickupInstructions,
		LocalDeliveryDays:       cfg.LocalDeliveryDays,
	})
	if err != nil {
		return fmt.Errorf("update shipping config: %w", err)
	}
	return nil
}

// --- Shipments ---

// CreateShipmentParams holds the fields needed to create a shipment.
// LabelURL and the box dimensions are pointer-typed because not every
// shipment source supplies them — only carrier label purchases do.
type CreateShipmentParams struct {
	OrderID        uuid.UUID
	Status         domain.ShipmentStatus
	Provider       string
	TrackingNumber string
	LabelURL       *string
	CarrierName    string
	ServiceName    string
	LabelCostCents int
	LabelCurrency  string
	WeightOz       float64
	LengthIn       *float64
	WidthIn        *float64
	HeightIn       *float64
	ShippedAt      *time.Time
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
		LengthIn:       float64PtrToNumeric(p.LengthIn),
		WidthIn:        float64PtrToNumeric(p.WidthIn),
		HeightIn:       float64PtrToNumeric(p.HeightIn),
		ShippedAt:      timestampToPG(p.ShippedAt),
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
		LabelUrl:       &labelURL,
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

// --- Label attempts ---

// GetLatestLabelAttempt returns the most recent non-successful BuyLabel job
// for an order, or nil if none is in flight or recently failed. A successful
// attempt is communicated via the shipment row, not here — so completed
// states are filtered out.
//
// This reads River's river_job table directly. The columns referenced (kind,
// args, state, attempt, max_attempts, errors) are stable across River
// versions; the table layout is part of River's documented contract.
func (s *ShippingStore) GetLatestLabelAttempt(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (*domain.LabelAttempt, error) {
	const query = `
		SELECT id, state, attempt, max_attempts, errors
		FROM river_job
		WHERE kind = 'buy_label'
		  AND (args->>'order_id') = $1
		  AND state <> 'completed'
		ORDER BY id DESC
		LIMIT 1`

	var (
		id            int64
		state         string
		attempt       int
		maxAttempts   int
		errorsJSON    []byte
	)
	err := tx.QueryRow(ctx, query, orderID.String()).Scan(&id, &state, &attempt, &maxAttempts, &errorsJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get latest label attempt: %w", err)
	}

	out := &domain.LabelAttempt{
		JobID:       id,
		Attempt:     attempt,
		MaxAttempts: maxAttempts,
		Status:      labelAttemptStatusFromRiverState(state),
	}
	if out.Status == domain.LabelAttemptStatusFailed && len(errorsJSON) > 0 {
		out.LastError = lastErrorMessage(errorsJSON)
	}
	return out, nil
}

// ListOrdersWithFailedLabelAttempts returns the subset of orderIDs whose
// latest BuyLabel job ended in a terminal failure state (cancelled or
// discarded). Used by the order list to flag rows that need operator
// attention. Orders with a queued/running attempt are NOT included — those
// resolve on their own.
func (s *ShippingStore) ListOrdersWithFailedLabelAttempts(ctx context.Context, tx pgx.Tx, orderIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := make(map[uuid.UUID]bool)
	if len(orderIDs) == 0 {
		return out, nil
	}

	idStrs := make([]string, len(orderIDs))
	for i, id := range orderIDs {
		idStrs[i] = id.String()
	}

	const query = `
		SELECT DISTINCT ON ((args->>'order_id'))
		       (args->>'order_id') AS order_id,
		       state
		FROM river_job
		WHERE kind = 'buy_label'
		  AND (args->>'order_id') = ANY($1::text[])
		ORDER BY (args->>'order_id'), id DESC`

	rows, err := tx.Query(ctx, query, idStrs)
	if err != nil {
		return nil, fmt.Errorf("list orders with failed label attempts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var orderIDStr, state string
		if err := rows.Scan(&orderIDStr, &state); err != nil {
			return nil, fmt.Errorf("scan label attempt row: %w", err)
		}
		if state != "cancelled" && state != "discarded" {
			continue
		}
		id, parseErr := uuid.Parse(orderIDStr)
		if parseErr != nil {
			continue
		}
		out[id] = true
	}
	return out, rows.Err()
}

// labelAttemptStatusFromRiverState collapses River's seven job states into
// the two we surface in the UI. "cancelled" + "discarded" are terminal
// failures; everything else (available, scheduled, running, retryable,
// pending) is in-flight. "completed" is filtered out at the query level.
func labelAttemptStatusFromRiverState(state string) domain.LabelAttemptStatus {
	switch state {
	case "cancelled", "discarded":
		return domain.LabelAttemptStatusFailed
	default:
		return domain.LabelAttemptStatusQueued
	}
}

// lastErrorMessage extracts the error string of the last AttemptError in
// River's errors JSONB column. Returns empty string if the JSON is malformed
// or empty — the UI handles that gracefully with a generic copy.
func lastErrorMessage(b []byte) string {
	type attemptError struct {
		Error string `json:"error"`
	}
	var attempts []attemptError
	if err := json.Unmarshal(b, &attempts); err != nil || len(attempts) == 0 {
		return ""
	}
	return attempts[len(attempts)-1].Error
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
		LengthIn:       numericToFloat64Ptr(r.LengthIn),
		WidthIn:        numericToFloat64Ptr(r.WidthIn),
		HeightIn:       numericToFloat64Ptr(r.HeightIn),
		CreatedBy:      r.CreatedBy,
		CreatedAt:      r.CreatedAt,
		LabelCreatedAt: timestampFromPG(r.LabelCreatedAt),
		ShippedAt:      timestampFromPG(r.ShippedAt),
		DeliveredAt:    timestampFromPG(r.DeliveredAt),
		LabelR2Key:     r.LabelR2Key,
		LabelFormat:    r.LabelFormat,
	}
}
