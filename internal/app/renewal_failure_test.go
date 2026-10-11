package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/payments/paymentstest"
	"github.com/dukerupert/hiri/internal/store"
)

// The renewal's failure paths, driven through the charge against a fake
// provider. recordRenewalFailure runs here for real: the dunning story tests
// in renewal_dunning_test.go decide the verdict as a pure value, and these are
// what join that verdict to the writes that carry it out.
//
// These commit, like renewal_claim_test.go: RenewSubscription opens its own
// transactions, so there is no caller transaction to roll back. Every fixture
// is fresh per test and every assertion is scoped by its ids.

// renewalMail is one past-due notice or ended mail the enqueuer saw.
type renewalMail struct {
	SubscriptionID uuid.UUID
	Stage          int
}

// renewalEnqueuer records the mail a renewal sends and fails on demand. A
// failed enqueue fails its transaction, so it rolls back whole — phase 3 after
// the order insert, or the dunning write after the ladder moved.
type renewalEnqueuer struct {
	fakeEnqueuer

	mu       sync.Mutex
	receipts []uuid.UUID
	pastDue  []renewalMail
	ended    []uuid.UUID

	// failReceipts fails the next n renewal receipts.
	failReceipts int
	// failPastDue fails the next n past-due notices.
	failPastDue int
}

var errEnqueue = errors.New("renewalEnqueuer: enqueue failed")

func (e *renewalEnqueuer) EnqueueRenewalReceipt(_ context.Context, _ pgx.Tx, orderID, _ uuid.UUID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failReceipts > 0 {
		e.failReceipts--
		return errEnqueue
	}
	e.receipts = append(e.receipts, orderID)
	return nil
}

func (e *renewalEnqueuer) EnqueuePastDueNotice(_ context.Context, _ pgx.Tx, subscriptionID, _ uuid.UUID, stage int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failPastDue > 0 {
		e.failPastDue--
		return errEnqueue
	}
	e.pastDue = append(e.pastDue, renewalMail{SubscriptionID: subscriptionID, Stage: stage})
	return nil
}

func (e *renewalEnqueuer) EnqueueSubscriptionEnded(_ context.Context, _ pgx.Tx, subscriptionID, _ uuid.UUID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ended = append(e.ended, subscriptionID)
	return nil
}

func (e *renewalEnqueuer) endedMail() []uuid.UUID {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]uuid.UUID(nil), e.ended...)
}

func (e *renewalEnqueuer) pastDueNotices() []renewalMail {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]renewalMail(nil), e.pastDue...)
}

// newFailureRenewalService is the claim tests' service with mail wired, so a
// failure path has somewhere to send its notice.
func newFailureRenewalService(provider *paymentstest.Provider) (*app.RenewalService, *renewalEnqueuer) {
	enq := &renewalEnqueuer{}
	return newClaimRenewalService(provider).WithJobEnqueuer(enq), enq
}

// customerOrderCount is how many orders a customer has. Scoped by customer
// because these tests commit and the table is shared.
func customerOrderCount(t *testing.T, customerID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM orders WHERE customer_id = $1`, customerID).Scan(&n))
	return n
}

// auditCount is how many entries of action name the resource.
func auditCount(t *testing.T, resourceID uuid.UUID, action string) int {
	t.Helper()
	var n int
	require.NoError(t, testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE resource_id = $1 AND action = $2`,
		resourceID, action).Scan(&n))
	return n
}

// assertRetryAround checks a dunning retry was scheduled wait after the
// renewal ran. No anchor is wired in these tests, so it is the raw instant,
// bracketed by clock reads either side of the call.
func assertRetryAround(t *testing.T, sub *domain.Subscription, before, after time.Time, wait time.Duration) {
	t.Helper()
	assert.False(t, sub.NextOrderAt.Before(before.Add(wait).Add(-time.Millisecond)),
		"next attempt %s is not before %s", sub.NextOrderAt, before.Add(wait))
	assert.False(t, sub.NextOrderAt.After(after.Add(wait)),
		"next attempt %s is not after %s", sub.NextOrderAt, after.Add(wait))
}

func TestRenewSubscription_ADeclineAdvancesTheLadder(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New().Then(paymentstest.Decline("insufficient_funds"))
	svc, enq := newFailureRenewalService(provider)
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	before := time.Now()
	order, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	after := time.Now()

	assert.ErrorIs(t, err, app.ErrRenewalPaymentDeclined)
	assert.Nil(t, order)
	assert.Len(t, provider.Requests(), 1)
	assert.Zero(t, provider.ChargeCount())
	assert.Zero(t, customerOrderCount(t, sub.CustomerID), "a decline places no order")

	got := readSub(t, sub.ID)
	assert.Equal(t, domain.SubscriptionStatusPastDue, got.Status)
	assert.Equal(t, 1, got.DunningAttempt())
	assert.False(t, got.DunningHardDeclined(), "insufficient funds is worth retrying")
	assert.True(t, got.CurrentPeriodEnd.Equal(sub.CurrentPeriodEnd), "the period does not advance on a decline")
	assertRetryAround(t, got, before, after, 72*time.Hour)

	assert.Equal(t, []renewalMail{{SubscriptionID: sub.ID, Stage: 1}}, enq.pastDueNotices(),
		"the first rung tells the customer")
	assert.Equal(t, 1, auditCount(t, sub.ID, audit.AuditSubscriptionFailed))
}

