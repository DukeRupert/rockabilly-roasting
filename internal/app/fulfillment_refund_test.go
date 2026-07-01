package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/shipping"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// stubLabelProvider records refund calls and returns canned results so the
// external-call service methods can be exercised without a live carrier.
type stubLabelProvider struct {
	requestResult *shipping.RefundResult
	requestErr    error
	getResult     *shipping.RefundResult
	getErr        error

	sawRequestTxnID string
	sawGetRefundID  string
}

func (s *stubLabelProvider) GetRates(context.Context, shipping.LabelRequest) ([]shipping.Rate, error) {
	return nil, errors.New("not implemented")
}
func (s *stubLabelProvider) BuyRate(context.Context, shipping.Rate) (*shipping.LabelResult, error) {
	return nil, errors.New("not implemented")
}
func (s *stubLabelProvider) CreateLabel(context.Context, shipping.LabelRequest) (*shipping.LabelResult, error) {
	return nil, errors.New("not implemented")
}
func (s *stubLabelProvider) SupportedServices(context.Context) ([]string, error) {
	return nil, nil
}
func (s *stubLabelProvider) RequestRefund(_ context.Context, transactionID string) (*shipping.RefundResult, error) {
	s.sawRequestTxnID = transactionID
	return s.requestResult, s.requestErr
}
func (s *stubLabelProvider) GetRefund(_ context.Context, refundID string) (*shipping.RefundResult, error) {
	s.sawGetRefundID = refundID
	return s.getResult, s.getErr
}

// newRefundFulfillmentService wires only the dependencies the refund + buy-guard
// paths touch: the shipments store, audit, and the label provider. The other
// stores are nil — exercised code never reaches them.
func newRefundFulfillmentService(provider shipping.LabelProvider) *app.FulfillmentService {
	return app.NewFulfillmentService(
		nil, // fulfillment
		store.NewShippingStore(),
		nil, // orders
		nil, // boxPresets
		nil, // customers
		nil, // catalog
		provider,
		audit.NewAuditWriter(),
		metrics.NewRegistry(),
	)
}

// createLabelShipment inserts a label_created shipment for a fresh order,
// optionally carrying a Shippo transaction id, and returns it plus the staff
// actor that owns it.
func createLabelShipment(t *testing.T, tx pgx.Tx, transactionID *string) (*domain.Shipment, app.Actor) {
	t.Helper()
	customer := testutil.CreateCustomer(t, tx)
	shipAddr := testutil.CreateAddress(t, tx, customer.ID)
	billAddr := testutil.CreateAddress(t, tx, customer.ID)
	order := testutil.CreateOrder(t, tx, customer.ID, shipAddr.ID, billAddr.ID)
	staffID := testutil.CreateStaff(t, tx)

	sh, err := store.NewShippingStore().CreateShipment(context.Background(), tx, store.CreateShipmentParams{
		OrderID:               order.ID,
		Status:                domain.ShipmentStatusLabelCreated,
		Provider:              "shippo",
		TrackingNumber:        "TRACK-" + uuid.NewString(),
		CarrierName:           "USPS",
		ServiceName:           "Ground Advantage",
		LabelCostCents:        758,
		LabelCurrency:         "USD",
		WeightOz:              12.5,
		CreatedBy:             staffID,
		ProviderTransactionID: transactionID,
	})
	require.NoError(t, err)

	actor := app.Actor{Type: domain.AuditActorTypeStaff, ID: &staffID, Name: "Test Staff"}
	return sh, actor
}

func strptr(s string) *string { return &s }

