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

func TestOrderService_ConvertLocalOrderToShipped(t *testing.T) {
	ctx := context.Background()
	svc := newOrderService()
	actor := testutil.TestActor()

	t.Run("local_delivery order converts to shipped, total untouched", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodLocalDelivery),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))

		got, err := svc.ConvertLocalOrderToShipped(ctx, tx, order.ID, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.ShippingMethodShipped, got.ShippingMethod)
		// Comped: shipping is not charged, so the order total is unchanged.
		assert.Equal(t, order.Total, got.Total)
		assert.Equal(t, order.ShippingTotal, got.ShippingTotal)
	})

	t.Run("pickup order also converts (still in shop)", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodPickup),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusFulfilled))

		got, err := svc.ConvertLocalOrderToShipped(ctx, tx, order.ID, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.ShippingMethodShipped, got.ShippingMethod)
	})

	t.Run("already-shipped-channel order is rejected", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodShipped),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))

		_, err := svc.ConvertLocalOrderToShipped(ctx, tx, order.ID, actor)
		assert.ErrorIs(t, err, app.ErrInvalidOrderStatus)
	})

	t.Run("order that already left the shop is rejected", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodLocalDelivery),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusShipped))

		_, err := svc.ConvertLocalOrderToShipped(ctx, tx, order.ID, actor)
		assert.ErrorIs(t, err, app.ErrInvalidOrderStatus)
	})

	t.Run("cancelled order is rejected", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodLocalDelivery),
			testutil.WithOrderStatus(domain.OrderStatusCancelled),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))

		_, err := svc.ConvertLocalOrderToShipped(ctx, tx, order.ID, actor)
		assert.ErrorIs(t, err, app.ErrInvalidOrderStatus)
	})

	t.Run("missing order returns ErrOrderNotFound", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, err := svc.ConvertLocalOrderToShipped(ctx, tx, uuid.New(), actor)
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

// setLocalPickupEnabled flips the shop's pickup toggle inside the test tx. The
// seeded config row ships with delivery on and pickup off (migration 041), so a
// pickup conversion is refused until this runs.
func setLocalPickupEnabled(t *testing.T, tx pgx.Tx, enabled bool) {
	t.Helper()
	_, err := tx.Exec(context.Background(),
		`UPDATE shipping_config SET local_pickup_enabled = $1`, enabled)
	require.NoError(t, err)
}

// setLocalDeliveryEnabled flips the delivery toggle. The seeded row ships with
// delivery *on*, so without this the only covered case would be the default —
// and the guard that matters is the one staff hit when the van is off the road.
func setLocalDeliveryEnabled(t *testing.T, tx pgx.Tx, enabled bool) {
	t.Helper()
	_, err := tx.Exec(context.Background(),
		`UPDATE shipping_config SET local_delivery_enabled = $1`, enabled)
	require.NoError(t, err)
}

// lastShippingMethodAudit returns the action and metadata of the most recent
// shipping-method audit entry for an order.
func lastShippingMethodAudit(t *testing.T, tx pgx.Tx, orderID uuid.UUID) (string, map[string]any) {
	t.Helper()
	var action string
	var metadata map[string]any
	err := tx.QueryRow(context.Background(),
		`SELECT action, metadata FROM audit_log
		  WHERE resource_id = $1 AND action = $2
		  ORDER BY created_at DESC LIMIT 1`,
		orderID, audit.AuditOrderShippingMethodChanged).Scan(&action, &metadata)
	require.NoError(t, err, "expected a shipping-method audit entry")
	return action, metadata
}

// TestOrderService_ConvertShippedOrderToLocal covers the return leg off the
// carrier channel. The blocker that matters is a live label: postage is bought
// and unrefunded, so the order must not quietly become a van stop.
func TestOrderService_ConvertShippedOrderToLocal(t *testing.T) {
	ctx := context.Background()
	svc := newDeliveryOrderService()
	actor := testutil.TestActor()

	t.Run("shipped order converts to local delivery, total untouched", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodShipped),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))

		got, err := svc.ConvertShippedOrderToLocal(ctx, tx, order.ID, domain.ShippingMethodLocalDelivery, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.ShippingMethodLocalDelivery, got.ShippingMethod)
		// Shipping the customer already paid is left alone — refunding it is a
		// Stripe call and cannot happen in this transaction.
		assert.Equal(t, order.Total, got.Total)
		assert.Equal(t, order.ShippingTotal, got.ShippingTotal)
	})

	// An order created without an explicit method lands on 'shipped' via the
	// column default (migration 086), not on an empty string — so it is on the
	// carrier channel and convertible. This is the invariant that replaced the
	// old "nil is a second spelling of shipped" special case.
	t.Run("an order created without a method defaults to shipped and converts", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))
		require.Equal(t, domain.ShippingMethodShipped, order.ShippingMethod,
			"the column default must fill an unset method")

		got, err := svc.ConvertShippedOrderToLocal(ctx, tx, order.ID, domain.ShippingMethodLocalDelivery, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.ShippingMethodLocalDelivery, got.ShippingMethod)
	})

	t.Run("a fulfilled order still in the shop converts", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodShipped),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusFulfilled))

		got, err := svc.ConvertShippedOrderToLocal(ctx, tx, order.ID, domain.ShippingMethodLocalDelivery, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.ShippingMethodLocalDelivery, got.ShippingMethod)
	})

	t.Run("a live label blocks the conversion", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		staffID := testutil.CreateStaff(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodShipped),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))
		createOrderShipment(t, tx, order.ID, staffID, domain.ShipmentStatusLabelCreated, nil)

		_, err := svc.ConvertShippedOrderToLocal(ctx, tx, order.ID, domain.ShippingMethodLocalDelivery, actor)
		assert.ErrorIs(t, err, app.ErrOrderHasActiveLabel)
	})

	t.Run("a refund-requested label no longer blocks", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		staffID := testutil.CreateStaff(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodShipped),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))
		createOrderShipment(t, tx, order.ID, staffID, domain.ShipmentStatusLabelCreated, nil)

		// Requesting the refund is what frees the order — the same rule the
		// buy-label guard reads (domain.Shipment.BlocksRebuy).
		shipments, err := store.NewShippingStore().ListShipmentsByOrder(ctx, tx, order.ID)
		require.NoError(t, err)
		require.Len(t, shipments, 1)
		_, err = store.NewShippingStore().UpdateShipmentRefundRequested(
			ctx, tx, shipments[0].ID, "refund_1", &staffID)
		require.NoError(t, err)

		got, err := svc.ConvertShippedOrderToLocal(ctx, tx, order.ID, domain.ShippingMethodLocalDelivery, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.ShippingMethodLocalDelivery, got.ShippingMethod)
	})

	t.Run("pickup is refused while the shop has pickup switched off", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodShipped),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))

		_, err := svc.ConvertShippedOrderToLocal(ctx, tx, order.ID, domain.ShippingMethodPickup, actor)
		assert.ErrorIs(t, err, app.ErrInvalidOrderStatus)
	})

	t.Run("pickup converts once the shop enables it", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		setLocalPickupEnabled(t, tx, true)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodShipped),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))

		got, err := svc.ConvertShippedOrderToLocal(ctx, tx, order.ID, domain.ShippingMethodPickup, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.ShippingMethodPickup, got.ShippingMethod)
	})

	t.Run("an already-local order is rejected", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodLocalDelivery),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))

		_, err := svc.ConvertShippedOrderToLocal(ctx, tx, order.ID, domain.ShippingMethodLocalDelivery, actor)
		assert.ErrorIs(t, err, app.ErrInvalidOrderStatus)
	})

	t.Run("shipped target is not a local method", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodShipped),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))

		_, err := svc.ConvertShippedOrderToLocal(ctx, tx, order.ID, domain.ShippingMethodShipped, actor)
		assert.ErrorIs(t, err, app.ErrInvalidOrderStatus)
	})

	t.Run("order that already left the shop is rejected", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodShipped),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusShipped))

		_, err := svc.ConvertShippedOrderToLocal(ctx, tx, order.ID, domain.ShippingMethodLocalDelivery, actor)
		assert.ErrorIs(t, err, app.ErrInvalidOrderStatus)
	})

	t.Run("cancelled order is rejected", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodShipped),
			testutil.WithOrderStatus(domain.OrderStatusCancelled),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))

		_, err := svc.ConvertShippedOrderToLocal(ctx, tx, order.ID, domain.ShippingMethodLocalDelivery, actor)
		assert.ErrorIs(t, err, app.ErrInvalidOrderStatus)
	})

	t.Run("local delivery is refused while the shop has delivery switched off", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		setLocalDeliveryEnabled(t, tx, false)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodShipped),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))

		_, err := svc.ConvertShippedOrderToLocal(ctx, tx, order.ID, domain.ShippingMethodLocalDelivery, actor)
		assert.ErrorIs(t, err, app.ErrInvalidOrderStatus)
	})

	t.Run("the conversion is audited with both ends of the move", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodShipped),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))

		_, err := svc.ConvertShippedOrderToLocal(ctx, tx, order.ID, domain.ShippingMethodLocalDelivery, actor)
		require.NoError(t, err)

		action, metadata := lastShippingMethodAudit(t, tx, order.ID)
		assert.Equal(t, audit.AuditOrderShippingMethodChanged, action)
		assert.Equal(t, "shipped", metadata["from"])
		assert.Equal(t, "local_delivery", metadata["to"])
	})

	// The audit records a real method on both ends. This used to need a special
	// case because an unset method was NULL and reporting it as "shipped" would
	// have invented history; there is no longer anything to invent.
	t.Run("an order created without a method audits its real from-value", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))

		_, err := svc.ConvertShippedOrderToLocal(ctx, tx, order.ID, domain.ShippingMethodLocalDelivery, actor)
		require.NoError(t, err)

		_, metadata := lastShippingMethodAudit(t, tx, order.ID)
		assert.Equal(t, "shipped", metadata["from"])
		assert.Equal(t, "local_delivery", metadata["to"])
	})

	t.Run("missing order returns ErrOrderNotFound", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, err := svc.ConvertShippedOrderToLocal(ctx, tx, uuid.New(), domain.ShippingMethodLocalDelivery, actor)
		assert.ErrorIs(t, err, app.ErrOrderNotFound)
	})

	t.Run("a service without the shipments store fails closed", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodShipped),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))

		// Without the shipments store there is no way to see a live label, so
		// the conversion must refuse rather than skip the check.
		_, err := newOrderService().ConvertShippedOrderToLocal(
			ctx, tx, order.ID, domain.ShippingMethodLocalDelivery, actor)
		assert.ErrorIs(t, err, app.ErrInvalidOrderStatus)
	})
}
