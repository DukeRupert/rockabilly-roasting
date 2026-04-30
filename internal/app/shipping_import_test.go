package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/pirateship"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// fakeEnqueuer captures EnqueueOrderShipped calls so the test can assert
// the email job rides on the same tx as the import write. Other methods
// are stubbed to satisfy the JobEnqueuer interface — none are exercised
// by the import path.
type fakeEnqueuer struct {
	shippedCalls []shippedCall
}

type shippedCall struct {
	OrderID, CustomerID, ShipmentID uuid.UUID
}

func (f *fakeEnqueuer) EnqueueRenewalReceipt(_ context.Context, _ pgx.Tx, _, _ uuid.UUID) error {
	return nil
}
func (f *fakeEnqueuer) EnqueuePastDueNotice(_ context.Context, _ pgx.Tx, _, _ uuid.UUID) error {
	return nil
}
func (f *fakeEnqueuer) EnqueueOrderConfirm(_ context.Context, _ pgx.Tx, _, _ uuid.UUID) error {
	return nil
}
func (f *fakeEnqueuer) EnqueueOrderShipped(_ context.Context, _ pgx.Tx, orderID, customerID, shipmentID uuid.UUID) error {
	f.shippedCalls = append(f.shippedCalls, shippedCall{orderID, customerID, shipmentID})
	return nil
}
func (f *fakeEnqueuer) EnqueueOrderReadyForPickup(_ context.Context, _ pgx.Tx, _, _ uuid.UUID) error {
	return nil
}
func (f *fakeEnqueuer) EnqueueOrderOutForDelivery(_ context.Context, _ pgx.Tx, _, _ uuid.UUID) error {
	return nil
}

func newImportService(enq *fakeEnqueuer) *app.ShippingImportService {
	// pool is unused by the per-row in-tx helper. Tests drive that helper
	// directly via the test-only export so the writes stay inside the test's
	// rollback-on-cleanup tx.
	return app.NewShippingImportService(
		store.NewOrderStore(nil),
		store.NewShippingStore(),
		audit.NewAuditWriter(),
		metrics.NewRegistry(),
		enq,
		nil,
	)
}

func TestShippingImport_RecordsTrackingAndEnqueuesEmail(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()

	staffID := testutil.CreateStaff(t, tx)
	actor := testutil.TestActorFromStaff(staffID)
	custID, shipID, billID := orderFixtures(t, tx)
	order := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))

	enq := &fakeEnqueuer{}
	svc := newImportService(enq)

	shipDate := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	row := pirateship.TrackingRow{
		LineNumber:       1,
		OrderID:          order.Number,
		TrackingNumber:   "9400111202555842761523",
		CarrierName:      "USPS",
		ServiceName:      "Ground Advantage",
		PostageCostCents: 485,
		ShipDate:         &shipDate,
	}

	res := app.ImportResult{LineNumber: row.LineNumber, OrderNumber: row.OrderID}
	err := svc.RecordPirateShipTrackingInTxForTest(ctx, tx, row, actor, &res)
	require.NoError(t, err)

	require.NotNil(t, res.ShipmentID)

	// Order flipped to shipped.
	orderStore := store.NewOrderStore(nil)
	updated, err := orderStore.GetOrderByIDAsStaff(ctx, tx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.FulfillmentStatusShipped, updated.FulfillmentStatus)

	// Shipment row is the one we inserted.
	shippingStore := store.NewShippingStore()
	shipment, err := shippingStore.GetShipmentByIDAsStaff(ctx, tx, *res.ShipmentID)
	require.NoError(t, err)
	assert.Equal(t, pirateship.ProviderCSV, shipment.Provider)
	assert.Equal(t, "9400111202555842761523", shipment.TrackingNumber)
	assert.Equal(t, "USPS", shipment.CarrierName)
	assert.Equal(t, 485, shipment.LabelCostCents)
	assert.Equal(t, "USD", shipment.LabelCurrency)
	require.NotNil(t, shipment.ShippedAt)
	assert.True(t, shipment.ShippedAt.Equal(shipDate))
	// Imports do not carry box dimensions or a label artifact.
	assert.Nil(t, shipment.LabelURL)
	assert.Nil(t, shipment.LengthIn)
	assert.Nil(t, shipment.WidthIn)
	assert.Nil(t, shipment.HeightIn)

	// Audit entry on the shipment.
	entry := testutil.LastAuditEntry(t, tx, "shipment", shipment.ID)
	assert.Equal(t, audit.AuditShipmentImported, entry.Action)

	// Customer notification job enqueued exactly once.
	require.Len(t, enq.shippedCalls, 1)
	assert.Equal(t, order.ID, enq.shippedCalls[0].OrderID)
	assert.Equal(t, custID, enq.shippedCalls[0].CustomerID)
	assert.Equal(t, shipment.ID, enq.shippedCalls[0].ShipmentID)
}

