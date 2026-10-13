package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/platform/payments/paymentstest"
	"github.com/dukerupert/hiri/internal/store"
)

// A card the bank permanently refused was remembered on the subscription that
// found out. A card belongs to the customer, so their other subscriptions knew
// nothing: the next one to renew charged the dead card again — a network fine
// each time, and a card-failed email about a card the customer may already have
// replaced. The batch path charged it with nothing to avoid at all. Dead cards
// are now recorded against the customer, and every charge avoids them.

// deadCardProvider is a customer whose default card is pm_dead, with pm_good
// attached beside it, and whose first charge is refused as a lost card.
func deadCardProvider() *paymentstest.Provider {
	p := paymentstest.New().Then(paymentstest.Decline("lost_card"))
	p.DefaultPaymentMethod = "pm_dead"
	p.Attached = []payments.PaymentMethod{{ID: "pm_dead"}, {ID: "pm_good"}}
	return p
}

// chargedCards is the card on every charge request the provider saw.
func chargedCards(p *paymentstest.Provider) []string {
	var out []string
	for _, r := range p.Requests() {
		out = append(out, r.PaymentMethodID)
	}
	return out
}

func TestDeadCard_ASiblingsSoloRenewalAvoidsIt(t *testing.T) {
	ctx := context.Background()
	provider := deadCardProvider()
	svc, _ := newFailureRenewalService(provider)
	a, b := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(ctx, testPool, a.ID)
	require.ErrorIs(t, err, app.ErrRenewalPaymentDeclined)

	_, err = svc.RenewSubscription(ctx, testPool, b.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"pm_dead", "pm_good"}, chargedCards(provider),
		"the sibling charges the other card, never the one the bank refused")
}

func TestDeadCard_ASiblingsBatchRenewalAvoidsIt(t *testing.T) {
	ctx := context.Background()
	provider := deadCardProvider()
	svc, _ := newFailureRenewalService(provider)
	a, b := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(ctx, testPool, a.ID)
	require.ErrorIs(t, err, app.ErrRenewalPaymentDeclined)

	_, err = svc.RenewBatch(ctx, testPool, []uuid.UUID{a.ID, b.ID})
	require.NoError(t, err)
	assert.Equal(t, []string{"pm_dead", "pm_good"}, chargedCards(provider))
}

// Only the dead card on file: the batch charges nothing and walks the ladder,
// latched, the way the solo renewal of the subscription that found it does.
func TestDeadCard_ABatchWithNothingElseOnFileIsNotCharged(t *testing.T) {
	ctx := context.Background()
	provider := deadCardProvider()
	provider.Attached = []payments.PaymentMethod{{ID: "pm_dead"}}
	svc, enq := newFailureRenewalService(provider)
	a, b := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(ctx, testPool, a.ID)
	require.ErrorIs(t, err, app.ErrRenewalPaymentDeclined)

	_, err = svc.RenewBatch(ctx, testPool, []uuid.UUID{a.ID, b.ID})
	require.ErrorIs(t, err, app.ErrRenewalPaymentDeclined)
	assert.Equal(t, 1, len(provider.Requests()), "the dead card is not charged a second time")

	got := readSub(t, b.ID)
	assert.Equal(t, domain.SubscriptionStatusPastDue, got.Status)
	assert.Equal(t, 1, got.DunningAttempt())
	assert.True(t, got.DunningHardDeclined(), "it says no charge is coming until a new card")
	assert.Contains(t, enq.pastDueNotices(), renewalMail{SubscriptionIDs: []uuid.UUID{b.ID}, Stage: 1},
		"the customer is told, so they can add a card")
}

// What the bank said about the card is true whatever became of the
// subscription. One cancelled while the charge was out still leaves the card
// recorded for the customer's others.
func TestDeadCard_RecordedEvenWhenTheSubscriptionWasCancelledMeanwhile(t *testing.T) {
	ctx := context.Background()
	provider := deadCardProvider()
	svc, _ := newFailureRenewalService(provider)
	a, b := dueBox(t, domain.SubscriptionStatusActive)
	provider.OnCreate = func() {
		provider.OnCreate = nil
		require.NoError(t, store.Tx(ctx, testPool, cancelIt(a.ID)))
	}

	_, err := svc.RenewSubscription(ctx, testPool, a.ID)
	require.ErrorIs(t, err, app.ErrRenewalPaymentDeclined)
	require.Equal(t, domain.SubscriptionStatusCancelled, readSub(t, a.ID).Status)

	_, err = svc.RenewSubscription(ctx, testPool, b.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"pm_dead", "pm_good"}, chargedCards(provider))
}

// A soft decline is the card having a bad day, not a dead card. The sibling
// still tries it.
func TestDeadCard_ASoftDeclineIsNotRecorded(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New().Then(paymentstest.Decline("insufficient_funds"))
	provider.DefaultPaymentMethod = "pm_dead"
	provider.Attached = []payments.PaymentMethod{{ID: "pm_dead"}, {ID: "pm_good"}}
	svc, _ := newFailureRenewalService(provider)
	a, b := dueBox(t, domain.SubscriptionStatusActive)

	_, err := svc.RenewSubscription(ctx, testPool, a.ID)
	require.ErrorIs(t, err, app.ErrRenewalPaymentDeclined)

	_, err = svc.RenewSubscription(ctx, testPool, b.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"pm_dead", "pm_dead"}, chargedCards(provider))
}
