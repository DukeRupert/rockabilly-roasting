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

// ListByOrder finds an order's subscriptions through subscription_orders, which
// is the only link an order that started or renewed several of them has.
func TestSubscriptionStore_ListByOrder(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	subs := store.NewSubscriptionStore(nil)

	a := claimableSubscription(t, tx)
	b := claimableSubscription(t, tx)
	unrelated := claimableSubscription(t, tx)
	addr := testutil.CreateAddress(t, tx, a.CustomerID)
	order := testutil.CreateOrder(t, tx, a.CustomerID, addr.ID, addr.ID)
	other := testutil.CreateOrder(t, tx, a.CustomerID, addr.ID, addr.ID)

	now := time.Now()
	for _, id := range []uuid.UUID{a.ID, b.ID} {
		require.NoError(t, subs.CreateSubscriptionOrder(ctx, tx, id, order.ID, now, now.AddDate(0, 0, 30)))
	}
	require.NoError(t, subs.CreateSubscriptionOrder(ctx, tx, unrelated.ID, other.ID, now, now.AddDate(0, 0, 30)))

	got, err := subs.ListByOrder(ctx, tx, order.ID)
	require.NoError(t, err)
	ids := make([]uuid.UUID, len(got))
	for i, s := range got {
		ids[i] = s.ID
	}
	assert.ElementsMatch(t, []uuid.UUID{a.ID, b.ID}, ids)

	none, err := subs.ListByOrder(ctx, tx, uuid.New())
	require.NoError(t, err)
	assert.Empty(t, none)
}
