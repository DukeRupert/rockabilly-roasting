package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/pirateship"
	"github.com/dukerupert/hiri/internal/store"
)

// ShippingImportService records tracking rows from a Pirate Ship export back
// against the orders they shipped. One transaction per row so a malformed
// row in the middle of the file doesn't roll back the rest.
type ShippingImportService struct {
	orders   *store.OrderStore
	shipping *store.ShippingStore
	audit    *audit.AuditWriter
	metrics  *metrics.Registry
	enqueuer JobEnqueuer
	pool     *pgxpool.Pool
}

// NewShippingImportService wires the import service.
func NewShippingImportService(
	orders *store.OrderStore,
	shipping *store.ShippingStore,
	auditWriter *audit.AuditWriter,
	metricsReg *metrics.Registry,
	enqueuer JobEnqueuer,
	pool *pgxpool.Pool,
) *ShippingImportService {
	return &ShippingImportService{
		orders:   orders,
		shipping: shipping,
		audit:    auditWriter,
		metrics:  metricsReg,
		enqueuer: enqueuer,
		pool:     pool,
	}
}

// ImportOutcome describes what happened to a single tracking row.
type ImportOutcome string

const (
	ImportOutcomeRecorded ImportOutcome = "recorded" // shipment written, fulfillment flipped, email enqueued
	ImportOutcomeSkipped  ImportOutcome = "skipped"  // already-shipped or not-found — no writes
	ImportOutcomeError    ImportOutcome = "error"    // unexpected DB / encode failure
)

// ImportResult is the outcome for one row of the uploaded CSV.
type ImportResult struct {
	LineNumber  int
	OrderNumber string
	Outcome     ImportOutcome
	Reason      string // populated for skipped + error outcomes
	ShipmentID  *uuid.UUID
}

// RecordPirateShipTracking applies one Pirate Ship tracking row. Each call
// runs in its own transaction so partial files commit cleanly. The
// idempotency guard is the order's fulfillment status: anything past
// `unfulfilled`/`partially_fulfilled` is treated as already-shipped and
// skipped, which makes re-uploading the same file a no-op.
func (s *ShippingImportService) RecordPirateShipTracking(
	ctx context.Context,
	row pirateship.TrackingRow,
	actor Actor,
) ImportResult {
	res := ImportResult{
		LineNumber:  row.LineNumber,
		OrderNumber: row.OrderID,
	}

	if reason, skip := preflightSkipReason(row); skip {
		res.Outcome = ImportOutcomeSkipped
		res.Reason = reason
		s.metrics.PirateShipImports.WithLabelValues(string(res.Outcome)).Inc()
		return res
	}

	err := store.Tx(ctx, s.pool, func(tx pgx.Tx) error {
		return s.recordPirateShipTrackingInTx(ctx, tx, row, actor, &res)
	})
	if err != nil {
		if errors.Is(err, errSkip) {
			s.metrics.PirateShipImports.WithLabelValues(string(res.Outcome)).Inc()
			return res
		}
		res.Outcome = ImportOutcomeError
		res.Reason = err.Error()
		s.metrics.PirateShipImports.WithLabelValues(string(res.Outcome)).Inc()
		return res
	}

	res.Outcome = ImportOutcomeRecorded
	s.metrics.PirateShipImports.WithLabelValues(string(res.Outcome)).Inc()
	return res
}

// preflightSkipReason returns the reason to skip a row before opening a tx,
// or ("", false) if the row should proceed to a write attempt. Pulled out
// so tests can exercise the in-tx logic directly without the row-validation
// noise.
func preflightSkipReason(row pirateship.TrackingRow) (string, bool) {
	if row.OrderID == "" {
		return "blank order id", true
	}
	if row.TrackingNumber == "" {
		return "no tracking number", true
	}
	return "", false
}

// recordPirateShipTrackingInTx is the per-row write logic, factored out of
// RecordPirateShipTracking so tests can drive it inside a rollback-on-cleanup
// transaction. Mutates res in place (sets Outcome/Reason on a skip,
// ShipmentID on success). Returns errSkip on a recoverable skip or a real
// error on a write failure; the caller maps both to the response.
func (s *ShippingImportService) recordPirateShipTrackingInTx(
	ctx context.Context,
	tx pgx.Tx,
	row pirateship.TrackingRow,
	actor Actor,
	res *ImportResult,
) error {
	order, err := s.orders.GetOrderByNumberAsStaff(ctx, tx, row.OrderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			res.Outcome = ImportOutcomeSkipped
			res.Reason = "order not found"
			return errSkip
		}
		return fmt.Errorf("get order %s: %w", row.OrderID, err)
	}

	if !canImportTrackingFor(order.FulfillmentStatus) {
		res.Outcome = ImportOutcomeSkipped
		res.Reason = "already shipped"
		return errSkip
	}

	var createdBy uuid.UUID
	if actor.ID != nil {
		createdBy = *actor.ID
	}

	shipment, err := s.shipping.CreateShipment(ctx, tx, store.CreateShipmentParams{
		OrderID:        order.ID,
		Status:         domain.ShipmentStatusInTransit,
		Provider:       pirateship.ProviderCSV,
		TrackingNumber: row.TrackingNumber,
		CarrierName:    row.CarrierName,
		ServiceName:    row.ServiceName,
		LabelCostCents: row.PostageCostCents,
		LabelCurrency:  "USD",
		WeightOz:       0, // Pirate Ship's tracking export doesn't echo the weight back
		ShippedAt:      row.ShipDate,
		CreatedBy:      createdBy,
	})
	if err != nil {
		return fmt.Errorf("insert shipment: %w", err)
	}
	res.ShipmentID = &shipment.ID

	if _, err := s.orders.UpdateOrderFulfillmentStatus(ctx, tx, order.ID, domain.FulfillmentStatusShipped); err != nil {
		return fmt.Errorf("flip fulfillment status: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditShipmentImported,
		ResourceType: "shipment",
		ResourceID:   shipment.ID,
		After:        shipment,
		Metadata: map[string]any{
			"order_number":    order.Number,
			"tracking_number": row.TrackingNumber,
			"carrier":         row.CarrierName,
			"source":          pirateship.ProviderCSV,
		},
	}); err != nil {
		return fmt.Errorf("audit shipment imported: %w", err)
	}

	// Only enqueue the customer notification when an order has a customer
	// attached. Guest orders (no customer_id) skip the email — there is no
	// account to mail. This is rare on the storefront but happens for
	// staff-created drafts.
	if order.CustomerID != nil {
		if err := s.enqueuer.EnqueueOrderShipped(ctx, tx, order.ID, *order.CustomerID, shipment.ID); err != nil {
			return fmt.Errorf("enqueue order shipped email: %w", err)
		}
	}
	return nil
}

// errSkip is a sentinel used inside the per-row transaction callback to short
// out without leaking an error. The outer code converts it to a skipped
// outcome that's already been populated on the result.
var errSkip = errors.New("import skip")

// canImportTrackingFor reports whether an order's fulfillment status still
// permits a tracking import. Anything past unfulfilled / partially_fulfilled
// is treated as already-shipped — re-uploading the same Pirate Ship file is
// a no-op.
func canImportTrackingFor(status domain.FulfillmentStatus) bool {
	switch status {
	case domain.FulfillmentStatusUnfulfilled,
		domain.FulfillmentStatusPartiallyFulfilled,
		domain.FulfillmentStatusFulfilled:
		return true
	default:
		return false
	}
}
