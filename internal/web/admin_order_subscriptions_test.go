package web

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

// The order page's subscriptions: the one stamped on the order, and every one
// linked through subscription_orders, which is the only link a batched renewal
// or a signup of several items has.

func TestOrderSubscriptionIDs(t *testing.T) {
	ctx := context.Background()
	d := newTestDeps()
	d.SubscriptionService = app.NewSubscriptionService(
		store.NewSubscriptionStore(nil), store.NewOrderStore(nil),
		audit.NewAuditWriter(), metrics.NewRegistry(),
	)

	tx := testutil.NewTestTx(t, testPool)
	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	a := orderPageSubscription(t, tx, customer.ID, addr.ID)
	b := orderPageSubscription(t, tx, customer.ID, addr.ID)
	subs := store.NewSubscriptionStore(nil)
	now := time.Now()

	t.Run("linked only through subscription_orders", func(t *testing.T) {
		order := testutil.CreateOrder(t, tx, customer.ID, addr.ID, addr.ID)
		for _, id := range []uuid.UUID{a.ID, b.ID} {
			require.NoError(t, subs.CreateSubscriptionOrder(ctx, tx, id, order.ID, now, now.AddDate(0, 0, 30)))
		}
		require.Nil(t, order.SubscriptionID)

		got, err := d.orderSubscriptionIDs(ctx, tx, order)
		require.NoError(t, err)
		assert.ElementsMatch(t, []uuid.UUID{a.ID, b.ID}, got)
	})

	t.Run("stamped, and linked too, is one subscription", func(t *testing.T) {
		order := testutil.CreateOrder(t, tx, customer.ID, addr.ID, addr.ID)
		require.NoError(t, store.NewOrderStore(nil).UpdateOrderSubscriptionID(ctx, tx, order.ID, a.ID))
		require.NoError(t, subs.CreateSubscriptionOrder(ctx, tx, a.ID, order.ID, now, now.AddDate(0, 0, 30)))
		order.SubscriptionID = &a.ID

		got, err := d.orderSubscriptionIDs(ctx, tx, order)
		require.NoError(t, err)
		assert.Equal(t, []uuid.UUID{a.ID}, got)
	})

	t.Run("stamped with no link row still shows", func(t *testing.T) {
		// Imported and older orders can carry the stamp alone.
		order := testutil.CreateOrder(t, tx, customer.ID, addr.ID, addr.ID)
		order.SubscriptionID = &b.ID

		got, err := d.orderSubscriptionIDs(ctx, tx, order)
		require.NoError(t, err)
		assert.Equal(t, []uuid.UUID{b.ID}, got)
	})

	t.Run("a retail order has none", func(t *testing.T) {
		order := testutil.CreateOrder(t, tx, customer.ID, addr.ID, addr.ID)
		got, err := d.orderSubscriptionIDs(ctx, tx, order)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func orderPageSubscription(t *testing.T, tx pgx.Tx, customerID, addressID uuid.UUID) *domain.Subscription {
	t.Helper()
	ctx := context.Background()
	subs := store.NewSubscriptionStore(nil)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	plan, err := subs.CreatePlan(ctx, tx, store.CreatePlanParams{
		Name: "Monthly", Interval: domain.SubscriptionIntervalEvery30Days, IntervalCount: 1, IsActive: true,
	})
	require.NoError(t, err)
	now := time.Now()
	sub, err := subs.Create(ctx, tx, store.CreateSubscriptionParams{
		CustomerID: customerID, PlanID: plan.ID, VariantID: variant.ID, Quantity: 1,
		Status: domain.SubscriptionStatusActive, ShippingAddressID: addressID,
		CurrentPeriodStart: now, CurrentPeriodEnd: now.AddDate(0, 0, 30), NextOrderAt: now.AddDate(0, 0, 30),
	})
	require.NoError(t, err)
	return sub
}
