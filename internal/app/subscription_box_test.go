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
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// Whole-box skip and pause. Members come only from the customer's own
// subscriptions at the box's address and instant; every member is checked
// before any is written, so a box that cannot be skipped or paused is left as
// it was.

type boxFixture struct {
	customer *domain.Customer
	addr     *domain.Address
	at       time.Time
	weekly   *domain.Subscription // every 7 days
	fourWeek *domain.Subscription // every 7 days × 4, due at the same instant
}

// boxMember writes a subscription due at at, on a plan of interval × count.
func boxMember(t *testing.T, tx pgx.Tx, customerID, addrID uuid.UUID, at time.Time,
	interval domain.SubscriptionInterval, count int, status domain.SubscriptionStatus) *domain.Subscription {
	t.Helper()
	ctx := context.Background()
	subs := store.NewSubscriptionStore(nil)
	plan, err := subs.CreatePlan(ctx, tx, store.CreatePlanParams{
		Name: "Plan", Interval: interval, IntervalCount: count, IsActive: true,
	})
	require.NoError(t, err)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	sub, err := subs.Create(ctx, tx, store.CreateSubscriptionParams{
		CustomerID: customerID, PlanID: plan.ID, VariantID: variant.ID, Quantity: 1,
		Status: status, ShippingAddressID: addrID,
		CurrentPeriodStart: at.AddDate(0, 0, -7), CurrentPeriodEnd: at, NextOrderAt: at,
	})
	require.NoError(t, err)
	return sub
}

func newBoxFixture(t *testing.T, tx pgx.Tx) boxFixture {
	t.Helper()
	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	at := time.Now().UTC().AddDate(0, 0, 3).Truncate(time.Microsecond)
	return boxFixture{
		customer: customer, addr: addr, at: at,
		weekly:   boxMember(t, tx, customer.ID, addr.ID, at, domain.SubscriptionIntervalEvery7Days, 1, domain.SubscriptionStatusActive),
		fourWeek: boxMember(t, tx, customer.ID, addr.ID, at, domain.SubscriptionIntervalEvery7Days, 4, domain.SubscriptionStatusActive),
	}
}

func (f boxFixture) key() domain.SubscriptionBoxKey {
	return domain.SubscriptionBoxKey{ShippingAddressID: f.addr.ID, NextOrderAt: f.at}
}

func reread(t *testing.T, tx pgx.Tx, id uuid.UUID) *domain.Subscription {
	t.Helper()
	sub, err := store.NewSubscriptionStore(nil).GetByIDAsStaff(context.Background(), tx, id)
	require.NoError(t, err)
	return sub
}

