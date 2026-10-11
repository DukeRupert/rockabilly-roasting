package app_test

import (
	"context"
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
)

// Critique item 1: a renewal can charge the card and write no order.
//
// The charge and the order are two steps with a network call between them.
// When the second fails, the money has moved and nothing says so. The renewal
// now writes the intent down on its subscriptions the moment Stripe answers,
// and the write that follows runs detached from the job's deadline. A retry
// that finds a charge written down finishes it — the order is placed on that
// charge — rather than charging again; one that finds the subscription no
// longer to be renewed refunds it.
//
// The idempotency key covers a retry inside 24 hours on its own. These expire
// the keys where it matters, so they pin what holds after that.

func pendingIntent(t *testing.T, id uuid.UUID) *string {
	t.Helper()
	return readSub(t, id).RenewalPaymentIntentID
}

func TestRenewSubscription_AChargeIsRecordedBeforeTheOrderIsWritten(t *testing.T) {
	provider := paymentstest.New()
	svc, enq := newFailureRenewalService(provider)
	enq.failReceipts = 1
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(context.Background(), testPool, sub.ID)
	require.Error(t, err)
	require.Equal(t, 1, provider.ChargeCount())

	got := pendingIntent(t, sub.ID)
	require.NotNil(t, got, "the charge is on record though the order is not")
	assert.Equal(t, provider.Charges()[0].ID, *got)
	assert.Equal(t, 1, auditCount(t, sub.ID, audit.AuditSubscriptionRenewalCharged))
}

func TestRenewSubscription_AWriteFailureAfterTheChargeIsFinishedOnRetry(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc, enq := newFailureRenewalService(provider)
	enq.failReceipts = 1
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	require.Error(t, err)

	// A day later: River's backoff, or a scheduler tick in a new uniqueness
	// period. Stripe has forgotten the key.
	provider.ExpireKeys()
	order, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	require.NoError(t, err)
	require.NotNil(t, order)

	assert.Equal(t, 1, provider.ChargeCount(), "the retry finished the first charge")
	_, stored := orderLines(t, order.ID)
	require.NotNil(t, stored.StripePaymentIntentID)
	assert.Equal(t, provider.Charges()[0].ID, *stored.StripePaymentIntentID)
	assert.Nil(t, pendingIntent(t, sub.ID), "settled by the order")
	assert.Empty(t, provider.Refunds())
}

// River's job timeout is a minute, and the Stripe call is the slow part. A
// deadline that fires during the charge used to take the order's write down
// with it.
func TestRenewSubscription_AJobTimeoutDuringTheChargeStillWritesTheOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := paymentstest.New()
	provider.OnCreate = cancel
	svc, _ := newFailureRenewalService(provider)
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	order, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	require.NoError(t, err)
	require.NotNil(t, order)
	assert.Equal(t, 1, provider.ChargeCount())
	assert.Equal(t, 1, customerOrderCount(t, sub.CustomerID))
	assert.Nil(t, pendingIntent(t, sub.ID))
}

func TestRenewSubscription_AChargeForACancelledSubscriptionIsRefunded(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc, enq := newFailureRenewalService(provider)
	enq.failReceipts = 1
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	require.Error(t, err)
	charge := provider.Charges()[0]

	// The customer cancelled before the retry.
	setStatus(t, testPool, sub.ID, domain.SubscriptionStatusCancelled)
	_, err = svc.RenewSubscription(ctx, testPool, sub.ID)
	assert.ErrorIs(t, err, app.ErrSubscriptionNotActive)

	refunds := provider.Refunds()
	require.Len(t, refunds, 1, "no order will be written, so the money goes back")
	assert.Equal(t, charge.ID, refunds[0].PaymentIntentID)
	assert.Equal(t, "renewal-refund:"+charge.ID, refunds[0].IdempotencyKey)
	assert.Nil(t, pendingIntent(t, sub.ID))
	assert.Equal(t, 1, auditCount(t, sub.ID, audit.AuditSubscriptionRenewalOrphanedCharge))
	assert.Zero(t, customerOrderCount(t, sub.CustomerID))
}

// A refund that fails leaves the charge on record and the job retrying it. It
// must not come back as "not active", which the worker cancels on.
func TestRenewSubscription_ARefundThatFailsKeepsTheChargeOnRecord(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc, enq := newFailureRenewalService(provider)
	enq.failReceipts = 1
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	require.Error(t, err)
	setStatus(t, testPool, sub.ID, domain.SubscriptionStatusCancelled)
	provider.RefundErr = paymentstest.ErrTimeout

	_, err = svc.RenewSubscription(ctx, testPool, sub.ID)
	require.Error(t, err)
	assert.NotErrorIs(t, err, app.ErrSubscriptionNotActive, "retried, not cancelled")
	assert.NotNil(t, pendingIntent(t, sub.ID))
	assert.Zero(t, auditCount(t, sub.ID, audit.AuditSubscriptionRenewalOrphanedCharge))
}

