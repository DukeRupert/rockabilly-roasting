package jobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// A renewal job that finds its subscription already renewed — a Retry queued
// behind a batch, or the batch behind a Retry — has nothing to do. Both workers
// cancel rather than letting River retry into a subscription that is simply
// not due, which would log a failure every backoff until the attempts ran out.

// renewedSubscriptions commits n active subscriptions for one customer at one
// address, next due in a month: what a renewal leaves behind.
func renewedSubscriptions(t *testing.T, n int) []uuid.UUID {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck

	subs := store.NewSubscriptionStore(nil)
	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	plan, err := subs.CreatePlan(ctx, tx, store.CreatePlanParams{
		Name: "Monthly", Interval: domain.SubscriptionIntervalEvery30Days, IntervalCount: 1, IsActive: true,
	})
	require.NoError(t, err)
	next := time.Now().AddDate(0, 0, 29)
	ids := make([]uuid.UUID, n)
	for i := range ids {
		product := testutil.CreateProduct(t, tx)
		variant := testutil.CreateVariant(t, tx, product.ID)
		sub, err := subs.Create(ctx, tx, store.CreateSubscriptionParams{
			CustomerID: customer.ID, PlanID: plan.ID, VariantID: variant.ID, Quantity: 1,
			Status: domain.SubscriptionStatusActive, ShippingAddressID: addr.ID,
			CurrentPeriodStart: time.Now(), CurrentPeriodEnd: next, NextOrderAt: next,
		})
		require.NoError(t, err)
		ids[i] = sub.ID
	}
	require.NoError(t, tx.Commit(ctx))
	return ids
}

// notDueRenewalService never reaches a payment provider: a subscription that
// is not due is refused before the charge.
func notDueRenewalService() *app.RenewalService {
	return app.NewRenewalService(
		store.NewSubscriptionStore(nil), store.NewOrderStore(nil), store.NewCustomerStore(),
		store.NewPricingStore(), store.NewShippingStore(), nil,
		audit.NewAuditWriter(), metrics.NewRegistry(),
	)
}

func assertCancelled(t *testing.T, err error) {
	t.Helper()
	var cancel *river.JobCancelError
	require.True(t, errors.As(err, &cancel), "cancelled, not left to retry: %v", err)
	assert.ErrorIs(t, err, app.ErrRenewalNotDue)
}

func TestSubscriptionRenewalWorker_CancelsWhenAlreadyRenewed(t *testing.T) {
	ids := renewedSubscriptions(t, 1)
	w := jobs.NewSubscriptionRenewalWorker(notDueRenewalService(), testPool(t), metrics.NewRegistry())
	err := w.Work(context.Background(), &river.Job[jobs.SubscriptionRenewalArgs]{
		JobRow: &rivertype.JobRow{ID: 1, Attempt: 1},
		Args:   jobs.SubscriptionRenewalArgs{SubscriptionID: ids[0]},
	})
	assertCancelled(t, err)
}

func TestBatchRenewalWorker_CancelsWhenAlreadyRenewed(t *testing.T) {
	// A batch of one goes through RenewSubscription, which refuses; a batch of
	// several drops every member and places nothing, which is not an error.
	ids := renewedSubscriptions(t, 1)
	w := jobs.NewBatchRenewalWorker(notDueRenewalService(), testPool(t), metrics.NewRegistry())
	err := w.Work(context.Background(), &river.Job[jobs.BatchRenewalArgs]{
		JobRow: &rivertype.JobRow{ID: 1, Attempt: 1},
		Args:   jobs.BatchRenewalArgs{SubscriptionIDs: ids},
	})
	assertCancelled(t, err)

	several := renewedSubscriptions(t, 2)
	assert.NoError(t, w.Work(context.Background(), &river.Job[jobs.BatchRenewalArgs]{
		JobRow: &rivertype.JobRow{ID: 1, Attempt: 1},
		Args:   jobs.BatchRenewalArgs{SubscriptionIDs: several},
	}), "nothing due, nothing placed, nothing to retry")
}
