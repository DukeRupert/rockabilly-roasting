package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/payments/paymentstest"
	"github.com/dukerupert/hiri/internal/testutil"
)

// Critique item 18: a skip during the charge was overwritten and charged.
//
// A skip leaves the subscription active, so item 4's status guard let the
// write through: the order was written, the charge kept, and the skip's new
// schedule overwritten from the renewal's stale read. The advance is now also
// guarded on the next_order_at the renewal read before charging. A schedule
// moved under it — a skip, a whole-box skip — rolls the write back and the
// charge is refunded, as a status change is.

// skipDuring makes the provider's charge the moment a customer skips the next
// shipment through the real skip path.
func skipDuring(t *testing.T, provider *paymentstest.Provider, id uuid.UUID) {
	t.Helper()
	provider.OnCreate = func() {
		ctx := context.Background()
		tx, err := testPool.Begin(ctx)
		require.NoError(t, err)
		defer tx.Rollback(ctx) //nolint:errcheck
		_, err = newSubscriptionService().SkipSubscription(ctx, tx, id,
			app.SkipSubscriptionParams{Intervals: 1}, testutil.TestActor())
		require.NoError(t, err)
		require.NoError(t, tx.Commit(ctx))
	}
}

func TestRenewSubscription_ASkipDuringTheChargeIsRefunded(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc, _ := newFailureRenewalService(provider)
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)
	skipDuring(t, provider, sub.ID)

	order, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	assert.ErrorIs(t, err, app.ErrRenewalNotDue, "skipped: not due, and not an error worth retrying")
	assert.Nil(t, order)
	assert.Zero(t, customerOrderCount(t, sub.CustomerID), "no order for a skipped shipment")
	require.Len(t, provider.Refunds(), 1, "the money goes back")

	got := readSub(t, sub.ID)
	assert.True(t, got.NextOrderAt.After(sub.NextOrderAt.AddDate(0, 0, 20)), "the skip stands: %s", got.NextOrderAt)
	assert.Equal(t, domain.SubscriptionStatusActive, got.Status)
	assert.Nil(t, got.RenewalPaymentIntentID)

	var reason string
	require.NoError(t, testPool.QueryRow(ctx, `SELECT metadata->>'reason' FROM audit_log
		WHERE resource_id = $1 AND action = 'subscription.renewal_orphaned_charge'`, sub.ID).Scan(&reason))
	assert.Equal(t, "schedule_changed", reason)
}

// A past-due subscription whose schedule moves mid-charge (a plan change) is
// a schedule change too, not a status change: it is still past due.
func TestRenewSubscription_APastDueScheduleChangeIsRecordedAsOne(t *testing.T) {
	provider := paymentstest.New()
	svc, _ := newFailureRenewalService(provider)
	sub, _ := dueBox(t, domain.SubscriptionStatusPastDue)
	provider.OnCreate = func() {
		_, err := testPool.Exec(context.Background(),
			`UPDATE subscriptions SET next_order_at = next_order_at + interval '7 days' WHERE id = $1`, sub.ID)
		require.NoError(t, err)
	}
	_, err := svc.RenewSubscription(context.Background(), testPool, sub.ID)
	assert.ErrorIs(t, err, app.ErrRenewalNotDue)
	assert.Len(t, provider.Refunds(), 1)
}

func TestRenewBatch_AMemberSkippedDuringTheChargeRefundsTheBox(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc, _ := newFailureRenewalService(provider)
	a, b := dueBox(t, domain.SubscriptionStatusActive)
	skipDuring(t, provider, b.ID)

	order, err := svc.RenewBatch(ctx, testPool, []uuid.UUID{a.ID, b.ID})
	assert.ErrorIs(t, err, app.ErrRenewalNotDue)
	assert.Nil(t, order)
	assert.Zero(t, customerOrderCount(t, a.CustomerID))
	require.Len(t, provider.Refunds(), 1)
	assert.True(t, readSub(t, a.ID).NextOrderAt.Equal(a.NextOrderAt), "the member not skipped is still due, for the next run")
	assert.True(t, readSub(t, b.ID).NextOrderAt.After(b.NextOrderAt.AddDate(0, 0, 20)), "the skip stands")
}
