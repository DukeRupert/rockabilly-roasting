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

func newOrderService() *app.OrderService {
	return app.NewOrderService(store.NewOrderStore(), audit.NewAuditWriter(), metrics.NewRegistry())
}

// orderFixtures creates the prerequisite customer + addresses and returns them.
func orderFixtures(t *testing.T, tx pgx.Tx) (customerID, shippingID, billingID uuid.UUID) {
	t.Helper()
	customer := testutil.CreateCustomer(t, tx)
	shipping := testutil.CreateAddress(t, tx, customer.ID)
	billing := testutil.CreateAddress(t, tx, customer.ID)
	return customer.ID, shipping.ID, billing.ID
}

func TestOrderService_GetOrder(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newOrderService()
	ctx := context.Background()

	custID, shipID, billID := orderFixtures(t, tx)
	order := testutil.CreateOrder(t, tx, custID, shipID, billID)

	got, err := svc.GetOrderAsStaff(ctx, tx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, order.ID, got.ID)

	_, err = svc.GetOrderAsStaff(ctx, tx, uuid.New())
	assert.ErrorIs(t, err, app.ErrOrderNotFound)
}

func TestOrderService_CancelOrder(t *testing.T) {
	pool := testPool
	ctx := context.Background()
	svc := newOrderService()
	actor := testutil.TestActor()

	t.Run("cancel pending order", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusPending))

		cancelled, err := svc.CancelOrder(ctx, tx, order.ID, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderStatusCancelled, cancelled.Status)

		entry := testutil.LastAuditEntry(t, tx, "order", order.ID)
		assert.Equal(t, audit.AuditOrderCancelled, entry.Action)
	})

	t.Run("cancel confirmed order", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusConfirmed))

		cancelled, err := svc.CancelOrder(ctx, tx, order.ID, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderStatusCancelled, cancelled.Status)
	})

	t.Run("cannot cancel complete order", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusComplete))

		_, err := svc.CancelOrder(ctx, tx, order.ID, actor)
		assert.ErrorIs(t, err, app.ErrOrderNotCancellable)
	})

	t.Run("cannot cancel cancelled order", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusCancelled))

		_, err := svc.CancelOrder(ctx, tx, order.ID, actor)
		assert.ErrorIs(t, err, app.ErrOrderNotCancellable)
	})
}

func TestOrderService_RefundOrder(t *testing.T) {
	pool := testPool
	ctx := context.Background()
	svc := newOrderService()
	actor := testutil.TestActor()

	t.Run("refund captured complete order", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusComplete),
			testutil.WithPaymentStatus(domain.PaymentStatusCaptured))

		refunded, err := svc.RefundOrder(ctx, tx, order.ID, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderStatusRefunded, refunded.Status)
		assert.Equal(t, domain.PaymentStatusRefunded, refunded.PaymentStatus)

		entry := testutil.LastAuditEntry(t, tx, "order", order.ID)
		assert.Equal(t, audit.AuditOrderRefunded, entry.Action)
	})

	t.Run("refund captured confirmed order", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusConfirmed),
			testutil.WithPaymentStatus(domain.PaymentStatusCaptured))

		refunded, err := svc.RefundOrder(ctx, tx, order.ID, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderStatusRefunded, refunded.Status)
	})

	t.Run("cannot refund awaiting payment", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusComplete),
			testutil.WithPaymentStatus(domain.PaymentStatusAwaiting))

		_, err := svc.RefundOrder(ctx, tx, order.ID, actor)
		assert.ErrorIs(t, err, app.ErrOrderNotRefundable)
	})

	t.Run("cannot refund already refunded", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusRefunded),
			testutil.WithPaymentStatus(domain.PaymentStatusRefunded))

		_, err := svc.RefundOrder(ctx, tx, order.ID, actor)
		assert.ErrorIs(t, err, app.ErrOrderNotRefundable)
	})
}

func TestOrderService_UpdateFulfillmentStatus(t *testing.T) {
	pool := testPool
	ctx := context.Background()
	svc := newOrderService()
	actor := testutil.TestActor()

	t.Run("fulfilled writes audit", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID)

		got, err := svc.UpdateFulfillmentStatus(ctx, tx, order.ID, domain.FulfillmentStatusFulfilled, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.FulfillmentStatusFulfilled, got.FulfillmentStatus)

		entry := testutil.LastAuditEntry(t, tx, "order", order.ID)
		assert.Equal(t, audit.AuditOrderFulfilled, entry.Action)
	})

	t.Run("partial does not write audit", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID)

		got, err := svc.UpdateFulfillmentStatus(ctx, tx, order.ID, domain.FulfillmentStatusPartiallyFulfilled, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.FulfillmentStatusPartiallyFulfilled, got.FulfillmentStatus)

		testutil.AssertNoAuditEntry(t, tx, "order", order.ID)
	})
}
