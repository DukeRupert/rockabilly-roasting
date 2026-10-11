package jobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/payments/paymentstest"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// The renewal workers' Work on the paths that reach the card: a charge that
// lands, a decline, and a failure after the charge. renewal_not_due_test.go
// covers the stand-down; these are what the worker does with the money.
//
// They commit, as every worker test against the database does here: the
// renewal opens its own transactions. Fixtures are fresh per test.

// dueSubscriptions commits n active subscriptions for one customer at one
// address, priced, with a card on file, and due an hour ago: what the
// scheduler hands a worker.
func dueSubscriptions(t *testing.T, n int) []uuid.UUID {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool(t).Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck

	subs := store.NewSubscriptionStore(nil)
	customer := testutil.CreateCustomer(t, tx)
	_, err = tx.Exec(ctx, `UPDATE customers SET stripe_customer_id = $2 WHERE id = $1`,
		customer.ID, "cus_"+uuid.NewString())
	require.NoError(t, err)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	plan, err := subs.CreatePlan(ctx, tx, store.CreatePlanParams{
		Name: "Monthly", Interval: domain.SubscriptionIntervalEvery30Days, IntervalCount: 1, IsActive: true,
	})
	require.NoError(t, err)
	hourAgo := time.Now().Add(-time.Hour)
	ids := make([]uuid.UUID, n)
	for i := range ids {
		product := testutil.CreateProduct(t, tx)
		variant := testutil.CreateVariant(t, tx, product.ID)
		testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")
		sub, err := subs.Create(ctx, tx, store.CreateSubscriptionParams{
			CustomerID: customer.ID, PlanID: plan.ID, VariantID: variant.ID, Quantity: 1,
			Status: domain.SubscriptionStatusActive, ShippingAddressID: addr.ID,
			CurrentPeriodStart: hourAgo.AddDate(0, 0, -30), CurrentPeriodEnd: hourAgo, NextOrderAt: hourAgo,
		})
		require.NoError(t, err)
		ids[i] = sub.ID
	}
	require.NoError(t, tx.Commit(ctx))
	return ids
}

// failingReceipts is the renewal's mail, failing the receipt: the last write
// of the order's transaction, so failing it fails the write after the charge.
// The embedded interface is nil; a renewal sends nothing else on these paths.
type failingReceipts struct{ app.JobEnqueuer }