// The charge was for one total and the renewal now prices another — a price
// changed between the failed write and its retry. The charge is refunded
// rather than written against an order it does not match, and the renewal
// charges afresh at once.
//
// The fresh charge cannot reuse the refunded charge's key. Stripe keeps a key
// for 24 hours and replays its first response verbatim, and that response
// says succeeded: a refund followed by a create with the same key and the same
// parameters — the price back where it was — would get the refunded intent
// back, and the order would be written on money already given back. So every
// refund moves the key on.
func TestRenewSubscription_AnOrderIsNeverWrittenOnARefundedCharge(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc, enq := newFailureRenewalService(provider)
	enq.failReceipts = 2
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)
	movePrice := func(by int) {
		_, err := testPool.Exec(ctx, `
			UPDATE prices SET amount = amount + $2
			WHERE price_set_id = (SELECT id FROM price_sets WHERE variant_id = $1)`, sub.VariantID, by)
		require.NoError(t, err)
	}

	// Charged; the order's write fails.
	_, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	require.Error(t, err)

	// The price goes up: that charge is refunded, a new one made, and its
	// write fails too.
	movePrice(500)
	_, err = svc.RenewSubscription(ctx, testPool, sub.ID)
	require.Error(t, err)
	assert.NotErrorIs(t, err, paymentstest.ErrIdempotencyMismatch, "the fresh charge has a fresh key")
	require.Equal(t, 2, provider.ChargeCount())
	require.Len(t, provider.Refunds(), 1)

	// And back down, to the first charge's amount and parameters.
	movePrice(-500)
	order, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	require.NoError(t, err)
	require.NotNil(t, order)

	_, stored := orderLines(t, order.ID)
	require.NotNil(t, stored.StripePaymentIntentID)
	for _, refund := range provider.Refunds() {
		assert.NotEqual(t, refund.PaymentIntentID, *stored.StripePaymentIntentID,
			"the order is not on a refunded charge")
	}
	charges := provider.Charges()
	assert.Equal(t, charges[len(charges)-1].ID, *stored.StripePaymentIntentID)
	assert.Equal(t, int64(stored.Total), charges[len(charges)-1].AmountCents)
}

// A refund that went through and lost its response leaves the charge on
// record, already given back. The retry must finish the refund, not write an
// order on it — even when the price has come back to that charge's amount.
func TestRenewSubscription_AChargeBeingRefundedIsNeverReused(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc, enq := newFailureRenewalService(provider)
	enq.failReceipts = 1
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	require.Error(t, err)
	first := provider.Charges()[0]

	_, err = testPool.Exec(ctx, `
		UPDATE prices SET amount = amount + 500
		WHERE price_set_id = (SELECT id FROM price_sets WHERE variant_id = $1)`, sub.VariantID)
	require.NoError(t, err)
	provider.LoseRefundResponses = true
	_, err = svc.RenewSubscription(ctx, testPool, sub.ID)
	require.ErrorIs(t, err, paymentstest.ErrTimeout)
	require.Len(t, provider.Refunds(), 1, "the refund went through")

	_, err = testPool.Exec(ctx, `
		UPDATE prices SET amount = amount - 500
		WHERE price_set_id = (SELECT id FROM price_sets WHERE variant_id = $1)`, sub.VariantID)
	require.NoError(t, err)
	provider.LoseRefundResponses = false

	order, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	require.NoError(t, err)
	_, stored := orderLines(t, order.ID)
	require.NotNil(t, stored.StripePaymentIntentID)
	assert.NotEqual(t, first.ID, *stored.StripePaymentIntentID, "not the refunded charge")
	assert.Equal(t, 2, provider.ChargeCount())
	assert.Nil(t, pendingIntent(t, sub.ID))
	assert.Equal(t, 1, auditCount(t, sub.ID, audit.AuditSubscriptionRenewalOrphanedCharge))
}

// A lost refund response, then a lost charge response: the generation must move
// once, in the database and in memory alike, or the retry charges a third time.
func TestRenewSubscription_LostResponsesAfterARefundDoNotChargeAThirdTime(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc, enq := newFailureRenewalService(provider)
	enq.failReceipts = 1
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	require.Error(t, err)
	_, err = testPool.Exec(ctx, `UPDATE prices SET amount = amount + 500
		WHERE price_set_id = (SELECT id FROM price_sets WHERE variant_id = $1)`, sub.VariantID)
	require.NoError(t, err)
	provider.LoseRefundResponses = true
	_, err = svc.RenewSubscription(ctx, testPool, sub.ID)
	require.ErrorIs(t, err, paymentstest.ErrTimeout)

	provider.LoseRefundResponses = false
	provider.Then(paymentstest.ChargeThenFail(paymentstest.ErrTimeout))
	_, err = svc.RenewSubscription(ctx, testPool, sub.ID)
	require.ErrorIs(t, err, paymentstest.ErrTimeout)

	order, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	require.NoError(t, err)
	require.NotNil(t, order)
	assert.Equal(t, 2, provider.Executed(), "the first charge and its replacement, nothing more")
}