func TestShippingImport_OrderNotFound(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()

	enq := &fakeEnqueuer{}
	svc := newImportService(enq)
	actor := testutil.TestActor()

	row := pirateship.TrackingRow{
		LineNumber:     7,
		OrderID:        "NOPE-9999",
		TrackingNumber: "1Z999AA1",
	}

	res := app.ImportResult{LineNumber: row.LineNumber, OrderNumber: row.OrderID}
	err := svc.RecordPirateShipTrackingInTxForTest(ctx, tx, row, actor, &res)

	require.ErrorIs(t, err, app.ErrImportSkipForTest)
	assert.Equal(t, app.ImportOutcomeSkipped, res.Outcome)
	assert.Equal(t, "order not found", res.Reason)
	assert.Nil(t, res.ShipmentID)
	assert.Empty(t, enq.shippedCalls)
}

func TestShippingImport_AlreadyShipped(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()

	custID, shipID, billID := orderFixtures(t, tx)
	order := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithFulfillmentStatus(domain.FulfillmentStatusShipped))

	enq := &fakeEnqueuer{}
	svc := newImportService(enq)
	actor := testutil.TestActor()

	row := pirateship.TrackingRow{
		LineNumber:     1,
		OrderID:        order.Number,
		TrackingNumber: "9400111202555842761523",
		CarrierName:    "USPS",
	}

	res := app.ImportResult{LineNumber: row.LineNumber, OrderNumber: row.OrderID}
	err := svc.RecordPirateShipTrackingInTxForTest(ctx, tx, row, actor, &res)

	require.ErrorIs(t, err, app.ErrImportSkipForTest)
	assert.Equal(t, app.ImportOutcomeSkipped, res.Outcome)
	assert.Equal(t, "already shipped", res.Reason)
	assert.Nil(t, res.ShipmentID)
	assert.Empty(t, enq.shippedCalls)

	// Crucially: order's fulfillment status is still shipped (untouched).
	orderStore := store.NewOrderStore(nil)
	updated, err := orderStore.GetOrderByIDAsStaff(ctx, tx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.FulfillmentStatusShipped, updated.FulfillmentStatus)
}

func TestShippingImport_PreflightSkips(t *testing.T) {
	tests := []struct {
		name string
		row  pirateship.TrackingRow
		want string
	}{
		{"blank order id", pirateship.TrackingRow{TrackingNumber: "X"}, "blank order id"},
		{"no tracking number", pirateship.TrackingRow{OrderID: "RR-1"}, "no tracking number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, skip := app.PreflightSkipReasonForTest(tt.row)
			assert.True(t, skip)
			assert.Equal(t, tt.want, reason)
		})
	}
}

func TestShippingImport_CanImportTrackingFor(t *testing.T) {
	allowed := []domain.FulfillmentStatus{
		domain.FulfillmentStatusUnfulfilled,
		domain.FulfillmentStatusPartiallyFulfilled,
		domain.FulfillmentStatusFulfilled,
	}
	denied := []domain.FulfillmentStatus{
		domain.FulfillmentStatusShipped,
		domain.FulfillmentStatusPartiallyShipped,
		domain.FulfillmentStatusDelivered,
		domain.FulfillmentStatusReturned,
	}
	for _, s := range allowed {
		assert.True(t, app.CanImportTrackingForForTest(s), "%s should be importable", s)
	}
	for _, s := range denied {
		assert.False(t, app.CanImportTrackingForForTest(s), "%s should be blocked", s)
	}
}
