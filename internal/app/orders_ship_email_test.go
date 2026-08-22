package app_test

import (
	"context"
	"testing"

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

// shippedCall records one EnqueueOrderShipped call so the tests can assert
// both that the email was scheduled and that it points at the right shipment.
type shippedCall struct {
	orderID    uuid.UUID
	customerID uuid.UUID
	shipmentID uuid.UUID
}

// shipEnqueuer captures the shipped notifications ShipOrder fans out; every
// other JobEnqueuer method comes from the embedded no-op fake.
type shipEnqueuer struct {
	*fakeEnqueuer
	shipped []shippedCall
}

func (e *shipEnqueuer) EnqueueOrderShipped(_ context.Context, _ pgx.Tx, orderID, customerID, shipmentID uuid.UUID) error {
	e.shipped = append(e.shipped, shippedCall{orderID, customerID, shipmentID})
	return nil
}

func newShipOrderService(enq app.JobEnqueuer) *app.OrderService {
	return app.NewOrderService(store.NewOrderStore(nil), audit.NewAuditWriter(), metrics.NewRegistry()).
		WithShipments(store.NewShippingStore()).
		WithEnqueuer(enq)
}

// TestOrderService_ShipOrderEmail covers the notification ShipOrder schedules.
// Nothing enqueued this between the Pirate Ship removal and now, so these
// cases exist to keep the customer in the loop when a parcel goes out.
func TestOrderService_ShipOrderEmail(t *testing.T) {
	ctx := context.Background()
	actor := testutil.TestActor()

	t.Run("with a tracked shipment, emails the customer", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		enq := &shipEnqueuer{fakeEnqueuer: &fakeEnqueuer{}}
		svc := newShipOrderService(enq)

		custID, shipAddrID, billID := orderFixtures(t, tx)
		staffID := testutil.CreateStaff(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipAddrID, billID,
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusFulfilled))
		createOrderShipment(t, tx, order.ID, staffID, domain.ShipmentStatusInTransit, nil)

		_, err := svc.ShipOrder(ctx, tx, order.ID, actor)
		require.NoError(t, err)

		require.Len(t, enq.shipped, 1)
		assert.Equal(t, order.ID, enq.shipped[0].orderID)
		assert.Equal(t, custID, enq.shipped[0].customerID)
		assert.NotEqual(t, uuid.Nil, enq.shipped[0].shipmentID)
	})

	t.Run("without a shipment, sends nothing", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		enq := &shipEnqueuer{fakeEnqueuer: &fakeEnqueuer{}}
		svc := newShipOrderService(enq)

		custID, shipAddrID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipAddrID, billID,
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusFulfilled))

		_, err := svc.ShipOrder(ctx, tx, order.ID, actor)
		require.NoError(t, err)

		// No label means no carrier and no tracking number, and the shipped
		// email is built entirely around those. Silence beats a blank one.
		assert.Empty(t, enq.shipped)
	})

	t.Run("re-shipping an already shipped order does not email twice", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		enq := &shipEnqueuer{fakeEnqueuer: &fakeEnqueuer{}}
		svc := newShipOrderService(enq)

		custID, shipAddrID, billID := orderFixtures(t, tx)
		staffID := testutil.CreateStaff(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipAddrID, billID,
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusFulfilled))
		createOrderShipment(t, tx, order.ID, staffID, domain.ShipmentStatusInTransit, nil)

		_, err := svc.ShipOrder(ctx, tx, order.ID, actor)
		require.NoError(t, err)

		// The state machine only accepts an order still in "fulfilled", which
		// is what makes a double-click safe.
		_, err = svc.ShipOrder(ctx, tx, order.ID, actor)
		require.Error(t, err)
		assert.Len(t, enq.shipped, 1)
	})
}