// Skipped while the charge waited for its order: not due any more, so the
// charge goes back.
func TestRenewSubscription_AChargeForASkippedSubscriptionIsRefunded(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc, enq := newFailureRenewalService(provider)
	enq.failReceipts = 1
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	require.Error(t, err)
	_, err = testPool.Exec(ctx, `UPDATE subscriptions SET next_order_at = now() + interval '29 days' WHERE id = $1`, sub.ID)
	require.NoError(t, err)

	_, err = svc.RenewSubscription(ctx, testPool, sub.ID)
	assert.ErrorIs(t, err, app.ErrRenewalNotDue)
	require.Len(t, provider.Refunds(), 1)
	assert.Nil(t, pendingIntent(t, sub.ID))
}

func TestRenewBatch_AWriteFailureAfterTheChargeIsFinishedOnRetry(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc, enq := newFailureRenewalService(provider)
	enq.failReceipts = 1
	a, b := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewBatch(ctx, testPool, []uuid.UUID{a.ID, b.ID})
	require.Error(t, err)
	charge := provider.Charges()[0]
	for _, id := range []uuid.UUID{a.ID, b.ID} {
		got := pendingIntent(t, id)
		require.NotNil(t, got)
		assert.Equal(t, charge.ID, *got, "the box's one charge, on every member")
	}

	provider.ExpireKeys()
	order, err := svc.RenewBatch(ctx, testPool, []uuid.UUID{a.ID, b.ID})
	require.NoError(t, err)
	require.NotNil(t, order)
	assert.Equal(t, 1, provider.ChargeCount())
	lines, _ := orderLines(t, order.ID)
	assert.Len(t, lines, 2)
	assert.Nil(t, pendingIntent(t, a.ID))
	assert.Nil(t, pendingIntent(t, b.ID))
}

// A member of a charged box picks up a dead card before the retry and drops out
// of the batch. The charge covered it, so it is refunded whole, and the rest
// is charged on its own.
func TestRenewBatch_AMemberDroppedAfterTheChargeRefundsTheBox(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc, enq := newFailureRenewalService(provider)
	enq.failReceipts = 1
	a, b := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewBatch(ctx, testPool, []uuid.UUID{a.ID, b.ID})
	require.Error(t, err)
	charge := provider.Charges()[0]

	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	subs := store.NewSubscriptionStore(nil)
	require.NoError(t, subs.SetDunningRetry(ctx, tx, b.ID, time.Now().Add(-time.Minute), 1))
	require.NoError(t, subs.SetDunningHardDecline(ctx, tx, b.ID, "stolen_card", []string{"pm_old"}))
	require.NoError(t, tx.Commit(ctx))

	order, err := svc.RenewBatch(ctx, testPool, []uuid.UUID{a.ID, b.ID})
	require.NoError(t, err)
	require.NotNil(t, order)
	require.Len(t, provider.Refunds(), 1)
	assert.Equal(t, charge.ID, provider.Refunds()[0].PaymentIntentID)
	lines, _ := orderLines(t, order.ID)
	assert.Len(t, lines, 1, "only the member still in the box")
	assert.Equal(t, 2, provider.ChargeCount())
	assert.Nil(t, pendingIntent(t, b.ID), "settled on the member that left too")
	var reason string
	require.NoError(t, testPool.QueryRow(ctx, `SELECT metadata->>'reason' FROM audit_log
		WHERE resource_id = $1 AND action = $2`, b.ID, audit.AuditSubscriptionRenewalOrphanedCharge).Scan(&reason))
	assert.Equal(t, "box_changed", reason, "refunded because the member left, not for its amount")
}

// One member of a charged box is cancelled before the retry. The charge was
// for the whole box, so it is refunded whole and the rest renew afresh.
func TestRenewBatch_AMemberCancelledAfterTheChargeRefundsTheBox(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc, enq := newFailureRenewalService(provider)
	enq.failReceipts = 1
	a, b := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewBatch(ctx, testPool, []uuid.UUID{a.ID, b.ID})
	require.Error(t, err)
	charge := provider.Charges()[0]

	setStatus(t, testPool, b.ID, domain.SubscriptionStatusCancelled)
	_, err = svc.RenewBatch(ctx, testPool, []uuid.UUID{a.ID, b.ID})
	assert.ErrorIs(t, err, app.ErrSubscriptionNotActive)

	require.Len(t, provider.Refunds(), 1)
	assert.Equal(t, charge.ID, provider.Refunds()[0].PaymentIntentID)
	assert.Nil(t, pendingIntent(t, a.ID), "settled on every member it covered")
	assert.Nil(t, pendingIntent(t, b.ID))
	assert.Equal(t, 1, auditCount(t, a.ID, audit.AuditSubscriptionRenewalOrphanedCharge))
	assert.Equal(t, 1, auditCount(t, b.ID, audit.AuditSubscriptionRenewalOrphanedCharge))

	// What is left of the box renews on its own, with a charge of its own.
	order, err := svc.RenewSubscription(ctx, testPool, a.ID)
	require.NoError(t, err)
	require.NotNil(t, order)
	assert.Equal(t, 2, provider.ChargeCount())
}
