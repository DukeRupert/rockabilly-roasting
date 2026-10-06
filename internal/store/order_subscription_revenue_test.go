package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// Subscription revenue on the dashboard counts every order a subscription paid
// for. orders.subscription_id names one subscription, so an order covering
// several — a batched renewal, or a signup of several items — leaves it null
// and is linked only through subscription_orders. Those used to count as
// one-time revenue.
func TestSumOrderRevenue_CountsOrdersLinkedOnlyThroughSubscriptionOrders(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	orders := store.NewOrderStore(nil)
	subs := store.NewSubscriptionStore(nil)

	a := claimableSubscription(t, tx)
	b := claimableSubscription(t, tx)
	addr := testutil.CreateAddress(t, tx, a.CustomerID)

	// Two subscriptions, one order, subscription_id null.
	batched := testutil.CreateOrder(t, tx, a.CustomerID, addr.ID, addr.ID,
		testutil.WithOrderTotals(3000, 0, 0, 0, 3000))
	now := time.Now()
	for _, id := range []uuid.UUID{a.ID, b.ID} {
		require.NoError(t, subs.CreateSubscriptionOrder(ctx, tx, id, batched.ID, now, now.AddDate(0, 0, 30)))
	}
	// And one retail order beside it.
	testutil.CreateOrder(t, tx, a.CustomerID, addr.ID, addr.ID, testutil.WithOrderTotals(500, 0, 0, 0, 500))

	yes, no := true, false
	sub, err := orders.SumOrderRevenue(ctx, tx, store.OrderFilter{CustomerID: &a.CustomerID, OnlySubscription: &yes})
	require.NoError(t, err)
	assert.Equal(t, 3000, sub, "the batched order is subscription revenue")

	oneTime, err := orders.SumOrderRevenue(ctx, tx, store.OrderFilter{CustomerID: &a.CustomerID, OnlySubscription: &no})
	require.NoError(t, err)
	assert.Equal(t, 500, oneTime, "and not one-time revenue")
}
