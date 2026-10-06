package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func TestSubscriptionStore_ClaimRenewal(t *testing.T) {
	ctx := context.Background()
	subs := store.NewSubscriptionStore(nil)
	stale := func() time.Time { return time.Now().Add(-15 * time.Minute) }

	t.Run("all or nothing", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		a := claimableSubscription(t, tx)
		b := claimableSubscription(t, tx)

		ok, err := subs.ClaimRenewal(ctx, tx, []uuid.UUID{b.ID}, stale())
		require.NoError(t, err)
		require.True(t, ok)

		ok, err = subs.ClaimRenewal(ctx, tx, []uuid.UUID{a.ID, b.ID}, stale())
		require.NoError(t, err)
		assert.False(t, ok, "b is held")

		// a was not left claimed by the refused call.
		var claimedAt *time.Time
		require.NoError(t, tx.QueryRow(ctx,
			`SELECT renewal_claimed_at FROM subscriptions WHERE id = $1`, a.ID).Scan(&claimedAt))
		assert.Nil(t, claimedAt)
	})

	t.Run("a claim older than the lease is taken over", func(t *testing.T) {
		// A process that died mid-renewal never releases. The lease is what
		// stops that subscription being held for ever.
		tx := testutil.NewTestTx(t, testPool)
		a := claimableSubscription(t, tx)
		_, err := tx.Exec(ctx, `UPDATE subscriptions SET renewal_claimed_at = now() - interval '20 minutes' WHERE id = $1`, a.ID)
		require.NoError(t, err)

		ok, err := subs.ClaimRenewal(ctx, tx, []uuid.UUID{a.ID}, stale())
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("release frees it", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		a := claimableSubscription(t, tx)
		ok, err := subs.ClaimRenewal(ctx, tx, []uuid.UUID{a.ID}, stale())
		require.NoError(t, err)
		require.True(t, ok)

		require.NoError(t, subs.ReleaseRenewalClaim(ctx, tx, []uuid.UUID{a.ID}))
		ok, err = subs.ClaimRenewal(ctx, tx, []uuid.UUID{a.ID}, stale())
		require.NoError(t, err)
		assert.True(t, ok)
	})
}

func claimableSubscription(t *testing.T, tx pgx.Tx) *domain.Subscription {
	t.Helper()
	ctx := context.Background()
	subs := store.NewSubscriptionStore(nil)
	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	plan, err := subs.CreatePlan(ctx, tx, store.CreatePlanParams{
		Name: "Monthly", Interval: domain.SubscriptionIntervalEvery30Days, IntervalCount: 1, IsActive: true,
	})
	require.NoError(t, err)
	now := time.Now()
	sub, err := subs.Create(ctx, tx, store.CreateSubscriptionParams{
		CustomerID: customer.ID, PlanID: plan.ID, VariantID: variant.ID, Quantity: 1,
		Status: domain.SubscriptionStatusActive, ShippingAddressID: addr.ID,
		CurrentPeriodStart: now, CurrentPeriodEnd: now.AddDate(0, 0, 30), NextOrderAt: now.AddDate(0, 0, 30),
	})
	require.NoError(t, err)
	return sub
}
