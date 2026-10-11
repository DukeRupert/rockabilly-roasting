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
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/platform/payments/paymentstest"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// A batch renewal and a customer's or staff Retry are different job kinds, so
// River's uniqueness cannot stop one riding on the other. After a batch decline
// every member is past due and every row offers Retry; a click while the
// batch's own retry is queued used to charge that member twice.
//
// Two guards, one for each ordering. Sequential: a renewal renews only a
// subscription that is past due, or active and due — once the other path has
// charged it, it is neither. Concurrent: each renewal claims its subscriptions
// before reading them and holds the claim until it has written the result, and
// a second entrant is refused.

func newClaimRenewalService(provider payments.Provider) *app.RenewalService {
	return app.NewRenewalService(
		store.NewSubscriptionStore(nil),
		store.NewOrderStore(nil),
		store.NewCustomerStore(),
		store.NewPricingStore(),
		store.NewShippingStore(),
		provider,
		audit.NewAuditWriter(),
		metrics.NewRegistry(),
	)
}

// dueBox commits one customer with two subscriptions at one address, both in
// the given status and due an hour ago, as a batch decline leaves them.
func dueBox(t *testing.T, status domain.SubscriptionStatus) (a, b *domain.Subscription) {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck

	customer := testutil.CreateCustomer(t, tx)
	_, err = tx.Exec(ctx, `UPDATE customers SET stripe_customer_id = $2 WHERE id = $1`,
		customer.ID, "cus_"+uuid.NewString())
	require.NoError(t, err)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	plan := planEvery(t, tx, domain.SubscriptionIntervalEvery30Days)

	subs := make([]*domain.Subscription, 2)
	var productIDs []uuid.UUID
	for i, price := range []int{1800, 1000} {
		product := testutil.CreateProduct(t, tx)
		productIDs = append(productIDs, product.ID)
		variant := testutil.CreateVariant(t, tx, product.ID)
		testutil.SetBasePriceForVariant(t, tx, variant.ID, price, "USD")
		hourAgo := time.Now().Add(-time.Hour)
		sub, err := store.NewSubscriptionStore(nil).Create(ctx, tx, store.CreateSubscriptionParams{
			CustomerID:         customer.ID,
			PlanID:             plan.ID,
			VariantID:          variant.ID,
			Quantity:           1,
			Status:             status,
			ShippingAddressID:  addr.ID,
			CurrentPeriodStart: hourAgo.AddDate(0, 0, -30),
			CurrentPeriodEnd:   hourAgo,
			NextOrderAt:        hourAgo,
		})
		require.NoError(t, err)
		subs[i] = sub
	}
	require.NoError(t, tx.Commit(ctx))
	cleanupCommittedCustomer(t, customer.ID, productIDs, []uuid.UUID{plan.ID})
	return subs[0], subs[1]
}

func TestRenewal_ARetryAfterTheBatchChargedDoesNotChargeAgain(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc := newClaimRenewalService(provider)
	a, b := dueBox(t, domain.SubscriptionStatusPastDue)

	// The batch's own retry runs first and succeeds.
	order, err := svc.RenewBatch(ctx, testPool, []uuid.UUID{a.ID, b.ID})
	require.NoError(t, err)
	require.NotNil(t, order)
	require.Equal(t, 1, provider.ChargeCount())

	// Then the Retry the customer clicked while it was queued.
	_, err = svc.RenewSubscription(ctx, testPool, a.ID)
	assert.ErrorIs(t, err, app.ErrRenewalNotDue)
	assert.Equal(t, 1, provider.ChargeCount(), "the member the batch just renewed is not charged again")
}

func TestRenewal_ABatchAfterARetryChargedChargesOnlyTheRest(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc := newClaimRenewalService(provider)
	a, b := dueBox(t, domain.SubscriptionStatusPastDue)

	// The Retry runs first and renews one member.
	_, err := svc.RenewSubscription(ctx, testPool, a.ID)
	require.NoError(t, err)
	require.Equal(t, 1, provider.ChargeCount())

	// Then the batch, which still names both.
	order, err := svc.RenewBatch(ctx, testPool, []uuid.UUID{a.ID, b.ID})
	require.NoError(t, err)
	require.NotNil(t, order)
	require.Equal(t, 2, provider.ChargeCount())

	tx := testutil.NewTestTx(t, testPool)
	lines, err := store.NewOrderStore(nil).ListLineItems(ctx, tx, order.ID)
	require.NoError(t, err)
	require.Len(t, lines, 1, "the batch drops the member already renewed")
	assert.Equal(t, b.VariantID, lines[0].VariantID)
}

func TestRenewal_AnActiveSubscriptionNotYetDueIsNotRenewed(t *testing.T) {
	// The scheduler only queues what is due, so an active subscription that is
	// not due reached a renewal because something else already renewed it.
	ctx := context.Background()
	svc := newClaimRenewalService(nil) // must never reach the provider
	a, b := dueBox(t, domain.SubscriptionStatusActive)
	_, err := testPool.Exec(ctx, `UPDATE subscriptions SET next_order_at = now() + interval '29 days' WHERE id = ANY($1)`,
		[]uuid.UUID{a.ID, b.ID})
	require.NoError(t, err)

	_, err = svc.RenewSubscription(ctx, testPool, a.ID)
	assert.ErrorIs(t, err, app.ErrRenewalNotDue)

	order, err := svc.RenewBatch(ctx, testPool, []uuid.UUID{a.ID, b.ID})
	assert.NoError(t, err)
	assert.Nil(t, order, "nothing in the batch was due, so nothing was placed")
}

func TestRenewal_ASecondEntrantIsRefusedWhileTheFirstHoldsTheClaim(t *testing.T) {
	ctx := context.Background()
	svc := newClaimRenewalService(nil) // must never reach the provider
	a, b := dueBox(t, domain.SubscriptionStatusPastDue)
	subs := store.NewSubscriptionStore(nil)

	// Another renewal of a is between its read and its write.
	claimTx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	claimed, err := subs.ClaimRenewal(ctx, claimTx, []uuid.UUID{a.ID}, time.Now().Add(-15*time.Minute))
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, claimTx.Commit(ctx))

	_, err = svc.RenewSubscription(ctx, testPool, a.ID)
	assert.ErrorIs(t, err, app.ErrRenewalInFlight)

	_, err = svc.RenewBatch(ctx, testPool, []uuid.UUID{a.ID, b.ID})
	assert.ErrorIs(t, err, app.ErrRenewalInFlight, "one member in flight holds back the whole box")

	// The refused batch took no claim of its own: b is still free.
	tx := testutil.NewTestTx(t, testPool)
	claimed, err = subs.ClaimRenewal(ctx, tx, []uuid.UUID{b.ID}, time.Now().Add(-15*time.Minute))
	require.NoError(t, err)
	assert.True(t, claimed)
}

func TestRenewal_TheClaimIsReleasedAfterwards(t *testing.T) {
	ctx := context.Background()
	provider := paymentstest.New()
	svc := newClaimRenewalService(provider)
	a, b := dueBox(t, domain.SubscriptionStatusPastDue)

	_, err := svc.RenewBatch(ctx, testPool, []uuid.UUID{a.ID, b.ID})
	require.NoError(t, err)

	tx := testutil.NewTestTx(t, testPool)
	claimed, err := store.NewSubscriptionStore(nil).ClaimRenewal(ctx, tx,
		[]uuid.UUID{a.ID, b.ID}, time.Now().Add(-15*time.Minute))
	require.NoError(t, err)
	assert.True(t, claimed, "a finished renewal leaves nothing held")
}
