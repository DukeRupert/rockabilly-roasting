package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// Resuming must book the next order at the next renewal window, not a fresh
// interval out.
//
// The live case: a weekly subscriber paused, resumed at 9:51am on a Sunday, and
// got nothing for eight days. Resume granted a whole new 7-day period and then
// rounded the anchor *up* a day, because 2am on the period-end Sunday had
// already passed. Nobody was told, and from the customer's side "resume" had
// simply done nothing.
func TestResumeSubscriptionBillsAtNextAnchor(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)

	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	subStore := store.NewSubscriptionStore(nil)
	subs := app.NewSubscriptionService(
		subStore,
		store.NewOrderStore(nil),
		audit.NewAuditWriter(),
		metrics.NewRegistry(),
	).WithRenewalAnchor(la, 2)

	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("RESUME-SCHED-12OZ"))
	plan, err := subStore.CreatePlan(ctx, tx, store.CreatePlanParams{
		Name:          "Weekly",
		Interval:      domain.SubscriptionIntervalEvery7Days,
		IntervalCount: 1,
		IsActive:      true,
	})
	require.NoError(t, err)

	sub := createTestSubscription(t, tx, customer.ID, plan.ID, variant.ID, addr.ID)
	_, err = subStore.UpdateStatus(ctx, tx, sub.ID, domain.SubscriptionStatusPaused)
	require.NoError(t, err)

	before := time.Now()
	resumed, err := subs.ResumeSubscription(ctx, tx, sub.ID, testutil.TestActor())
	require.NoError(t, err)

	// The screens quote ResumeOrderDate before the click, so a resume must book
	// what that helper names for the same instant. Not a promise to the
	// customer: a page rendered before the anchor hour and confirmed after it
	// gets the next window instead, which is why the card, the toast and the
	// email all read next_order_at rather than re-deriving this. Either side of
	// the call is accepted because the resume takes its own time.Now(), and the
	// anchor can roll between the two.
	after := time.Now()
	assert.True(t,
		resumed.NextOrderAt.Equal(subs.ResumeOrderDate(before)) ||
			resumed.NextOrderAt.Equal(subs.ResumeOrderDate(after)),
		"a resume must book the window ResumeOrderDate names for the instant it ran, got %s",
		resumed.NextOrderAt)

	// The property that matters regardless of what time the test runs: the next
	// renewal run, not the next cadence. The ceiling is a calendar day rather
	// than 24 hours, because a resume in the small hours before the clocks go
	// back is followed by a 25-hour day — which is also why nothing here or in
	// the copy promises "within 24 hours". Anchor behaviour across both DST
	// transitions is pinned exactly in TestAnchorRenewalTimeAcrossDST.
	assert.True(t, resumed.NextOrderAt.After(before), "the order must still be in the future")
	assert.True(t, resumed.NextOrderAt.Before(before.Add(25*time.Hour+time.Minute)),
		"a resume must not park the next order more than a day out, got %s", resumed.NextOrderAt)

	// Re-read: the struct is easy to patch in memory and miss the write.
	stored, err := subStore.GetByIDAsStaff(ctx, tx, sub.ID)
	require.NoError(t, err)
	assert.True(t, stored.NextOrderAt.Equal(resumed.NextOrderAt))
	assert.True(t, stored.CurrentPeriodEnd.Equal(resumed.NextOrderAt),
		"the period the resume opens must end when that order is placed, so the renewal walks the cadence on from it")
	assert.Equal(t, domain.SubscriptionStatusActive, stored.Status)
	assert.False(t, stored.RenewalBlocked(time.Now()))
}