func TestSkipBox(t *testing.T) {
	ctx := context.Background()
	actor := testutil.TestActor()
	svc := newSubscriptionService()

	t.Run("every member moves to the same day and stays one box", func(t *testing.T) {
		// A weekly and a four-weekly that coincide now. Skipping by intervals
		// walks each one's own cadence and splits them; a date keeps them
		// together.
		tx := testutil.NewTestTx(t, testPool)
		f := newBoxFixture(t, tx)
		resumeOn := f.at.AddDate(0, 0, 10)

		skipped, err := svc.SkipBox(ctx, tx, f.customer.ID, f.key(), resumeOn, actor)
		require.NoError(t, err)
		assert.Len(t, skipped, 2)

		w, m := reread(t, tx, f.weekly.ID), reread(t, tx, f.fourWeek.ID)
		assert.True(t, w.NextOrderAt.Equal(m.NextOrderAt), "still one instant: %s vs %s", w.NextOrderAt, m.NextOrderAt)
		assert.WithinDuration(t, resumeOn, w.NextOrderAt, time.Second)
	})

	t.Run("each member's audit entry names the box", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newBoxFixture(t, tx)
		_, err := svc.SkipBox(ctx, tx, f.customer.ID, f.key(), f.at.AddDate(0, 0, 10), actor)
		require.NoError(t, err)

		for _, id := range []uuid.UUID{f.weekly.ID, f.fourWeek.ID} {
			var box bool
			var addr string
			require.NoError(t, tx.QueryRow(ctx, `
				SELECT (metadata->>'box')::bool, metadata->>'box_shipping_address_id' FROM audit_log
				WHERE resource_type = 'subscription' AND resource_id = $1 AND action = $2`,
				id, audit.AuditSubscriptionSkipped).Scan(&box, &addr))
			assert.True(t, box)
			assert.Equal(t, f.addr.ID.String(), addr)
		}
	})

	t.Run("a past-due member refuses the whole box and nothing is written", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newBoxFixture(t, tx)
		pastDue := boxMember(t, tx, f.customer.ID, f.addr.ID, f.at,
			domain.SubscriptionIntervalEvery30Days, 1, domain.SubscriptionStatusPastDue)

		_, err := svc.SkipBox(ctx, tx, f.customer.ID, f.key(), f.at.AddDate(0, 0, 10), actor)
		assert.ErrorIs(t, err, app.ErrSubscriptionNotSkippable)
		for _, id := range []uuid.UUID{f.weekly.ID, f.fourWeek.ID, pastDue.ID} {
			assert.True(t, reread(t, tx, id).NextOrderAt.Equal(f.at), "subscription %s untouched", id)
		}
	})

	t.Run("another customer's subscription at the same address and instant is never touched", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newBoxFixture(t, tx)
		stranger := testutil.CreateCustomer(t, tx)
		theirs := boxMember(t, tx, stranger.ID, f.addr.ID, f.at,
			domain.SubscriptionIntervalEvery7Days, 1, domain.SubscriptionStatusActive)

		skipped, err := svc.SkipBox(ctx, tx, f.customer.ID, f.key(), f.at.AddDate(0, 0, 10), actor)
		require.NoError(t, err)
		assert.Len(t, skipped, 2)
		assert.True(t, reread(t, tx, theirs.ID).NextOrderAt.Equal(f.at))
	})

	t.Run("a key matching nothing is not found", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newBoxFixture(t, tx)
		for _, key := range []domain.SubscriptionBoxKey{
			{ShippingAddressID: uuid.New(), NextOrderAt: f.at},
			{ShippingAddressID: f.addr.ID, NextOrderAt: f.at.Add(time.Hour)},
		} {
			_, err := svc.SkipBox(ctx, tx, f.customer.ID, key, f.at.AddDate(0, 0, 10), actor)
			assert.ErrorIs(t, err, app.ErrSubscriptionNotFound)
		}
	})
}

func TestPauseBox(t *testing.T) {
	ctx := context.Background()
	actor := testutil.TestActor()
	svc := newSubscriptionService()

	t.Run("pauses every member, each audited as part of the box", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newBoxFixture(t, tx)

		paused, err := svc.PauseBox(ctx, tx, f.customer.ID, f.key(), actor)
		require.NoError(t, err)
		assert.Len(t, paused, 2)
		for _, id := range []uuid.UUID{f.weekly.ID, f.fourWeek.ID} {
			assert.Equal(t, domain.SubscriptionStatusPaused, reread(t, tx, id).Status)
			var box bool
			require.NoError(t, tx.QueryRow(ctx, `
				SELECT (metadata->>'box')::bool FROM audit_log
				WHERE resource_type = 'subscription' AND resource_id = $1 AND action = $2`,
				id, audit.AuditSubscriptionPaused).Scan(&box))
			assert.True(t, box)
		}
	})

	t.Run("a past-due member refuses the whole box and nothing is written", func(t *testing.T) {
		// A past-due subscription has an unpaid charge; pausing it is not
		// something the per-row button allows either.
		tx := testutil.NewTestTx(t, testPool)
		f := newBoxFixture(t, tx)
		pastDue := boxMember(t, tx, f.customer.ID, f.addr.ID, f.at,
			domain.SubscriptionIntervalEvery30Days, 1, domain.SubscriptionStatusPastDue)

		_, err := svc.PauseBox(ctx, tx, f.customer.ID, f.key(), actor)
		assert.ErrorIs(t, err, app.ErrSubscriptionNotPausable)
		assert.Equal(t, domain.SubscriptionStatusActive, reread(t, tx, f.weekly.ID).Status)
		assert.Equal(t, domain.SubscriptionStatusActive, reread(t, tx, f.fourWeek.ID).Status)
		assert.Equal(t, domain.SubscriptionStatusPastDue, reread(t, tx, pastDue.ID).Status)
	})

	t.Run("a key matching nothing is not found", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newBoxFixture(t, tx)
		_, err := svc.PauseBox(ctx, tx, f.customer.ID,
			domain.SubscriptionBoxKey{ShippingAddressID: uuid.New(), NextOrderAt: f.at}, actor)
		assert.ErrorIs(t, err, app.ErrSubscriptionNotFound)
	})
}
