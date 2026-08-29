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

// Resuming a subscription has to clear ends_at.
//
// The renewal scheduler decides what to bill from three columns, and ends_at is
// the only one of them that no screen presents as consequential. Resume already
// cleared pause_until and reset the period, so a resumed subscription looked
// completely healthy while remaining permanently unbillable. That is the shape
// of the live incident this guards: three accounts, months of silence, no error
// anywhere.
func TestResumeSubscriptionClearsEndsAt(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	subs := app.NewSubscriptionService(
		store.NewSubscriptionStore(nil),
		store.NewOrderStore(nil),
		audit.NewAuditWriter(),
		metrics.NewRegistry(),
	)
	subStore := store.NewSubscriptionStore(nil)

	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("RESUME-12OZ"))
	plan, err := subStore.CreatePlan(ctx, tx, store.CreatePlanParams{
		Name:          "Monthly",
		Interval:      domain.SubscriptionIntervalEvery30Days,
		IntervalCount: 1,
		IsActive:      true,
	})
	require.NoError(t, err)

	sub := createTestSubscription(t, tx, customer.ID, plan.ID, variant.ID, addr.ID)

	// Put it in the state a dunning expiry leaves behind, then pause it, which
	// is the only status resume accepts.
	require.NoError(t, subStore.ExpireForDunning(ctx, tx, sub.ID))
	_, err = subStore.UpdateStatus(ctx, tx, sub.ID, domain.SubscriptionStatusPaused)
	require.NoError(t, err)

	before, err := subStore.GetByIDAsStaff(ctx, tx, sub.ID)
	require.NoError(t, err)
	require.NotNil(t, before.EndsAt, "precondition: the subscription carries an end date")

	resumed, err := subs.ResumeSubscription(ctx, tx, sub.ID, testutil.TestActor())
	require.NoError(t, err)

	assert.Nil(t, resumed.EndsAt, "the returned subscription must not still carry an end date")

	// Re-read: the returned struct is easy to patch in memory and miss the write.
	stored, err := subStore.GetByIDAsStaff(ctx, tx, sub.ID)
	require.NoError(t, err)
	assert.Nil(t, stored.EndsAt, "ends_at must be cleared in the database, not just on the struct")
	assert.Equal(t, domain.SubscriptionStatusActive, stored.Status)

	// The whole point: it is billable again.
	assert.False(t, stored.RenewalBlocked(time.Now()),
		"a resumed subscription must be visible to the renewal scheduler")
}

// ClearEndsAt is the counterpart to ExpireForDunning, which had no inverse until
// this incident — the application could put a subscription beyond the renewal
// scheduler's reach but never bring it back, so the only remedy was manual SQL.
func TestClearEndsAtIsIdempotent(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	subStore := store.NewSubscriptionStore(nil)

	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("CLEAR-12OZ"))
	plan, err := subStore.CreatePlan(ctx, tx, store.CreatePlanParams{
		Name:          "Monthly",
		Interval:      domain.SubscriptionIntervalEvery30Days,
		IntervalCount: 1,
		IsActive:      true,
	})
	require.NoError(t, err)
	sub := createTestSubscription(t, tx, customer.ID, plan.ID, variant.ID, addr.ID)

	// Already null — clearing must be a no-op rather than an error, so callers
	// can call it unconditionally.
	require.NoError(t, subStore.ClearEndsAt(ctx, tx, sub.ID))

	require.NoError(t, subStore.ExpireForDunning(ctx, tx, sub.ID))
	require.NoError(t, subStore.ClearEndsAt(ctx, tx, sub.ID))
	require.NoError(t, subStore.ClearEndsAt(ctx, tx, sub.ID))

	stored, err := subStore.GetByIDAsStaff(ctx, tx, sub.ID)
	require.NoError(t, err)
	assert.Nil(t, stored.EndsAt)
}
