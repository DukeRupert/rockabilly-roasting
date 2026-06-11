package app_test

import (
	"context"
	"testing"
	"time"

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