func TestFulfillmentService_Refund(t *testing.T) {
	ctx := context.Background()

	t.Run("LoadRefundableShipment rejects a shipment with no transaction id", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		svc := newRefundFulfillmentService(nil)
		sh, _ := createLabelShipment(t, tx, nil) // no txn id → not refundable

		_, err := svc.LoadRefundableShipment(ctx, tx, sh.ID)
		assert.True(t, errors.Is(err, app.ErrShipmentNotRefundable))
	})

	t.Run("LoadRefundableShipment rejects a delivered shipment", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		svc := newRefundFulfillmentService(nil)
		sh, _ := createLabelShipment(t, tx, strptr("tx_1"))
		_, err := store.NewShippingStore().UpdateShipmentStatus(ctx, tx, sh.ID, domain.ShipmentStatusDelivered)
		require.NoError(t, err)

		_, err = svc.LoadRefundableShipment(ctx, tx, sh.ID)
		assert.True(t, errors.Is(err, app.ErrShipmentNotRefundable))
	})

	t.Run("request then resolve refunded, idempotent on replay", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		svc := newRefundFulfillmentService(nil)
		sh, actor := createLabelShipment(t, tx, strptr("tx_1"))

		// LoadRefundableShipment succeeds.
		loaded, err := svc.LoadRefundableShipment(ctx, tx, sh.ID)
		require.NoError(t, err)
		assert.Equal(t, "tx_1", *loaded.ProviderTransactionID)

		// PersistRefundRequest flips to requested and records the refund id.
		updated, err := svc.PersistRefundRequest(ctx, tx, sh.ID, &shipping.RefundResult{RefundID: "refund_1", State: shipping.RefundPending}, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.RefundStatusRequested, updated.RefundStatus)
		require.NotNil(t, updated.RefundID)
		assert.Equal(t, "refund_1", *updated.RefundID)
		require.NotNil(t, updated.RefundRequestedAt)

		// A requested refund no longer blocks re-buy.
		assert.False(t, updated.BlocksRebuy())

		// Resolve to refunded: status flips, refunded_at set.
		err = svc.ResolveRefund(ctx, tx, sh.ID, domain.RefundStatusRefunded, actor, nil)
		require.NoError(t, err)
		reloaded, err := store.NewShippingStore().GetShipmentByIDAsStaff(ctx, tx, sh.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.RefundStatusRefunded, reloaded.RefundStatus)
		require.NotNil(t, reloaded.RefundedAt)

		// Replaying ResolveRefund is a no-op — the terminal status is unchanged.
		err = svc.ResolveRefund(ctx, tx, sh.ID, domain.RefundStatusFailed, actor, nil)
		require.NoError(t, err)
		reloaded2, err := store.NewShippingStore().GetShipmentByIDAsStaff(ctx, tx, sh.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.RefundStatusRefunded, reloaded2.RefundStatus, "already-resolved refund must not be overwritten")
	})

	t.Run("resolve failed re-blocks re-buy", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		svc := newRefundFulfillmentService(nil)
		sh, actor := createLabelShipment(t, tx, strptr("tx_1"))

		_, err := svc.PersistRefundRequest(ctx, tx, sh.ID, &shipping.RefundResult{RefundID: "refund_1"}, actor)
		require.NoError(t, err)
		err = svc.ResolveRefund(ctx, tx, sh.ID, domain.RefundStatusFailed, actor, nil)
		require.NoError(t, err)

		reloaded, err := store.NewShippingStore().GetShipmentByIDAsStaff(ctx, tx, sh.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.RefundStatusFailed, reloaded.RefundStatus)
		assert.True(t, reloaded.BlocksRebuy(), "a failed refund means the label is live and still blocks re-buy")
	})

	t.Run("external passthroughs hit the provider", func(t *testing.T) {
		provider := &stubLabelProvider{
			requestResult: &shipping.RefundResult{RefundID: "refund_9", State: shipping.RefundPending},
			getResult:     &shipping.RefundResult{RefundID: "refund_9", State: shipping.RefundSuccess},
		}
		svc := newRefundFulfillmentService(provider)

		req, err := svc.RequestRefundExternal(ctx, "tx_abc")
		require.NoError(t, err)
		assert.Equal(t, "tx_abc", provider.sawRequestTxnID)
		assert.Equal(t, "refund_9", req.RefundID)

		got, err := svc.GetRefundStatus(ctx, "refund_9")
		require.NoError(t, err)
		assert.Equal(t, "refund_9", provider.sawGetRefundID)
		assert.Equal(t, shipping.RefundSuccess, got.State)
	})
}

func TestFulfillmentService_PersistShipmentLabel_BuyGuard(t *testing.T) {
	ctx := context.Background()
	svc := newRefundFulfillmentService(nil)

	req := shipping.LabelRequest{WeightOz: 12.5, LengthIn: 10, WidthIn: 8, HeightIn: 4}
	result := shipping.LabelResult{
		TrackingNumber:        "TRK-NEW",
		LabelURL:              "https://shippo/new.pdf",
		CarrierName:           "USPS",
		ServiceName:           "Ground Advantage",
		RateCents:             758,
		Currency:              "USD",
		ProviderTransactionID: "tx_new",
	}

	t.Run("blocks a second label while one is live", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		sh, actor := createLabelShipment(t, tx, strptr("tx_1")) // live label

		_, err := svc.PersistShipmentLabel(ctx, tx, sh.OrderID, req, result, actor)
		assert.True(t, errors.Is(err, app.ErrOrderHasActiveLabel))
	})

	t.Run("allows a new label once the existing one is refund-requested", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		sh, actor := createLabelShipment(t, tx, strptr("tx_1"))

		_, err := svc.PersistRefundRequest(ctx, tx, sh.ID, &shipping.RefundResult{RefundID: "refund_1"}, actor)
		require.NoError(t, err)

		created, err := svc.PersistShipmentLabel(ctx, tx, sh.OrderID, req, result, actor)
		require.NoError(t, err)
		assert.Equal(t, "TRK-NEW", created.TrackingNumber)
		require.NotNil(t, created.ProviderTransactionID)
		assert.Equal(t, "tx_new", *created.ProviderTransactionID)
	})
}
