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
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// newDeliveryOrderService builds an OrderService with the shipments store wired,
// which ReconcileDelivery needs to read an order's shipment statuses.
func newDeliveryOrderService() *app.OrderService {
	return app.NewOrderService(store.NewOrderStore(nil), audit.NewAuditWriter(), metrics.NewRegistry()).
		WithShipments(store.NewShippingStore())
}

// createOrderShipment inserts a shipment for an order in the given status with an
// optional shipped_at, returning nothing — tests only care about the side effect.
func createOrderShipment(t *testing.T, tx pgx.Tx, orderID, staffID uuid.UUID, status domain.ShipmentStatus, shippedAt *time.Time) {
	t.Helper()
	_, err := store.NewShippingStore().CreateShipment(context.Background(), tx, store.CreateShipmentParams{
		OrderID:        orderID,
		Status:         status,
		Provider:       "shippo",
		TrackingNumber: "T-" + uuid.NewString(),
		CarrierName:    "USPS",
		ServiceName:    "Ground Advantage",
		LabelCostCents: 758,
		LabelCurrency:  "USD",
		WeightOz:       12.5,
		CreatedBy:      staffID,
		ShippedAt:      shippedAt,
	})
	require.NoError(t, err)
}

func TestOrderService_ReconcileDelivery(t *testing.T) {
	ctx := context.Background()
	svc := newDeliveryOrderService()
	actor := testutil.TestActor()

	t.Run("all shipments delivered -> order delivered", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		staffID := testutil.CreateStaff(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusComplete),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusShipped))
		createOrderShipment(t, tx, order.ID, staffID, domain.ShipmentStatusDelivered, nil)

		got, err := svc.ReconcileDelivery(ctx, tx, order.ID, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.FulfillmentStatusDelivered, got.FulfillmentStatus)
	})

	t.Run("some shipments delivered -> partially_delivered", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		staffID := testutil.CreateStaff(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusComplete),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusShipped))
		createOrderShipment(t, tx, order.ID, staffID, domain.ShipmentStatusDelivered, nil)
		createOrderShipment(t, tx, order.ID, staffID, domain.ShipmentStatusInTransit, nil)

		got, err := svc.ReconcileDelivery(ctx, tx, order.ID, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.FulfillmentStatusPartiallyDelivered, got.FulfillmentStatus)
	})

	t.Run("order not in shipped state is left untouched", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		staffID := testutil.CreateStaff(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusFulfilled))
		createOrderShipment(t, tx, order.ID, staffID, domain.ShipmentStatusDelivered, nil)

		got, err := svc.ReconcileDelivery(ctx, tx, order.ID, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.FulfillmentStatusFulfilled, got.FulfillmentStatus)
	})

	t.Run("no shipments is a safe no-op", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusShipped))

		got, err := svc.ReconcileDelivery(ctx, tx, order.ID, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.FulfillmentStatusShipped, got.FulfillmentStatus)
	})
}

func TestOrderService_MarkOrderDelivered(t *testing.T) {
	ctx := context.Background()
	svc := newOrderService()
	actor := testutil.TestActor()

	t.Run("shipped order becomes delivered", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusComplete),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusShipped))

		got, err := svc.MarkOrderDelivered(ctx, tx, order.ID, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.FulfillmentStatusDelivered, got.FulfillmentStatus)
		// Order status (already complete) is a fulfillment-independent fact and
		// must not change.
		assert.Equal(t, domain.OrderStatusComplete, got.Status)
	})

	t.Run("non-shipped order returns ErrInvalidOrderStatus", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusFulfilled))

		_, err := svc.MarkOrderDelivered(ctx, tx, order.ID, actor)
		assert.ErrorIs(t, err, app.ErrInvalidOrderStatus)
	})

	t.Run("missing order returns ErrOrderNotFound", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, err := svc.MarkOrderDelivered(ctx, tx, uuid.New(), actor)
		assert.ErrorIs(t, err, app.ErrOrderNotFound)
	})
}

// TestOrderService_ListOrderIDsToAutoDeliver exercises the sweep candidate query
// across the two cohorts it must handle: legacy orders with no shipment rows
// (fall back to updated_at) and live orders with a precise shipments.shipped_at.
func TestOrderService_ListOrderIDsToAutoDeliver(t *testing.T) {
	ctx := context.Background()
	svc := newOrderService()
	cutoff := time.Now().Add(-7 * 24 * time.Hour)

	contains := func(ids []uuid.UUID, id uuid.UUID) bool {
		for _, x := range ids {
			if x == id {
				return true
			}
		}
		return false
	}

	t.Run("legacy shipped order (no shipment, stale updated_at) is swept", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusShipped))
		// Imported orders carry an old updated_at; the fixture stamps "now", so
		// age it past the cutoff explicitly.
		_, err := tx.Exec(ctx, `UPDATE orders SET updated_at = now() - interval '30 days' WHERE id = $1`, order.ID)
		require.NoError(t, err)

		ids, err := svc.ListOrderIDsToAutoDeliver(ctx, tx, cutoff, 100)
		require.NoError(t, err)
		assert.True(t, contains(ids, order.ID))
	})

	t.Run("recently shipped order (fresh shipped_at) is not swept", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		staffID := testutil.CreateStaff(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusShipped))
		// Old updated_at, but a recent ship — the precise shipment time must win
		// so a package still in its delivery window is never marked delivered.
		_, err := tx.Exec(ctx, `UPDATE orders SET updated_at = now() - interval '30 days' WHERE id = $1`, order.ID)
		require.NoError(t, err)
		shippedRecently := time.Now().Add(-2 * 24 * time.Hour)
		createOrderShipment(t, tx, order.ID, staffID, domain.ShipmentStatusInTransit, &shippedRecently)

		ids, err := svc.ListOrderIDsToAutoDeliver(ctx, tx, cutoff, 100)
		require.NoError(t, err)
		assert.False(t, contains(ids, order.ID))
	})

	t.Run("long-shipped order (stale shipped_at) is swept", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		staffID := testutil.CreateStaff(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusShipped))
		shippedLongAgo := time.Now().Add(-20 * 24 * time.Hour)
		createOrderShipment(t, tx, order.ID, staffID, domain.ShipmentStatusInTransit, &shippedLongAgo)

		ids, err := svc.ListOrderIDsToAutoDeliver(ctx, tx, cutoff, 100)
		require.NoError(t, err)
		assert.True(t, contains(ids, order.ID))
	})

	t.Run("cancelled and already-delivered orders are excluded", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)

		cancelled := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusCancelled),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusShipped))
		delivered := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusDelivered))
		for _, id := range []uuid.UUID{cancelled.ID, delivered.ID} {
			_, err := tx.Exec(ctx, `UPDATE orders SET updated_at = now() - interval '30 days' WHERE id = $1`, id)
			require.NoError(t, err)
		}

		ids, err := svc.ListOrderIDsToAutoDeliver(ctx, tx, cutoff, 100)
		require.NoError(t, err)
		assert.False(t, contains(ids, cancelled.ID))
		assert.False(t, contains(ids, delivered.ID))
	})
}
