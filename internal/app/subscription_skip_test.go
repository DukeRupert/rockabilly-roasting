package app_test

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

// TestSubscriptionService_Skip covers the two forms of a skip — by shipment
// count and by restart date — plus the guards that keep a skip from becoming a
// silent reschedule (backwards dates, unbounded gaps, non-active subscriptions).
func TestSubscriptionService_Skip(t *testing.T) {
	pool := testPool
	ctx := context.Background()
	actor := testutil.TestActor()
	subs := app.NewSubscriptionService(
		store.NewSubscriptionStore(nil),
		store.NewOrderStore(nil),
		audit.NewAuditWriter(),
		metrics.NewRegistry(),
	)

	// setup creates an active monthly subscription whose current period ends
	// (and next order fires) 30 days out.
	setup := func(t *testing.T, tx pgx.Tx) *domain.Subscription {
		t.Helper()
		customer := testutil.CreateCustomer(t, tx)
		addr := testutil.CreateAddress(t, tx, customer.ID)
		product := testutil.CreateProduct(t, tx)
		variant := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("SKIP-12OZ"))
		plan, err := store.NewSubscriptionStore(nil).CreatePlan(ctx, tx, store.CreatePlanParams{
			Name:          "Monthly",
			Interval:      domain.SubscriptionIntervalEvery30Days,
			IntervalCount: 1,
			IsActive:      true,
		})
		require.NoError(t, err)
		return createTestSubscription(t, tx, customer.ID, plan.ID, variant.ID, addr.ID)
	}

	t.Run("skipping shipments advances the schedule by whole cadences", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		sub := setup(t, tx)
		originalEnd := sub.CurrentPeriodEnd

		updated, err := subs.SkipSubscription(ctx, tx, sub.ID, app.SkipSubscriptionParams{Intervals: 2}, actor)
		require.NoError(t, err)

		want := originalEnd.AddDate(0, 0, 60)
		assert.WithinDuration(t, want, updated.CurrentPeriodEnd, time.Second)
		assert.WithinDuration(t, want, updated.NextOrderAt, time.Second)
		// The subscription keeps running — a skip is not a pause.
		assert.Equal(t, domain.SubscriptionStatusActive, updated.Status)
		// The period start is left alone: the current period stretches rather
		// than jumping into the future.
		assert.WithinDuration(t, sub.CurrentPeriodStart, updated.CurrentPeriodStart, time.Second)

		// Persisted, not just returned.
		reread, err := subs.GetSubscriptionAsStaff(ctx, tx, sub.ID)
		require.NoError(t, err)
		assert.WithinDuration(t, want, reread.NextOrderAt, time.Second)
	})

	t.Run("a restart date sets the next order to that day", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		sub := setup(t, tx)
		resume := time.Now().UTC().AddDate(0, 0, 45).Truncate(time.Second)

		updated, err := subs.SkipSubscription(ctx, tx, sub.ID, app.SkipSubscriptionParams{ResumeOn: &resume}, actor)
		require.NoError(t, err)
		assert.WithinDuration(t, resume, updated.NextOrderAt, time.Second)
		assert.WithinDuration(t, resume, updated.CurrentPeriodEnd, time.Second)
	})

	t.Run("skipping writes an audit record", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		sub := setup(t, tx)

		_, err := subs.SkipSubscription(ctx, tx, sub.ID, app.SkipSubscriptionParams{Intervals: 1}, actor)
		require.NoError(t, err)

		var count int
		require.NoError(t, tx.QueryRow(ctx,
			`SELECT count(*) FROM audit_log WHERE resource_type = 'subscription' AND resource_id = $1 AND action = $2`,
			sub.ID, audit.AuditSubscriptionSkipped,
		).Scan(&count))
		assert.Equal(t, 1, count)
	})

	t.Run("rejects out-of-range and contradictory requests", func(t *testing.T) {
		past := time.Now().UTC().AddDate(0, 0, -1)
		tooFar := time.Now().UTC().AddDate(0, 0, domain.SubscriptionMaxSkipDays+1)
		// Inside the 60-day window but before the shipment already scheduled —
		// that would pull the charge forward, which is not a skip.
		earlier := time.Now().UTC().AddDate(0, 0, 10)
		soon := time.Now().UTC().AddDate(0, 0, 20)

		cases := []struct {
			name   string
			params app.SkipSubscriptionParams
			want   error
		}{
			{"no form given", app.SkipSubscriptionParams{}, app.ErrInvalidSkipRequest},
			{"both forms given", app.SkipSubscriptionParams{Intervals: 1, ResumeOn: &soon}, app.ErrInvalidSkipRequest},
			{"too many shipments", app.SkipSubscriptionParams{Intervals: domain.SubscriptionMaxSkipIntervals + 1}, app.ErrSkipIntervalsOutOfRange},
			{"date in the past", app.SkipSubscriptionParams{ResumeOn: &past}, app.ErrSkipDateOutOfRange},
			{"date beyond the ceiling", app.SkipSubscriptionParams{ResumeOn: &tooFar}, app.ErrSkipDateOutOfRange},
			{"date before the scheduled order", app.SkipSubscriptionParams{ResumeOn: &earlier}, app.ErrSkipDateBeforeNextOrder},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				tx := testutil.NewTestTx(t, pool)
				sub := setup(t, tx)
				_, err := subs.SkipSubscription(ctx, tx, sub.ID, c.params, actor)
				assert.ErrorIs(t, err, c.want)
			})
		}
	})

	// A subscription whose period end has slipped into the past (renewals
	// backlogged, or a worker failing) must not come out of a skip with a
	// next_order_at behind it — the next sweep would bill the shipment the
	// customer just asked to skip.
	t.Run("skipping an overdue subscription still lands in the future", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		sub := setup(t, tx)

		past := time.Now().UTC().AddDate(0, 0, -90)
		_, err := tx.Exec(ctx,
			`UPDATE subscriptions SET current_period_start = $2, current_period_end = $3, next_order_at = $3 WHERE id = $1`,
			sub.ID, past.AddDate(0, 0, -30), past)
		require.NoError(t, err)

		updated, err := subs.SkipSubscription(ctx, tx, sub.ID, app.SkipSubscriptionParams{Intervals: 1}, actor)
		require.NoError(t, err)
		assert.True(t, updated.NextOrderAt.After(time.Now()),
			"next order %s must be in the future", updated.NextOrderAt)
		// One cadence from today, not one cadence from a stale period end.
		assert.WithinDuration(t, time.Now().AddDate(0, 0, 30), updated.NextOrderAt, time.Minute)
	})

	t.Run("only active subscriptions can be skipped", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		sub := setup(t, tx)
		_, err := subs.PauseSubscription(ctx, tx, sub.ID, nil, actor)
		require.NoError(t, err)

		_, err = subs.SkipSubscription(ctx, tx, sub.ID, app.SkipSubscriptionParams{Intervals: 1}, actor)
		assert.ErrorIs(t, err, app.ErrSubscriptionNotSkippable)
	})

	t.Run("undo puts the schedule back exactly", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		sub := setup(t, tx)

		skipped, err := subs.SkipSubscription(ctx, tx, sub.ID, app.SkipSubscriptionParams{Intervals: 2}, actor)
		require.NoError(t, err)
		require.False(t, skipped.NextOrderAt.Equal(sub.NextOrderAt))

		restored, err := subs.UndoSkip(ctx, tx, sub.ID, actor)
		require.NoError(t, err)
		assert.WithinDuration(t, sub.NextOrderAt, restored.NextOrderAt, time.Second)
		assert.WithinDuration(t, sub.CurrentPeriodEnd, restored.CurrentPeriodEnd, time.Second)

		reread, err := subs.GetSubscriptionAsStaff(ctx, tx, sub.ID)
		require.NoError(t, err)
		assert.WithinDuration(t, sub.NextOrderAt, reread.NextOrderAt, time.Second)

		// The snapshot is spent — undo is a one-shot, not a toggle.
		_, ok := reread.SkipUndo()
		assert.False(t, ok)
		_, err = subs.UndoSkip(ctx, tx, sub.ID, actor)
		assert.ErrorIs(t, err, app.ErrNoSkipToUndo)
	})

	t.Run("undo without a skip is refused", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		sub := setup(t, tx)
		_, err := subs.UndoSkip(ctx, tx, sub.ID, actor)
		assert.ErrorIs(t, err, app.ErrNoSkipToUndo)
	})

	t.Run("undo reverses only the most recent skip", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		sub := setup(t, tx)

		first, err := subs.SkipSubscription(ctx, tx, sub.ID, app.SkipSubscriptionParams{Intervals: 1}, actor)
		require.NoError(t, err)
		_, err = subs.SkipSubscription(ctx, tx, sub.ID, app.SkipSubscriptionParams{Intervals: 1}, actor)
		require.NoError(t, err)

		restored, err := subs.UndoSkip(ctx, tx, sub.ID, actor)
		require.NoError(t, err)
		// Back to where the second skip started, not all the way to the original.
		assert.WithinDuration(t, first.NextOrderAt, restored.NextOrderAt, time.Second)
	})

	t.Run("undo is refused once the schedule has moved on", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		sub := setup(t, tx)
		_, err := subs.SkipSubscription(ctx, tx, sub.ID, app.SkipSubscriptionParams{Intervals: 1}, actor)
		require.NoError(t, err)

		// A plan change reschedules the subscription; the snapshot no longer
		// describes a skip that can be cleanly reversed.
		newPlan, err := store.NewSubscriptionStore(nil).CreatePlan(ctx, tx, store.CreatePlanParams{
			Name:          "Weekly",
			Interval:      domain.SubscriptionIntervalEvery7Days,
			IntervalCount: 1,
			IsActive:      true,
		})
		require.NoError(t, err)
		_, err = subs.ChangePlan(ctx, tx, sub.ID, newPlan.ID, actor)
		require.NoError(t, err)

		_, err = subs.UndoSkip(ctx, tx, sub.ID, actor)
		assert.ErrorIs(t, err, app.ErrNoSkipToUndo)
	})

	t.Run("undo writes an audit record", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		sub := setup(t, tx)
		_, err := subs.SkipSubscription(ctx, tx, sub.ID, app.SkipSubscriptionParams{Intervals: 1}, actor)
		require.NoError(t, err)
		_, err = subs.UndoSkip(ctx, tx, sub.ID, actor)
		require.NoError(t, err)

		var count int
		require.NoError(t, tx.QueryRow(ctx,
			`SELECT count(*) FROM audit_log WHERE resource_type = 'subscription' AND resource_id = $1 AND action = $2`,
			sub.ID, audit.AuditSubscriptionSkipUndone,
		).Scan(&count))
		assert.Equal(t, 1, count)
	})

	t.Run("unknown subscription is not found", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		_, err := subs.SkipSubscription(ctx, tx, uuid.New(), app.SkipSubscriptionParams{Intervals: 1}, actor)
		assert.ErrorIs(t, err, app.ErrSubscriptionNotFound)
	})
}
