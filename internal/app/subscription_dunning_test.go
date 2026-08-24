package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// TestSubscriptionStore_DunningRetryLifecycle covers the P3 dunning mechanics
// at the store layer: a failed charge schedules a retry (past_due + next_order_at),
// the renewal scheduler picks up past_due rows whose retry is due, a future
// retry drops out, expiry is terminal, and ClearDunning resets the counter.
func TestSubscriptionStore_DunningRetryLifecycle(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)

	s, custID, addrID, variantID, planID := subTrendFixture(t, tx)

	contains := func(subs []domain.Subscription, id uuid.UUID) bool {
		for _, sub := range subs {
			if sub.ID == id {
				return true
			}
		}
		return false
	}

	// Active sub whose next renewal is comfortably in the future (not yet due).
	sub, err := s.Create(ctx, tx, store.CreateSubscriptionParams{
		CustomerID:         custID,
		PlanID:             planID,
		VariantID:          variantID,
		Quantity:           1,
		Status:             domain.SubscriptionStatusActive,
		ShippingAddressID:  addrID,
		CurrentPeriodStart: time.Now(),
		CurrentPeriodEnd:   time.Now().AddDate(0, 0, 30),
		NextOrderAt:        time.Now().AddDate(0, 0, 30),
	})
	require.NoError(t, err)

	due, err := s.ListDueForRenewal(ctx, tx)
	require.NoError(t, err)
	assert.False(t, contains(due, sub.ID), "a future active renewal is not due yet")

	// First failed charge: schedule a retry in the past so it reads as due.
	pastRetry := time.Now().Add(-time.Hour)
	require.NoError(t, s.SetDunningRetry(ctx, tx, sub.ID, pastRetry, 1))

	got, err := s.GetByIDAsStaff(ctx, tx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.SubscriptionStatusPastDue, got.Status)
	assert.WithinDuration(t, pastRetry, got.NextOrderAt, time.Second)
	assert.Equal(t, float64(1), got.Metadata["dunning_attempt"], "attempt count stamped in metadata")

	due, err = s.ListDueForRenewal(ctx, tx)
	require.NoError(t, err)
	assert.True(t, contains(due, sub.ID), "a past_due sub whose retry is due must be picked up")

	// Reschedule into the future → drops out of the due list.
	require.NoError(t, s.SetDunningRetry(ctx, tx, sub.ID, time.Now().Add(72*time.Hour), 2))
	due, err = s.ListDueForRenewal(ctx, tx)
	require.NoError(t, err)
	assert.False(t, contains(due, sub.ID), "a future retry must not be due")

	// Pull it back to due, then expire: even with a due next_order_at, an
	// expired subscription must never be picked up again.
	require.NoError(t, s.SetDunningRetry(ctx, tx, sub.ID, pastRetry, 3))
	require.NoError(t, s.ExpireForDunning(ctx, tx, sub.ID))
	got, err = s.GetByIDAsStaff(ctx, tx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.SubscriptionStatusExpired, got.Status)
	require.NotNil(t, got.EndsAt, "expiry stamps ends_at")
	due, err = s.ListDueForRenewal(ctx, tx)
	require.NoError(t, err)
	assert.False(t, contains(due, sub.ID), "an expired sub is terminal — never due")
}

// TestSubscriptionStore_ClearDunning verifies a recovered subscription is left
// with no dunning bookkeeping so a future failure starts a clean schedule.
func TestSubscriptionStore_ClearDunning(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)

	s, custID, addrID, variantID, planID := subTrendFixture(t, tx)

	sub, err := s.Create(ctx, tx, store.CreateSubscriptionParams{
		CustomerID:         custID,
		PlanID:             planID,
		VariantID:          variantID,
		Quantity:           1,
		Status:             domain.SubscriptionStatusActive,
		ShippingAddressID:  addrID,
		CurrentPeriodStart: time.Now(),
		CurrentPeriodEnd:   time.Now().AddDate(0, 0, 30),
		NextOrderAt:        time.Now().AddDate(0, 0, 30),
	})
	require.NoError(t, err)

	require.NoError(t, s.SetDunningRetry(ctx, tx, sub.ID, time.Now().Add(-time.Hour), 2))
	require.NoError(t, s.SetDunningHardDecline(ctx, tx, sub.ID, "lost_card"))

	// Sanity: the latch actually landed, so the assertions below are meaningful.
	got, err := s.GetByIDAsStaff(ctx, tx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, true, got.Metadata["dunning_hard_decline"])
	require.Equal(t, "lost_card", got.Metadata["dunning_decline_code"])

	require.NoError(t, s.ClearDunning(ctx, tx, sub.ID))
	got, err = s.GetByIDAsStaff(ctx, tx, sub.ID)
	require.NoError(t, err)
	_, hasAttempt := got.Metadata["dunning_attempt"]
	assert.False(t, hasAttempt, "dunning_attempt cleared")
	// The hard-decline latch is the one that must not survive: leaving it set
	// would silently stop charging a subscription whose card now works.
	_, hasHard := got.Metadata["dunning_hard_decline"]
	assert.False(t, hasHard, "dunning_hard_decline cleared")
	_, hasCode := got.Metadata["dunning_decline_code"]
	assert.False(t, hasCode, "dunning_decline_code cleared")
}

// TestSubscriptionStore_SetDunningHardDecline verifies the latch only lands on a
// subscription that is actually past_due — a charge that succeeded in the
// meantime must not be branded as having a dead card.
func TestSubscriptionStore_SetDunningHardDecline(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)

	s, custID, addrID, variantID, planID := subTrendFixture(t, tx)

	sub, err := s.Create(ctx, tx, store.CreateSubscriptionParams{
		CustomerID:         custID,
		PlanID:             planID,
		VariantID:          variantID,
		Quantity:           1,
		Status:             domain.SubscriptionStatusActive,
		ShippingAddressID:  addrID,
		CurrentPeriodStart: time.Now(),
		CurrentPeriodEnd:   time.Now().AddDate(0, 0, 30),
		NextOrderAt:        time.Now().AddDate(0, 0, 30),
	})
	require.NoError(t, err)

	// Active subscription: the latch is a no-op.
	require.NoError(t, s.SetDunningHardDecline(ctx, tx, sub.ID, "stolen_card"))
	got, err := s.GetByIDAsStaff(ctx, tx, sub.ID)
	require.NoError(t, err)
	_, hasHard := got.Metadata["dunning_hard_decline"]
	assert.False(t, hasHard, "an active subscription must not be flagged hard-declined")

	// Once past_due it takes.
	require.NoError(t, s.SetDunningRetry(ctx, tx, sub.ID, time.Now().Add(-time.Hour), 1))
	require.NoError(t, s.SetDunningHardDecline(ctx, tx, sub.ID, "stolen_card"))
	got, err = s.GetByIDAsStaff(ctx, tx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, true, got.Metadata["dunning_hard_decline"])
	assert.Equal(t, "stolen_card", got.Metadata["dunning_decline_code"])
}