func (failingReceipts) EnqueueRenewalReceipt(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error {
	return errors.New("failingReceipts: enqueue failed")
}

func payingRenewalService(provider *paymentstest.Provider, enq app.JobEnqueuer) *app.RenewalService {
	svc := app.NewRenewalService(
		store.NewSubscriptionStore(nil), store.NewOrderStore(nil), store.NewCustomerStore(),
		store.NewPricingStore(), store.NewShippingStore(), provider,
		audit.NewAuditWriter(), metrics.NewRegistry(),
	)
	if enq != nil {
		svc = svc.WithJobEnqueuer(enq)
	}
	return svc
}

func soloJob(id uuid.UUID) *river.Job[jobs.SubscriptionRenewalArgs] {
	return &river.Job[jobs.SubscriptionRenewalArgs]{
		JobRow: &rivertype.JobRow{ID: 1, Attempt: 1},
		Args:   jobs.SubscriptionRenewalArgs{SubscriptionID: id},
	}
}

func batchJob(ids []uuid.UUID) *river.Job[jobs.BatchRenewalArgs] {
	return &river.Job[jobs.BatchRenewalArgs]{
		JobRow: &rivertype.JobRow{ID: 1, Attempt: 1},
		Args:   jobs.BatchRenewalArgs{SubscriptionIDs: ids},
	}
}

// renewalOrders is the distinct orders the subscriptions were renewed into.
func renewalOrders(t *testing.T, ids []uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := testPool(t).Query(context.Background(),
		`SELECT DISTINCT order_id FROM subscription_orders WHERE subscription_id = ANY($1)`, ids)
	require.NoError(t, err)
	orders, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	require.NoError(t, err)
	return orders
}

func subscriptionStatus(t *testing.T, id uuid.UUID) domain.SubscriptionStatus {
	t.Helper()
	var status string
	require.NoError(t, testPool(t).QueryRow(context.Background(),
		`SELECT status FROM subscriptions WHERE id = $1`, id).Scan(&status))
	return domain.SubscriptionStatus(status)
}

func TestSubscriptionRenewalWorker_PlacesTheOrder(t *testing.T) {
	ids := dueSubscriptions(t, 1)
	provider := paymentstest.New()
	w := jobs.NewSubscriptionRenewalWorker(payingRenewalService(provider, nil), testPool(t), metrics.NewRegistry())

	require.NoError(t, w.Work(context.Background(), soloJob(ids[0])))
	assert.Equal(t, 1, provider.ChargeCount())
	assert.Len(t, renewalOrders(t, ids), 1)
	assert.Equal(t, domain.SubscriptionStatusActive, subscriptionStatus(t, ids[0]))
}

func TestBatchRenewalWorker_PlacesOneOrder(t *testing.T) {
	ids := dueSubscriptions(t, 2)
	provider := paymentstest.New()
	w := jobs.NewBatchRenewalWorker(payingRenewalService(provider, nil), testPool(t), metrics.NewRegistry())

	require.NoError(t, w.Work(context.Background(), batchJob(ids)))
	assert.Equal(t, 1, provider.ChargeCount(), "one charge for the box")
	assert.Len(t, renewalOrders(t, ids), 1, "both members on one order")
}

// A decline is dunning's business, and the scheduler owns the next attempt:
// River retrying the job would charge ahead of the ladder.
func assertCancelledAsDeclined(t *testing.T, err error) {
	t.Helper()
	var cancel *river.JobCancelError
	require.True(t, errors.As(err, &cancel), "cancelled, not left to retry: %v", err)
	assert.ErrorIs(t, err, app.ErrRenewalPaymentDeclined)
}

func TestSubscriptionRenewalWorker_CancelsOnADecline(t *testing.T) {
	ids := dueSubscriptions(t, 1)
	provider := paymentstest.New().Then(paymentstest.Decline("insufficient_funds"))
	w := jobs.NewSubscriptionRenewalWorker(payingRenewalService(provider, nil), testPool(t), metrics.NewRegistry())

	assertCancelledAsDeclined(t, w.Work(context.Background(), soloJob(ids[0])))
	assert.Equal(t, domain.SubscriptionStatusPastDue, subscriptionStatus(t, ids[0]), "the ladder moved")
	assert.Empty(t, renewalOrders(t, ids))
}

func TestBatchRenewalWorker_CancelsOnADecline(t *testing.T) {
	ids := dueSubscriptions(t, 2)
	provider := paymentstest.New().Then(paymentstest.Decline("insufficient_funds"))
	w := jobs.NewBatchRenewalWorker(payingRenewalService(provider, nil), testPool(t), metrics.NewRegistry())

	assertCancelledAsDeclined(t, w.Work(context.Background(), batchJob(ids)))
	for _, id := range ids {
		assert.Equal(t, domain.SubscriptionStatusPastDue, subscriptionStatus(t, id))
	}
}

// A failure after the charge is not a decline, and the job must not be
// cancelled: nothing else would ever write the order the customer paid for.
func TestSubscriptionRenewalWorker_RetriesAWriteFailureAfterTheCharge(t *testing.T) {
	ids := dueSubscriptions(t, 1)
	provider := paymentstest.New()
	w := jobs.NewSubscriptionRenewalWorker(payingRenewalService(provider, failingReceipts{}), testPool(t), metrics.NewRegistry())

	err := w.Work(context.Background(), soloJob(ids[0]))
	require.Error(t, err)
	var cancel *river.JobCancelError
	assert.False(t, errors.As(err, &cancel), "left for River to retry: %v", err)
	assert.Equal(t, 1, provider.ChargeCount())
	assert.Empty(t, renewalOrders(t, ids))
}

func TestBatchRenewalWorker_RetriesAWriteFailureAfterTheCharge(t *testing.T) {
	ids := dueSubscriptions(t, 2)
	provider := paymentstest.New()
	w := jobs.NewBatchRenewalWorker(payingRenewalService(provider, failingReceipts{}), testPool(t), metrics.NewRegistry())

	err := w.Work(context.Background(), batchJob(ids))
	require.Error(t, err)
	var cancel *river.JobCancelError
	assert.False(t, errors.As(err, &cancel), "left for River to retry: %v", err)
	assert.Equal(t, 1, provider.ChargeCount())
	assert.Empty(t, renewalOrders(t, ids))
}

// failingPastDue is the renewal's mail, failing the past-due notice: the last
// write of a failed renewal's transaction.
type failingPastDue struct{ app.JobEnqueuer }

func (failingPastDue) EnqueuePastDueNotice(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, int) error {
	return errors.New("failingPastDue: enqueue failed")
}

// A decline whose dunning write was lost is not yet a recorded decline. The
// job has to come back and write it; cancelled, the subscription stays due
// and the scheduler charges it again on the next tick (critique item 6).
func TestSubscriptionRenewalWorker_RetriesAFailedDunningWrite(t *testing.T) {
	ids := dueSubscriptions(t, 1)
	provider := paymentstest.New().Then(paymentstest.Decline("insufficient_funds"))
	w := jobs.NewSubscriptionRenewalWorker(payingRenewalService(provider, failingPastDue{}), testPool(t), metrics.NewRegistry())

	err := w.Work(context.Background(), soloJob(ids[0]))
	require.Error(t, err)
	var cancel *river.JobCancelError
	assert.False(t, errors.As(err, &cancel), "left for River to retry: %v", err)
	assert.Equal(t, domain.SubscriptionStatusActive, subscriptionStatus(t, ids[0]))
}

func TestBatchRenewalWorker_RetriesAFailedDunningWrite(t *testing.T) {
	ids := dueSubscriptions(t, 2)
	provider := paymentstest.New().Then(paymentstest.Decline("insufficient_funds"))
	w := jobs.NewBatchRenewalWorker(payingRenewalService(provider, failingPastDue{}), testPool(t), metrics.NewRegistry())

	err := w.Work(context.Background(), batchJob(ids))
	require.Error(t, err)
	var cancel *river.JobCancelError
	assert.False(t, errors.As(err, &cancel), "left for River to retry: %v", err)
}

// A Stripe outage or a timeout says nothing about the card. The job retries,
// under the same idempotency key, rather than cancelling as a decline would.
func TestSubscriptionRenewalWorker_RetriesATransportError(t *testing.T) {
	ids := dueSubscriptions(t, 1)
	provider := paymentstest.New().Then(paymentstest.Fail(paymentstest.ErrTimeout))
	w := jobs.NewSubscriptionRenewalWorker(payingRenewalService(provider, nil), testPool(t), metrics.NewRegistry())

	err := w.Work(context.Background(), soloJob(ids[0]))
	require.Error(t, err)
	var cancel *river.JobCancelError
	assert.False(t, errors.As(err, &cancel), "left for River to retry: %v", err)
	assert.Equal(t, domain.SubscriptionStatusActive, subscriptionStatus(t, ids[0]), "the ladder did not move")
}
