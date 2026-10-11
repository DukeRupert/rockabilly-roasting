package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/payments/paymentstest"
)

// Critique item 4: the write after the charge did not re-check status.
//
// Phase 1 refuses anything not active or past due, but the charge is a
// network call wide, and a customer can cancel or pause inside it. Phase 3
// wrote the order and advanced the period regardless: a captured renewal for
// a cancelled subscription. Now the period advances only for a subscription
// still active or past due. Anything else rolls the write back and the charge
// is refunded at once, through item 1's path.

func TestRenewSubscription_AStatusChangeDuringTheChargeIsRefunded(t *testing.T) {
	for _, status := range []domain.SubscriptionStatus{
		domain.SubscriptionStatusCancelled,
		domain.SubscriptionStatusPaused,
	} {
		t.Run(string(status), func(t *testing.T) {
			ctx := context.Background()
			provider := paymentstest.New()
			svc, _ := newFailureRenewalService(provider)
			sub, _ := dueBox(t, domain.SubscriptionStatusActive)
			provider.OnCreate = func() { setStatus(t, testPool, sub.ID, status) }

			order, err := svc.RenewSubscription(ctx, testPool, sub.ID)
			assert.ErrorIs(t, err, app.ErrSubscriptionNotActive)
			assert.Nil(t, order)

			assert.Zero(t, customerOrderCount(t, sub.CustomerID), "no order for a subscription that ended mid-charge")
			require.Equal(t, 1, provider.ChargeCount())
			require.Len(t, provider.Refunds(), 1, "the money goes back")
			assert.Equal(t, provider.Charges()[0].ID, provider.Refunds()[0].PaymentIntentID)

			got := readSub(t, sub.ID)
			assert.Equal(t, status, got.Status, "the customer's change stands")
			assert.True(t, got.CurrentPeriodEnd.Equal(sub.CurrentPeriodEnd), "the period does not advance")
			assert.Nil(t, got.RenewalPaymentIntentID)
			assert.Equal(t, 1, auditCount(t, sub.ID, audit.AuditSubscriptionRenewalOrphanedCharge))
		})
	}
}

// A past-due subscription cancelled mid-charge stays cancelled: the restore to
// active that follows a recovered charge must come after the guard, not before.
func TestRenewSubscription_APastDueSubscriptionCancelledDuringTheChargeIsRefunded(t *testing.T) {
	provider := paymentstest.New()
	svc, _ := newFailureRenewalService(provider)
	sub, _ := dueBox(t, domain.SubscriptionStatusPastDue)
	provider.OnCreate = func() { setStatus(t, testPool, sub.ID, domain.SubscriptionStatusCancelled) }

	_, err := svc.RenewSubscription(context.Background(), testPool, sub.ID)
	assert.ErrorIs(t, err, app.ErrSubscriptionNotActive)
	assert.Equal(t, domain.SubscriptionStatusCancelled, readSub(t, sub.ID).Status, "not revived")
	assert.Len(t, provider.Refunds(), 1)
}

// A refund that fails leaves the charge on record and the job retrying, and
// the retry's read refunds it. It must not come back as "not active", which
// the worker cancels on.
func TestRenewSubscription_AStatusChangeWhoseRefundFailsIsRetried(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc, _ := newFailureRenewalService(provider)
	sub, _ := dueBox(t, domain.SubscriptionStatusActive)
	provider.OnCreate = func() { setStatus(t, testPool, sub.ID, domain.SubscriptionStatusCancelled) }
	provider.RefundErr = paymentstest.ErrTimeout

	_, err := svc.RenewSubscription(ctx, testPool, sub.ID)
	require.Error(t, err)
	assert.NotErrorIs(t, err, app.ErrSubscriptionNotActive, "retried, not cancelled")
	assert.NotNil(t, readSub(t, sub.ID).RenewalPaymentIntentID, "the charge stays on record")

	provider.OnCreate, provider.RefundErr = nil, nil
	_, err = svc.RenewSubscription(ctx, testPool, sub.ID)
	assert.ErrorIs(t, err, app.ErrSubscriptionNotActive)
	assert.Len(t, provider.Refunds(), 1)
	assert.Nil(t, readSub(t, sub.ID).RenewalPaymentIntentID)
}

// One member of a box cancelled during the charge: the charge was for the
// whole box, so it is refunded whole and nothing is written. The scheduler
// regroups what is left on its next run.
func TestRenewBatch_AMemberCancelledDuringTheChargeRefundsTheBox(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc, _ := newFailureRenewalService(provider)
	a, b := dueBox(t, domain.SubscriptionStatusActive)
	provider.OnCreate = func() { setStatus(t, testPool, b.ID, domain.SubscriptionStatusCancelled) }

	order, err := svc.RenewBatch(ctx, testPool, []uuid.UUID{a.ID, b.ID})
	assert.ErrorIs(t, err, app.ErrSubscriptionNotActive)
	assert.Nil(t, order)
	assert.Zero(t, customerOrderCount(t, a.CustomerID))
	require.Len(t, provider.Refunds(), 1)
	assert.True(t, readSub(t, a.ID).CurrentPeriodEnd.Equal(a.CurrentPeriodEnd), "the member still active did not advance either")
	assert.Nil(t, readSub(t, a.ID).RenewalPaymentIntentID)
	assert.Nil(t, readSub(t, b.ID).RenewalPaymentIntentID)
}
