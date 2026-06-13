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

// TestSubscriptionStore_DunningAcknowledgement covers the dashboard past-due
// alert lifecycle: a past-due subscription counts as unacknowledged until staff
// acknowledge it, and a subsequent failed charge (any transition back into
// past_due via UpdateStatus) clears the acknowledgement so the alert re-surfaces.
func TestSubscriptionStore_DunningAcknowledgement(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)

	s, custID, addrID, variantID, planID := subTrendFixture(t, tx)

	sub, err := s.Create(ctx, tx, store.CreateSubscriptionParams{
		CustomerID:         custID,
		PlanID:             planID,
		VariantID:          variantID,
		Quantity:           1,
		Status:             domain.SubscriptionStatusPastDue,
		ShippingAddressID:  addrID,
		CurrentPeriodStart: time.Now(),
		CurrentPeriodEnd:   time.Now().AddDate(0, 0, 30),
		NextOrderAt:        time.Now().AddDate(0, 0, 30),
	})
	require.NoError(t, err)

	pastDue := domain.SubscriptionStatusPastDue
	listUnacked := func() []domain.Subscription {
		rows, lerr := s.List(ctx, tx, store.SubscriptionFilter{
			Status:                     &pastDue,
			ExcludeDunningAcknowledged: true,
		})
		require.NoError(t, lerr)
		return rows
	}

	// Fresh past-due: counts and lists as needing a first look.
	count, err := s.CountPastDueUnacknowledged(ctx, tx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.Len(t, listUnacked(), 1)

	// Acknowledge: drops off the count and the unacknowledged list.
	require.NoError(t, s.SetDunningAcknowledged(ctx, tx, sub.ID))
	count, err = s.CountPastDueUnacknowledged(ctx, tx)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "acknowledged past-due should not count")
	assert.Empty(t, listUnacked())

	// A new failed charge re-enters past_due via UpdateStatus, which must clear
	// the stale acknowledgement so the alert comes back.
	_, err = s.UpdateStatus(ctx, tx, sub.ID, domain.SubscriptionStatusPastDue)
	require.NoError(t, err)
	count, err = s.CountPastDueUnacknowledged(ctx, tx)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "a new failed charge should re-surface the alert")
	assert.Len(t, listUnacked(), 1)

	// Resolving the subscription (payment succeeds → active) removes it from the
	// past-due count regardless of acknowledgement state.
	_, err = s.UpdateStatus(ctx, tx, sub.ID, domain.SubscriptionStatusActive)
	require.NoError(t, err)
	count, err = s.CountPastDueUnacknowledged(ctx, tx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

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
	require.NoError(t, s.SetDunningAcknowledged(ctx, tx, sub.ID))

	require.NoError(t, s.ClearDunning(ctx, tx, sub.ID))
	got, err := s.GetByIDAsStaff(ctx, tx, sub.ID)
	require.NoError(t, err)
	_, hasAttempt := got.Metadata["dunning_attempt"]
	assert.False(t, hasAttempt, "dunning_attempt cleared")
	_, hasAck := got.Metadata["dunning_acknowledged_at"]
	assert.False(t, hasAck, "dunning_acknowledged_at cleared")
}
