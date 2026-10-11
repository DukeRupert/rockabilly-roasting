package app_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/payments/paymentstest"
)

// A renewal's create is its charge: OffSession sets Confirm. The claim and
// River's uniqueness stop a second job reaching the create; they do not cover
// the create itself being tried again after Stripe charged and the answer was
// lost. The idempotency key does — paymentstest keeps keys the way Stripe
// does, so these assert what the customer's statement would show.
//
// The key is the subscription, its period end, its dunning attempt and a
// generation that only a refund moves. The
// attempt is in it because the period does not move while a subscription is
// past due: without it, a Retry after a decline would replay the decline, or
// be refused for naming a different card.

// soloKey is a solo renewal's key at a dunning attempt. The last part is the
// key generation, which only a refund moves (renewal_orphan_test.go).
func soloKey(sub *domain.Subscription, attempt int) string {
	return fmt.Sprintf("renewal:%s:%d:%d:0", sub.ID, sub.CurrentPeriodEnd.Unix(), attempt)
}

func TestRenewSubscription_TheChargeCarriesAKeyForTheSubscriptionAndPeriod(t *testing.T) {
	provider := paymentstest.New()
	svc, _ := newFailureRenewalService(provider)
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(context.Background(), testPool, sub.ID)
	require.NoError(t, err)
	require.Len(t, provider.Requests(), 1)
	assert.Equal(t, soloKey(sub, 0), provider.Requests()[0].IdempotencyKey)
}

func TestRenewSubscription_ARetryInTheSamePeriodSendsTheSameKey(t *testing.T) {
	provider := paymentstest.New().Then(paymentstest.Fail(paymentstest.ErrTimeout))
	svc, _ := newFailureRenewalService(provider)
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(context.Background(), testPool, sub.ID)
	require.Error(t, err)
	// The first renewal has released its claim; this is River's retry.
	_, err = svc.RenewSubscription(context.Background(), testPool, sub.ID)
	require.NoError(t, err)

	reqs := provider.Requests()
	require.Len(t, reqs, 2)
	assert.NotEmpty(t, reqs[0].IdempotencyKey)
	assert.Equal(t, reqs[0].IdempotencyKey, reqs[1].IdempotencyKey)
}

// The case the key exists for. Stripe charged, the response never arrived,
// and the worker saw an error. Today that reads as a decline: the ladder
// moves, the customer is told their card failed, and the next attempt charges
// again.
func TestRenewSubscription_ATimeoutAfterStripeChargedDoesNotChargeAgain(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New().Then(paymentstest.ChargeThenFail(paymentstest.ErrTimeout))
	svc, enq := newFailureRenewalService(provider)
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	require.Error(t, err)
	assert.NotErrorIs(t, err, app.ErrRenewalPaymentDeclined, "a timeout says nothing about the card")
	assert.Empty(t, enq.pastDueNotices(), "and the customer is not told it failed")

	order, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	require.NoError(t, err)
	require.NotNil(t, order)
	require.Equal(t, 1, provider.ChargeCount(), "charged once")
	_, stored := orderLines(t, order.ID)
	require.NotNil(t, stored.StripePaymentIntentID)
	assert.Equal(t, provider.Charges()[0].ID, *stored.StripePaymentIntentID, "the order is for that charge")
}

func TestRenewSubscription_ATransportErrorDoesNotAdvanceTheLadder(t *testing.T) {
	provider := paymentstest.New().Then(paymentstest.Fail(paymentstest.ErrTimeout))
	svc, enq := newFailureRenewalService(provider)
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(context.Background(), testPool, sub.ID)
	require.Error(t, err)
	assert.ErrorIs(t, err, paymentstest.ErrTimeout)
	assert.NotErrorIs(t, err, app.ErrRenewalPaymentDeclined, "retryable, not a decline")

	got := readSub(t, sub.ID)
	assert.Equal(t, domain.SubscriptionStatusActive, got.Status)
	assert.Zero(t, got.DunningAttempt())
	assert.Empty(t, enq.pastDueNotices())
	assert.Zero(t, auditCount(t, sub.ID, audit.AuditSubscriptionFailed))
}

