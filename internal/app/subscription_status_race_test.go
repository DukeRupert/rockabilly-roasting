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
	"github.com/dukerupert/hiri/internal/platform/payments/paymentstest"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// A status change read the status, checked it, then wrote the new one with
// nothing tying the write to what was read. Two changes at once — Resume in
// one tab and Cancel in another — both passed their checks, and the later
// write won: a subscription active again with cancelled_at set, the customer
// already emailed that nothing more would be charged, and the next renewal
// charging them. A status write now names the status it read and changes
// nothing if that is no longer the status.

// raceAgainst holds first's change open in its own transaction, starts second
// so that it blocks on the row, waits until Postgres shows it waiting on that
// lock, commits first, and returns second's result.
func raceAgainst(t *testing.T, first, second func(tx pgx.Tx) error) error {
	t.Helper()
	ctx := context.Background()
	held, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer held.Rollback(ctx) //nolint:errcheck
	require.NoError(t, first(held))

	done := make(chan error, 1)
	go func() { done <- store.Tx(ctx, testPool, second) }()

	require.Eventually(t, func() bool {
		var waiting int
		err := testPool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND datname = current_database()`).Scan(&waiting)
		return err == nil && waiting > 0
	}, 5*time.Second, 10*time.Millisecond, "the second change blocks on the first's row lock")

	require.NoError(t, held.Commit(ctx))
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the second change never finished")
		return nil
	}
}

func cancelIt(id uuid.UUID) func(pgx.Tx) error {
	return func(tx pgx.Tx) error {
		_, err := newSubscriptionService().CancelSubscription(context.Background(), tx, id, testutil.TestActor())
		return err
	}
}

func TestResumeRacingCancel_TheCancelStands(t *testing.T) {
	sub, _ := dueBox(t, domain.SubscriptionStatusPaused)
	err := raceAgainst(t, cancelIt(sub.ID), func(tx pgx.Tx) error {
		_, err := newSubscriptionService().ResumeSubscription(context.Background(), tx, sub.ID, testutil.TestActor())
		return err
	})
	assert.ErrorIs(t, err, app.ErrSubscriptionChanged)
	assert.Equal(t, domain.SubscriptionStatusCancelled, readSub(t, sub.ID).Status)
}

func TestPauseRacingCancel_TheCancelStands(t *testing.T) {
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)
	err := raceAgainst(t, cancelIt(sub.ID), func(tx pgx.Tx) error {
		_, err := newSubscriptionService().PauseSubscription(context.Background(), tx, sub.ID, nil, testutil.TestActor())
		return err
	})
	assert.ErrorIs(t, err, app.ErrSubscriptionChanged)
	assert.Equal(t, domain.SubscriptionStatusCancelled, readSub(t, sub.ID).Status)
}

// A declined renewal put the subscription past due with no check on what it
// was. A customer who cancelled while the charge was out came back past due,
// and the dunning ladder went on charging the card they had cancelled. The
// failure is now written only to a subscription still active or past due.
func TestDeclineAfterCancel_TheCancelStands(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New().Then(paymentstest.Decline("insufficient_funds"))
	svc, enq := newFailureRenewalService(provider)
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)
	provider.OnCreate = func() { require.NoError(t, store.Tx(ctx, testPool, cancelIt(sub.ID))) }

	_, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	assert.ErrorIs(t, err, app.ErrRenewalPaymentDeclined)

	got := readSub(t, sub.ID)
	assert.Equal(t, domain.SubscriptionStatusCancelled, got.Status)
	assert.Zero(t, got.DunningAttempt(), "no ladder for a cancelled subscription")
	assert.Empty(t, enq.pastDueNotices(), "no card-failed mail after a cancel")
}

// The last rung expires the subscription. One cancelled during that charge
// stays cancelled, and is not mailed that it has ended for non-payment.
func TestLastRungDeclineAfterCancel_TheCancelStands(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New().Then(paymentstest.Decline("insufficient_funds"))
	svc, enq := newFailureRenewalService(provider)
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)
	require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		return store.NewSubscriptionStore(nil).SetDunningRetry(ctx, tx, sub.ID,
			time.Now().Add(-time.Minute), app.MaxDunningAttempts-1)
	}))
	provider.OnCreate = func() { require.NoError(t, store.Tx(ctx, testPool, cancelIt(sub.ID))) }

	_, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	assert.ErrorIs(t, err, app.ErrRenewalPaymentDeclined)
	assert.Equal(t, domain.SubscriptionStatusCancelled, readSub(t, sub.ID).Status)
	assert.Empty(t, enq.endedMail())
}

// The card-decline webhook marks a subscription past due. One cancelled at the
// same moment stays cancelled, and the webhook hears "not active", which it
// already treats as nothing to do — never past due, which dunning would charge.
func TestMarkPastDueRacingCancel_TheCancelStands(t *testing.T) {
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)
	err := raceAgainst(t, cancelIt(sub.ID), func(tx pgx.Tx) error {
		_, err := newSubscriptionService().MarkPastDue(context.Background(), tx, sub.ID)
		return err
	})
	assert.ErrorIs(t, err, app.ErrSubscriptionNotActive)
	assert.Equal(t, domain.SubscriptionStatusCancelled, readSub(t, sub.ID).Status)
}
