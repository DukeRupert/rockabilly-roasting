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

// The acceptance test for several items in one signup: the box it creates
// renews as one order with one shipping charge. The first test of RenewBatch
// that goes through the charge.
//
// Renewals open their own transactions, so these commit. The shipping config
// is one instance-wide row; withRenewalFlatShipping sets it and puts it back,
// which is safe because nothing in this package runs in parallel.

const renewalFlatRate = 650

func withRenewalFlatShipping(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	shipping := store.NewShippingStore()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	before, err := shipping.GetConfig(ctx, tx)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback(ctx))

	set := func(cfg domain.ShippingConfig) {
		tx, err := testPool.Begin(ctx)
		require.NoError(t, err)
		require.NoError(t, shipping.UpdateConfig(ctx, tx, cfg))
		require.NoError(t, tx.Commit(ctx))
	}
	restore := *before
	t.Cleanup(func() { set(restore) })

	cfg := *before
	cfg.FlatRateCents = renewalFlatRate
	cfg.FreeShippingThreshold = nil
	set(cfg)
}

// signedUpBox commits one signup of two lines on one plan, activates it, and
// makes both subscriptions due an hour ago with a card on file — two
// subscriptions from one checkout, as a customer who wanted two items has.
func signedUpBox(t *testing.T) (a, b *domain.Subscription) {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck

	f := newSignupFixture(t, tx)
	_, err = tx.Exec(ctx, `UPDATE customers SET stripe_customer_id = $2 WHERE id = $1`,
		f.customer.ID, "cus_"+uuid.NewString())
	require.NoError(t, err)
	plan := planEvery(t, tx, domain.SubscriptionIntervalEvery30Days)

	order, _ := f.place(t, tx, []app.CartItem{
		signupItem(f.first, 1, 1800, plan.ID),
		signupItem(f.second, 1, 1000, plan.ID),
	}, signupMeta())
	subs, err := newSubscriptionService().ActivateFromSignupOrder(ctx, tx, order, testutil.TestActor())
	require.NoError(t, err)
	require.Len(t, subs, 2)
	require.True(t, subs[0].NextOrderAt.Equal(subs[1].NextOrderAt), "one box")

	// A month on: both due at the same run.
	hourAgo := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	_, err = tx.Exec(ctx, `
		UPDATE subscriptions SET current_period_end = $2, next_order_at = $2
		WHERE id = ANY($1)`, []uuid.UUID{subs[0].ID, subs[1].ID}, hourAgo)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	cleanupCommittedCustomer(t, f.customer.ID, []uuid.UUID{f.product.ID}, []uuid.UUID{plan.ID})
	return subs[0], subs[1]
}

func orderLines(t *testing.T, orderID uuid.UUID) ([]domain.LineItem, *domain.Order) {
	t.Helper()
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	lines, err := store.NewOrderStore(nil).ListLineItems(ctx, tx, orderID)
	require.NoError(t, err)
	order, err := store.NewOrderStore(nil).GetOrderByIDAsStaff(ctx, tx, orderID)
	require.NoError(t, err)
	return lines, order
}

func readSub(t *testing.T, id uuid.UUID) *domain.Subscription {
	t.Helper()
	tx := testutil.NewTestTx(t, testPool)
	sub, err := store.NewSubscriptionStore(nil).GetByIDAsStaff(context.Background(), tx, id)
	require.NoError(t, err)
	return sub
}

func TestRenewBatch_OneSignupRenewsAsOneOrderWithOneShippingCharge(t *testing.T) {
	withRenewalFlatShipping(t)
	provider := paymentstest.New()
	svc := newClaimRenewalService(provider)
	a, b := signedUpBox(t)

	order, err := svc.RenewBatch(context.Background(), testPool, []uuid.UUID{a.ID, b.ID})
	require.NoError(t, err)
	require.NotNil(t, order)

	lines, stored := orderLines(t, order.ID)
	assert.Len(t, lines, 2, "both items on one order")
	assert.Equal(t, renewalFlatRate, stored.ShippingTotal, "shipping once, for the box")
	assert.Nil(t, stored.SubscriptionID, "several subscriptions: linked through subscription_orders")

	require.Equal(t, 1, provider.ChargeCount(), "one charge")
	assert.Equal(t, int64(stored.Total), provider.Charges()[0].AmountCents, "for the order's total")

	tx := testutil.NewTestTx(t, testPool)
	linked, err := store.NewSubscriptionStore(nil).ListByOrder(context.Background(), tx, order.ID)
	require.NoError(t, err)
	assert.Len(t, linked, 2)

	ra, rb := readSub(t, a.ID), readSub(t, b.ID)
	assert.True(t, ra.NextOrderAt.Equal(rb.NextOrderAt), "still one box afterwards")
	assert.True(t, ra.NextOrderAt.After(time.Now()))
}

// What happens once one member is skipped out of the box: each renews alone,
// and each order carries shipping. This is what the account page's notice and
// the signup page's notice tell the customer; pinned so a change that quietly
// merges them is a deliberate one.
func TestRenewSubscription_TwoSoloRenewalsCarryShippingEach(t *testing.T) {
	withRenewalFlatShipping(t)
	provider := paymentstest.New()
	svc := newClaimRenewalService(provider)
	a, b := signedUpBox(t)

	for _, sub := range []*domain.Subscription{a, b} {
		order, err := svc.RenewSubscription(context.Background(), testPool, sub.ID)
		require.NoError(t, err)
		require.NotNil(t, order)
		_, stored := orderLines(t, order.ID)
		assert.Equal(t, renewalFlatRate, stored.ShippingTotal)
	}
	assert.Equal(t, 2, provider.ChargeCount())
}

// A past-due member is retried at its next_order_at, and anything else due at
// that run batches with it. That is why GroupIntoBoxes keeps past-due rows in
// their box.
func TestRenewBatch_APastDueMemberBatchesWithTheActiveOne(t *testing.T) {
	withRenewalFlatShipping(t)
	provider := paymentstest.New()
	svc := newClaimRenewalService(provider)
	a, b := signedUpBox(t)
	setStatus(t, testPool, b.ID, domain.SubscriptionStatusPastDue)

	order, err := svc.RenewBatch(context.Background(), testPool, []uuid.UUID{a.ID, b.ID})
	require.NoError(t, err)
	require.NotNil(t, order)
	lines, stored := orderLines(t, order.ID)
	assert.Len(t, lines, 2)
	assert.Equal(t, renewalFlatRate, stored.ShippingTotal)
	assert.Equal(t, 1, provider.ChargeCount())
	assert.Equal(t, domain.SubscriptionStatusActive, readSub(t, b.ID).Status, "paid, so active again")
}

func setStatus(t *testing.T, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, id uuid.UUID, status domain.SubscriptionStatus) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE subscriptions SET status = $2 WHERE id = $1`, id, string(status))
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}