func TestRenewSubscription_ARetryAfterADeclineSendsANewKey(t *testing.T) {
	provider := paymentstest.New().Then(paymentstest.Decline("insufficient_funds"))
	svc, _ := newFailureRenewalService(provider)
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(context.Background(), testPool, sub.ID)
	require.ErrorIs(t, err, app.ErrRenewalPaymentDeclined)

	// The customer fixed their card and clicked Retry the same day.
	order, err := svc.RenewSubscription(context.Background(), testPool, sub.ID)
	require.NoError(t, err)
	require.NotNil(t, order, "a real second attempt, not the first decline replayed")

	reqs := provider.Requests()
	require.Len(t, reqs, 2)
	assert.Equal(t, soloKey(sub, 0), reqs[0].IdempotencyKey)
	assert.Equal(t, soloKey(sub, 1), reqs[1].IdempotencyKey)
	assert.Equal(t, 2, provider.Executed(), "the issuer was asked twice")
}

// Critique item 6 returns a lost dunning write for River to retry. The retry
// must not ask the issuer a second time: no decline was recorded, so the key
// is unchanged and Stripe answers from what it stored.
func TestRenewSubscription_RetryingAFailedDunningWriteDoesNotAskTheBankAgain(t *testing.T) {
	provider := paymentstest.New().Then(paymentstest.Decline("insufficient_funds"))
	svc, enq := newFailureRenewalService(provider)
	enq.failPastDue = 1
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(context.Background(), testPool, sub.ID)
	require.ErrorIs(t, err, errEnqueue)

	_, err = svc.RenewSubscription(context.Background(), testPool, sub.ID)
	assert.ErrorIs(t, err, app.ErrRenewalPaymentDeclined)
	assert.Equal(t, 1, provider.Executed(), "the decline was replayed, not re-asked")
	assert.Equal(t, 1, readSub(t, sub.ID).DunningAttempt(), "and recorded once")
}

func TestRenewBatch_TheKeyIsStableUnderMemberOrder(t *testing.T) {
	provider := paymentstest.New().Then(
		paymentstest.Fail(paymentstest.ErrTimeout),
		paymentstest.Fail(paymentstest.ErrTimeout),
	)
	svc, _ := newFailureRenewalService(provider)
	a, b := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewBatch(context.Background(), testPool, []uuid.UUID{a.ID, b.ID})
	require.Error(t, err)
	_, err = svc.RenewBatch(context.Background(), testPool, []uuid.UUID{b.ID, a.ID})
	require.Error(t, err)

	reqs := provider.Requests()
	require.Len(t, reqs, 2)
	assert.True(t, strings.HasPrefix(reqs[0].IdempotencyKey, "renewal-batch:"), reqs[0].IdempotencyKey)
	assert.LessOrEqual(t, len(reqs[0].IdempotencyKey), 255, "Stripe's limit")
	assert.Equal(t, reqs[0].IdempotencyKey, reqs[1].IdempotencyKey)
	assert.NotEqual(t, soloKey(a, 0), reqs[0].IdempotencyKey)
}

func TestRenewBatch_ATimeoutAfterStripeChargedDoesNotChargeAgain(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New().Then(paymentstest.ChargeThenFail(paymentstest.ErrTimeout))
	svc, _ := newFailureRenewalService(provider)
	a, b := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewBatch(ctx, testPool, []uuid.UUID{a.ID, b.ID})
	require.Error(t, err)
	assert.NotErrorIs(t, err, app.ErrRenewalPaymentDeclined)

	order, err := svc.RenewBatch(ctx, testPool, []uuid.UUID{a.ID, b.ID})
	require.NoError(t, err)
	require.NotNil(t, order)
	assert.Equal(t, 1, provider.ChargeCount())
	lines, _ := orderLines(t, order.ID)
	assert.Len(t, lines, 2)
}
