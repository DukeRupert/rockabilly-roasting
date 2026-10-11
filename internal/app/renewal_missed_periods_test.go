package app_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/payments/paymentstest"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// Critique item 2: missed periods were billed back to back.
//
// A renewal advanced the period from its old end, so a subscription more than
// one interval behind came out of its renewal still due, and the next
// scheduler tick charged it again — a weekly plan recovering from the
// two-week dunning ladder twice in two minutes, a weekly subscriber after a
// month-long worker outage four times in four minutes.
//
// Now a renewal more than one whole interval behind starts its new period
// from the renewal window it runs in: charged once, next due one interval
// out, the missed periods not billed and written into the audit entry. An
// on-time renewal still advances from its period end, so the schedule does
// not drift by the hours a renewal runs after its exact end.

// subscriptionsEnding commits one customer's subscriptions at one address on
// one plan, each with the given period end and due an hour ago.
func subscriptionsEnding(t *testing.T, interval domain.SubscriptionInterval, status domain.SubscriptionStatus, ends ...time.Time) []*domain.Subscription {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck

	customer := testutil.CreateCustomer(t, tx)
	_, err = tx.Exec(ctx, `UPDATE customers SET stripe_customer_id = $2 WHERE id = $1`,
		customer.ID, "cus_"+uuid.NewString())
	require.NoError(t, err)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	plan := planEvery(t, tx, interval)
	due := time.Now().Add(-time.Hour)

	subs := make([]*domain.Subscription, len(ends))
	var productIDs []uuid.UUID
	for i, end := range ends {
		product := testutil.CreateProduct(t, tx)
		productIDs = append(productIDs, product.ID)
		variant := testutil.CreateVariant(t, tx, product.ID)
		testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")
		sub, err := store.NewSubscriptionStore(nil).Create(ctx, tx, store.CreateSubscriptionParams{
			CustomerID:         customer.ID,
			PlanID:             plan.ID,
			VariantID:          variant.ID,
			Quantity:           1,
			Status:             status,
			ShippingAddressID:  addr.ID,
			CurrentPeriodStart: end.AddDate(0, 0, -30),
			CurrentPeriodEnd:   end,
			NextOrderAt:        due,
		})
		require.NoError(t, err)
		subs[i] = sub
	}
	require.NoError(t, tx.Commit(ctx))
	cleanupCommittedCustomer(t, customer.ID, productIDs, []uuid.UUID{plan.ID})
	return subs
}

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

// assertDueInDays checks a renewal left the subscription next due n calendar
// days after it ran — calendar days, as nextPeriodEnd counts them, so a DST
// change inside the period does not read as an hour's error.
func assertDueInDays(t *testing.T, sub *domain.Subscription, before, after time.Time, n int) {
	t.Helper()
	assert.False(t, sub.NextOrderAt.Before(before.AddDate(0, 0, n).Add(-time.Millisecond)),
		"next due %s is not before %s", sub.NextOrderAt, before.AddDate(0, 0, n))
	assert.False(t, sub.NextOrderAt.After(after.AddDate(0, 0, n)),
		"next due %s is not after %s", sub.NextOrderAt, after.AddDate(0, 0, n))
}

func TestRenewSubscription_ASubscriptionIntervalsOverdueRenewsOnce(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc, _ := newFailureRenewalService(provider)
	// Three 30-day periods have ended since this one did.
	sub := subscriptionsEnding(t, domain.SubscriptionIntervalEvery30Days, domain.SubscriptionStatusActive,
		time.Now().Add(-days(91)))[0]

	before := time.Now()
	order, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	after := time.Now()
	require.NoError(t, err)
	require.NotNil(t, order)

	got := readSub(t, sub.ID)
	assertDueInDays(t, got, before, after, 30)
	assert.True(t, got.CurrentPeriodEnd.Equal(got.NextOrderAt), "no anchor wired: due at the period end")

	_, err = svc.RenewSubscription(ctx, testPool, sub.ID)
	assert.ErrorIs(t, err, app.ErrRenewalNotDue, "not due again on the next tick")
	assert.Equal(t, 1, provider.ChargeCount(), "charged once, not once per missed period")
}

// The case the critique names first: a weekly plan back from two weeks of
// dunning.
func TestRenewSubscription_AWeeklyPlanRecoveredFromDunningIsNotDueAgainAtOnce(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc, _ := newFailureRenewalService(provider)
	sub := subscriptionsEnding(t, domain.SubscriptionIntervalEvery7Days, domain.SubscriptionStatusPastDue,
		time.Now().Add(-days(14)))[0]

	before := time.Now()
	_, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	after := time.Now()
	require.NoError(t, err)

	got := readSub(t, sub.ID)
	assert.Equal(t, domain.SubscriptionStatusActive, got.Status)
	assertDueInDays(t, got, before, after, 7)
}

