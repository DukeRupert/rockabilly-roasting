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

func TestSubscriptionStore_LockForCustomer(t *testing.T) {
	ctx := context.Background()
	subs := store.NewSubscriptionStore(nil)

	t.Run("returns the customer's own subscription with its claim", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		a := claimableSubscription(t, tx)
		ok, err := subs.ClaimRenewal(ctx, tx, []uuid.UUID{a.ID}, time.Now().Add(-15*time.Minute))
		require.NoError(t, err)
		require.True(t, ok)

		got, err := subs.LockForCustomer(ctx, tx, a.ID, a.CustomerID)
		require.NoError(t, err)
		assert.Equal(t, a.ID, got.ID)
		assert.NotNil(t, got.RenewalClaimedAt, "an edit must be able to see a renewal's claim")
	})

	t.Run("another customer's subscription is not found", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		a := claimableSubscription(t, tx)
		other := testutil.CreateCustomer(t, tx)

		_, err := subs.LockForCustomer(ctx, tx, a.ID, other.ID)
		assert.ErrorIs(t, err, pgx.ErrNoRows)
	})
}

func TestSubscriptionStore_LockBox(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	subs := store.NewSubscriptionStore(nil)

	a := claimableSubscription(t, tx)
	key := domain.SubscriptionBoxKey{ShippingAddressID: a.ShippingAddressID, NextOrderAt: a.NextOrderAt}

	sibling := func(status domain.SubscriptionStatus, nextOrderAt time.Time, addressID uuid.UUID) *domain.Subscription {
		t.Helper()
		sub, err := subs.Create(ctx, tx, store.CreateSubscriptionParams{
			CustomerID: a.CustomerID, PlanID: a.PlanID, VariantID: a.VariantID, Quantity: 1,
			Status: status, ShippingAddressID: addressID,
			CurrentPeriodStart: a.CurrentPeriodStart, CurrentPeriodEnd: nextOrderAt, NextOrderAt: nextOrderAt,
		})
		require.NoError(t, err)
		return sub
	}
	pastDue := sibling(domain.SubscriptionStatusPastDue, a.NextOrderAt, a.ShippingAddressID)
	sibling(domain.SubscriptionStatusPaused, a.NextOrderAt, a.ShippingAddressID)
	sibling(domain.SubscriptionStatusActive, a.NextOrderAt.Add(time.Hour), a.ShippingAddressID)
	otherAddr := testutil.CreateAddress(t, tx, a.CustomerID)
	sibling(domain.SubscriptionStatusActive, a.NextOrderAt, otherAddr.ID)

	members, err := subs.LockBox(ctx, tx, a.CustomerID, key)
	require.NoError(t, err)
	var ids []uuid.UUID
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	assert.ElementsMatch(t, []uuid.UUID{a.ID, pastDue.ID}, ids,
		"a box is the active and past-due members at one address on one instant, as GroupIntoBoxes has it")

	stranger := testutil.CreateCustomer(t, tx)
	none, err := subs.LockBox(ctx, tx, stranger.ID, key)
	require.NoError(t, err)
	assert.Empty(t, none, "another customer's box key finds nothing")
}

func TestSubscriptionStore_UpdateQuantity(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	subs := store.NewSubscriptionStore(nil)
	a := claimableSubscription(t, tx)

	got, err := subs.UpdateQuantity(ctx, tx, a.ID, 3)
	require.NoError(t, err)
	assert.Equal(t, 3, got.Quantity)
	assert.Equal(t, a.NextOrderAt.UTC(), got.NextOrderAt.UTC(), "the schedule is untouched")
}