func TestRenewSubscription_AHardDeclineLatchesTheCard(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New().Then(paymentstest.Decline("stolen_card"))
	svc, _ := newFailureRenewalService(provider)
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	require.ErrorIs(t, err, app.ErrRenewalPaymentDeclined)

	got := readSub(t, sub.ID)
	assert.True(t, got.DunningHardDeclined())
	assert.Equal(t, "stolen_card", got.DunningDeclineCode())
	assert.Equal(t, []string{paymentstest.DefaultCard}, got.DunningDeadPaymentMethods())

	// The next rung, with the same card still all they have: the ladder walks
	// on and Stripe is not asked again.
	_, err = svc.RenewSubscription(ctx, testPool, sub.ID)
	assert.ErrorIs(t, err, app.ErrRenewalPaymentDeclined)
	assert.Len(t, provider.Requests(), 1, "a dead card is never charged again")
	assert.Equal(t, 2, readSub(t, sub.ID).DunningAttempt())
}

func TestRenewSubscription_NoCardOnFileAdvancesTheLadderWithoutCharging(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	provider.DefaultPaymentMethod = ""
	svc, enq := newFailureRenewalService(provider)
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	assert.ErrorIs(t, err, app.ErrRenewalPaymentDeclined)
	assert.Empty(t, provider.Requests())

	got := readSub(t, sub.ID)
	assert.Equal(t, domain.SubscriptionStatusPastDue, got.Status)
	assert.Equal(t, 1, got.DunningAttempt())
	assert.Equal(t, []renewalMail{{SubscriptionID: sub.ID, Stage: 1}}, enq.pastDueNotices())
}

func TestRenewSubscription_TheLastRungExpiresTheSubscription(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New().Then(paymentstest.Decline("insufficient_funds"))
	svc, enq := newFailureRenewalService(provider)
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	// Four failed attempts behind it: this charge is the last.
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, store.NewSubscriptionStore(nil).SetDunningRetry(ctx, tx, sub.ID,
		time.Now().Add(-time.Minute), app.MaxDunningAttempts-1))
	require.NoError(t, tx.Commit(ctx))

	_, err = svc.RenewSubscription(ctx, testPool, sub.ID)
	assert.ErrorIs(t, err, app.ErrRenewalPaymentDeclined)

	assert.Equal(t, domain.SubscriptionStatusExpired, readSub(t, sub.ID).Status)
	assert.Equal(t, []uuid.UUID{sub.ID}, enq.endedMail(), "the customer is told it has ended")
	assert.Empty(t, enq.pastDueNotices())
	assert.Equal(t, 1, auditCount(t, sub.ID, audit.AuditSubscriptionExpired))
}

// One decline in a box runs one ladder per member, and each member mails its
// own notice. That is critique item 5, pinned here as today's behaviour so the
// change to one ladder per customer is a deliberate one.
func TestRenewBatch_ADeclineAdvancesEveryMember(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New().Then(paymentstest.Decline("insufficient_funds"))
	svc, enq := newFailureRenewalService(provider)
	a, b := dueBox(t, domain.SubscriptionStatusActive)

	order, err := svc.RenewBatch(ctx, testPool, []uuid.UUID{a.ID, b.ID})
	assert.ErrorIs(t, err, app.ErrRenewalPaymentDeclined)
	assert.Nil(t, order)
	assert.Len(t, provider.Requests(), 1, "one charge attempt for the box")
	assert.Zero(t, customerOrderCount(t, a.CustomerID))

	for _, id := range []uuid.UUID{a.ID, b.ID} {
		got := readSub(t, id)
		assert.Equal(t, domain.SubscriptionStatusPastDue, got.Status)
		assert.Equal(t, 1, got.DunningAttempt())
	}
	assert.ElementsMatch(t, []renewalMail{{a.ID, 1}, {b.ID, 1}}, enq.pastDueNotices(),
		"one notice per member (critique item 5)")
}

// What a failure after the charge does today: the order is not written, and
// the error is one River retries. Critique item 1 is what that retry then
// does with the card.
func TestRenewSubscription_AWriteFailureAfterTheChargeIsReturnedForRetry(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc, enq := newFailureRenewalService(provider)
	enq.failReceipts = 1
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	order, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	require.Error(t, err)
	assert.Nil(t, order)
	assert.NotErrorIs(t, err, app.ErrRenewalPaymentDeclined, "not a decline: the card was charged")
	assert.Equal(t, 1, provider.ChargeCount())
	assert.Zero(t, customerOrderCount(t, sub.CustomerID), "the write phase rolled back whole")
	assert.True(t, readSub(t, sub.ID).NextOrderAt.Equal(sub.NextOrderAt), "the period did not advance")
}