// A weekly plan recovered on the second dunning retry, day 8: just past one
// interval behind, the boundary where advancing from the old end would leave
// it due again tomorrow.
func TestRenewSubscription_OneDayPastAnIntervalRestartsTheSchedule(t *testing.T) {
	svc, _ := newFailureRenewalService(paymentstest.New())
	sub := subscriptionsEnding(t, domain.SubscriptionIntervalEvery7Days, domain.SubscriptionStatusPastDue,
		time.Now().Add(-days(8)))[0]
	before := time.Now()
	_, err := svc.RenewSubscription(context.Background(), testPool, sub.ID)
	require.NoError(t, err)
	assertDueInDays(t, readSub(t, sub.ID), before, time.Now(), 7)
}

// An on-time renewal runs hours after its exact period end — the anchor puts
// the charge pre-dawn — and must still advance from that end. Advancing from
// the clock would push every renewal later by its own lateness.
func TestRenewSubscription_AnOnTimeRenewalKeepsItsSchedule(t *testing.T) {
	provider := paymentstest.New()
	svc, _ := newFailureRenewalService(provider)
	end := time.Now().Add(-13 * time.Hour).Truncate(time.Microsecond)
	sub := subscriptionsEnding(t, domain.SubscriptionIntervalEvery30Days, domain.SubscriptionStatusActive, end)[0]

	_, err := svc.RenewSubscription(context.Background(), testPool, sub.ID)
	require.NoError(t, err)

	got := readSub(t, sub.ID)
	assert.True(t, got.CurrentPeriodStart.Equal(end), "the new period starts where the old one ended")
	assert.True(t, got.CurrentPeriodEnd.Equal(end.AddDate(0, 0, 30)))
}

func TestRenewSubscription_SkippedPeriodsAreRecorded(t *testing.T) {
	ctx := context.Background()
	svc, _ := newFailureRenewalService(paymentstest.New())
	end := time.Now().Add(-days(91))
	sub := subscriptionsEnding(t, domain.SubscriptionIntervalEvery30Days, domain.SubscriptionStatusActive, end)[0]

	_, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	require.NoError(t, err)

	var raw []byte
	require.NoError(t, testPool.QueryRow(ctx, `SELECT metadata FROM audit_log
		WHERE resource_id = $1 AND action = $2`, sub.ID, audit.AuditSubscriptionRenewed).Scan(&raw))
	var meta map[string]any
	require.NoError(t, json.Unmarshal(raw, &meta))
	assert.EqualValues(t, 3, meta["periods_skipped"], "three periods ended unbilled")
	assert.NotEmpty(t, meta["previous_period_end"])
}

func TestRenewBatch_AnOverdueBoxRenewsOnceAndStaysOneBox(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc, _ := newFailureRenewalService(provider)
	end := time.Now().Add(-days(91))
	subs := subscriptionsEnding(t, domain.SubscriptionIntervalEvery30Days, domain.SubscriptionStatusActive, end, end)
	ids := []uuid.UUID{subs[0].ID, subs[1].ID}

	order, err := svc.RenewBatch(ctx, testPool, ids)
	require.NoError(t, err)
	require.NotNil(t, order)

	a, b := readSub(t, ids[0]), readSub(t, ids[1])
	assert.True(t, a.NextOrderAt.After(time.Now().Add(days(29))))
	assert.True(t, a.NextOrderAt.Equal(b.NextOrderAt), "still one box")

	again, err := svc.RenewBatch(ctx, testPool, ids)
	assert.NoError(t, err)
	assert.Nil(t, again, "nothing due on the next tick")
	assert.Equal(t, 1, provider.ChargeCount())
}

// A member back from dunning and a member on time renew in one batch. With
// the renewal anchor wired, the recovered member's fresh period starts from
// the same renewal window the on-time member's schedule lands on, so the two
// are still one box afterwards. Starting it from the raw clock would put it a
// day later: the anchor only rounds forward.
func TestRenewBatch_ARecoveredMemberStaysInItsBox(t *testing.T) {
	ctx := context.Background()
	// The window two hours ago, so the test cannot straddle it.
	window := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Hour)
	provider := paymentstest.New()
	svc, _ := newFailureRenewalService(provider)
	svc = svc.WithRenewalAnchor(time.UTC, window.Hour())

	subs := subscriptionsEnding(t, domain.SubscriptionIntervalEvery30Days, domain.SubscriptionStatusActive,
		window.Add(-time.Hour), // on time: its renewal window is this one
		window.Add(-days(61)),  // two periods behind
	)
	setStatus(t, testPool, subs[1].ID, domain.SubscriptionStatusPastDue)

	_, err := svc.RenewBatch(ctx, testPool, []uuid.UUID{subs[0].ID, subs[1].ID})
	require.NoError(t, err)

	onTime, recovered := readSub(t, subs[0].ID), readSub(t, subs[1].ID)
	assert.True(t, onTime.NextOrderAt.Equal(window.AddDate(0, 0, 30)), "on time: %s", onTime.NextOrderAt)
	assert.True(t, recovered.NextOrderAt.Equal(onTime.NextOrderAt),
		"one box afterwards: %s vs %s", recovered.NextOrderAt, onTime.NextOrderAt)
}
