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
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/payments/paymentstest"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// Customers managing their own subscriptions: EditSubscription and AddToBox.
// Every change applies from the next renewal and nothing is charged when it is
// made, so the rules worth pinning are who may change what, when, and that a
// change never moves a shipment the customer already has booked.

// manageFixture is a customer with one active subscription on a subscribable
// coffee: 12oz whole bean at $18, a 12oz ground at $18 and a 2lb at $40 beside
// it, a monthly plan it is on and a weekly plan it is not.
type manageFixture struct {
	customer                 *domain.Customer
	addr                     *domain.Address
	product                  *domain.Product
	wholeBean, ground, twoLb *domain.Variant
	monthly, weekly          *domain.SubscriptionPlan
	sub                      *domain.Subscription
}

func subscribableProduct(t *testing.T, tx pgx.Tx) *domain.Product {
	t.Helper()
	product := testutil.CreateProduct(t, tx)
	_, err := tx.Exec(context.Background(), `UPDATE products SET subscribable = true WHERE id = $1`, product.ID)
	require.NoError(t, err)
	product.Subscribable = true
	return product
}

func pricedVariant(t *testing.T, tx pgx.Tx, productID uuid.UUID, cents int, opts ...testutil.VariantOption) *domain.Variant {
	t.Helper()
	v := testutil.CreateVariant(t, tx, productID, opts...)
	testutil.SetBasePriceForVariant(t, tx, v.ID, cents, "USD")
	return v
}

func newManageFixture(t *testing.T, tx pgx.Tx) manageFixture {
	t.Helper()
	f := manageFixture{}
	f.customer = testutil.CreateCustomer(t, tx)
	f.addr = testutil.CreateAddress(t, tx, f.customer.ID)
	f.product = subscribableProduct(t, tx)
	f.wholeBean = pricedVariant(t, tx, f.product.ID, 1800)
	f.ground = pricedVariant(t, tx, f.product.ID, 1800)
	f.twoLb = pricedVariant(t, tx, f.product.ID, 4000)
	f.monthly = planEvery(t, tx, domain.SubscriptionIntervalEvery30Days)
	f.weekly = planEvery(t, tx, domain.SubscriptionIntervalEvery7Days)
	f.sub = createTestSubscription(t, tx, f.customer.ID, f.monthly.ID, f.wholeBean.ID, f.addr.ID)
	return f
}

func auditActions(t *testing.T, tx pgx.Tx, resourceID uuid.UUID) []string {
	t.Helper()
	rows, err := tx.Query(context.Background(),
		`SELECT action FROM audit_log WHERE resource_id = $1 ORDER BY created_at`, resourceID)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		require.NoError(t, rows.Scan(&a))
		out = append(out, a)
	}
	require.NoError(t, rows.Err())
	return out
}

func ptr[T any](v T) *T { return &v }

func TestEditSubscription(t *testing.T) {
	ctx := context.Background()
	actor := testutil.TestActor()
	svc := newSubscriptionServiceWithCatalog()

	t.Run("quantity, variant and plan in one edit, from the next renewal", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newManageFixture(t, tx)

		edit, err := svc.EditSubscription(ctx, tx, f.sub.ID, f.customer.ID, app.EditSubscriptionParams{
			Quantity:  ptr(3),
			VariantID: &f.twoLb.ID,
			PlanID:    &f.weekly.ID,
		}, actor)
		require.NoError(t, err)
		assert.True(t, edit.QuantityChanged && edit.VariantChanged && edit.PlanChanged)

		got := reread(t, tx, f.sub.ID)
		assert.Equal(t, 3, got.Quantity)
		assert.Equal(t, f.twoLb.ID, got.VariantID, "a size change is allowed: the customer saw the price")
		assert.Equal(t, f.weekly.ID, got.PlanID)
		assert.True(t, got.NextOrderAt.Equal(f.sub.NextOrderAt), "the booked shipment keeps its date")
		assert.True(t, got.CurrentPeriodEnd.Equal(f.sub.CurrentPeriodEnd))

		// One transaction, one now(): the rows' created_at do not order them.
		assert.ElementsMatch(t, []string{
			audit.AuditSubscriptionQuantityChanged,
			audit.AuditSubscriptionVariantChanged,
			audit.AuditSubscriptionPlanChanged,
		}, auditActions(t, tx, f.sub.ID))
	})

	t.Run("a shorter plan does not pull the next shipment into the past", func(t *testing.T) {
		// Staff ChangePlan recomputes next_order_at from the period start. Three
		// weeks into a monthly period, weekly would land two weeks ago and the
		// scheduler would charge at its next run.
		tx := testutil.NewTestTx(t, testPool)
		f := newManageFixture(t, tx)
		start := time.Now().UTC().AddDate(0, 0, -21).Truncate(time.Second)
		end := start.AddDate(0, 0, 30)
		require.NoError(t, store.NewSubscriptionStore(nil).UpdatePeriod(ctx, tx, f.sub.ID, start, end, end))

		_, err := svc.EditSubscription(ctx, tx, f.sub.ID, f.customer.ID, app.EditSubscriptionParams{PlanID: &f.weekly.ID}, actor)
		require.NoError(t, err)
		got := reread(t, tx, f.sub.ID)
		assert.True(t, got.NextOrderAt.Equal(end), "still the booked date, not %s", got.NextOrderAt)
		assert.True(t, got.NextOrderAt.After(time.Now()))
	})

	t.Run("nothing to change writes nothing", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newManageFixture(t, tx)
		edit, err := svc.EditSubscription(ctx, tx, f.sub.ID, f.customer.ID, app.EditSubscriptionParams{
			Quantity: ptr(1), VariantID: &f.wholeBean.ID, PlanID: &f.monthly.ID,
		}, actor)
		require.NoError(t, err)
		assert.False(t, edit.Changed())
		assert.Empty(t, auditActions(t, tx, f.sub.ID))
	})

	t.Run("all or nothing: a refused field leaves the others as they were", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newManageFixture(t, tx)
		otherProduct := subscribableProduct(t, tx)
		elsewhere := pricedVariant(t, tx, otherProduct.ID, 1800)

		_, err := svc.EditSubscription(ctx, tx, f.sub.ID, f.customer.ID, app.EditSubscriptionParams{
			Quantity: ptr(2), VariantID: &elsewhere.ID,
		}, actor)
		require.ErrorIs(t, err, app.ErrVariantNotOnSameProduct)
		assert.Equal(t, 1, reread(t, tx, f.sub.ID).Quantity)
	})

	refusals := []struct {
		name  string
		setup func(t *testing.T, tx pgx.Tx, f manageFixture) (customerID uuid.UUID, p app.EditSubscriptionParams)
		want  error
	}{
		{
			name: "another customer's subscription",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.EditSubscriptionParams) {
				return testutil.CreateCustomer(t, tx).ID, app.EditSubscriptionParams{Quantity: ptr(2)}
			},
			want: app.ErrSubscriptionNotFound,
		},
		{
			name: "past due: settle the charge first",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.EditSubscriptionParams) {
				execT(t, tx, `UPDATE subscriptions SET status = 'past_due' WHERE id = $1`, f.sub.ID)
				return f.customer.ID, app.EditSubscriptionParams{Quantity: ptr(2)}
			},
			want: app.ErrSubscriptionPastDue,
		},
		{
			name: "cancelled",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.EditSubscriptionParams) {
				execT(t, tx, `UPDATE subscriptions SET status = 'cancelled', cancelled_at = now() WHERE id = $1`, f.sub.ID)
				return f.customer.ID, app.EditSubscriptionParams{Quantity: ptr(2)}
			},
			want: app.ErrSubscriptionNotEditable,
		},
		{
			name: "a renewal holds the claim",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.EditSubscriptionParams) {
				execT(t, tx, `UPDATE subscriptions SET renewal_claimed_at = now() WHERE id = $1`, f.sub.ID)
				return f.customer.ID, app.EditSubscriptionParams{Quantity: ptr(2)}
			},
			want: app.ErrSubscriptionRenewing,
		},
		{
			name: "a renewal has charged and not yet written the order",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.EditSubscriptionParams) {
				execT(t, tx, `UPDATE subscriptions SET renewal_payment_intent_id = 'pi_test' WHERE id = $1`, f.sub.ID)
				return f.customer.ID, app.EditSubscriptionParams{Quantity: ptr(2)}
			},
			want: app.ErrSubscriptionRenewing,
		},
		{
			name: "due and not yet claimed: the next renewal run takes it as it stands",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.EditSubscriptionParams) {
				execT(t, tx, `UPDATE subscriptions SET next_order_at = now() - interval '1 minute' WHERE id = $1`, f.sub.ID)
				return f.customer.ID, app.EditSubscriptionParams{Quantity: ptr(2)}
			},
			want: app.ErrSubscriptionRenewing,
		},
		{
			name: "quantity over ten",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.EditSubscriptionParams) {
				return f.customer.ID, app.EditSubscriptionParams{Quantity: ptr(11)}
			},
			want: app.ErrInvalidQuantity,
		},
		{
			name: "an archived variant",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.EditSubscriptionParams) {
				execT(t, tx, `UPDATE variants SET archived_at = now() WHERE id = $1`, f.ground.ID)
				return f.customer.ID, app.EditSubscriptionParams{VariantID: &f.ground.ID}
			},
			want: app.ErrVariantArchived,
		},
		{
			name: "a wholesale-only variant",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.EditSubscriptionParams) {
				v := pricedVariant(t, tx, f.product.ID, 1800, testutil.WithChannelAvailability(false, true))
				return f.customer.ID, app.EditSubscriptionParams{VariantID: &v.ID}
			},
			want: app.ErrVariantNotInChannel,
		},
		{
			name: "an inactive plan",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.EditSubscriptionParams) {
				execT(t, tx, `UPDATE subscription_plans SET is_active = false WHERE id = $1`, f.weekly.ID)
				return f.customer.ID, app.EditSubscriptionParams{PlanID: &f.weekly.ID}
			},
			want: app.ErrSubscriptionPlanInactive,
		},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			tx := testutil.NewTestTx(t, testPool)
			f := newManageFixture(t, tx)
			customerID, p := tc.setup(t, tx, f)

			_, err := svc.EditSubscription(ctx, tx, f.sub.ID, customerID, p, actor)
			require.ErrorIs(t, err, tc.want)
			assert.Empty(t, auditActions(t, tx, f.sub.ID), "nothing written")
		})
	}

	t.Run("a claim older than the lease does not hold it", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newManageFixture(t, tx)
		execT(t, tx, `UPDATE subscriptions SET renewal_claimed_at = now() - interval '20 minutes' WHERE id = $1`, f.sub.ID)

		_, err := svc.EditSubscription(ctx, tx, f.sub.ID, f.customer.ID, app.EditSubscriptionParams{Quantity: ptr(2)}, actor)
		require.NoError(t, err)
	})

	t.Run("a paused subscription can be changed", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newManageFixture(t, tx)
		// Pausing leaves next_order_at where it was, so a paused subscription's
		// is often in the past. It is not renewed, so that does not hold it.
		execT(t, tx, `UPDATE subscriptions SET status = 'paused', next_order_at = now() - interval '19 days' WHERE id = $1`, f.sub.ID)

		_, err := svc.EditSubscription(ctx, tx, f.sub.ID, f.customer.ID, app.EditSubscriptionParams{Quantity: ptr(2)}, actor)
		require.NoError(t, err)
		assert.Equal(t, 2, reread(t, tx, f.sub.ID).Quantity)
	})
}

func TestAddToBox(t *testing.T) {
	ctx := context.Background()
	actor := testutil.TestActor()
	svc := newSubscriptionServiceWithCatalog()

	boxKey := func(f manageFixture) domain.SubscriptionBoxKey {
		return domain.SubscriptionBoxKey{ShippingAddressID: f.addr.ID, NextOrderAt: f.sub.NextOrderAt}
	}

	t.Run("joins the box: same address and instant, nothing charged now", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newManageFixture(t, tx)
		other := subscribableProduct(t, tx)
		decaf := pricedVariant(t, tx, other.ID, 1900)

		added, err := svc.AddToBox(ctx, tx, f.customer.ID, app.AddToBoxParams{
			Key: boxKey(f), VariantID: decaf.ID, PlanID: f.monthly.ID, Quantity: 2,
		}, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.SubscriptionStatusActive, added.Status)
		assert.Equal(t, 2, added.Quantity)
		assert.True(t, added.NextOrderAt.Equal(f.sub.NextOrderAt))
		assert.True(t, added.CurrentPeriodEnd.Equal(f.sub.NextOrderAt), "the first period runs to the box's renewal")
		assert.False(t, added.ShippingGrandfathered())

		subs, err := svc.ListSubscriptionsByCustomer(ctx, tx, f.customer.ID)
		require.NoError(t, err)
		boxes := domain.GroupIntoBoxes(subs, time.UTC)
		require.Len(t, boxes, 1, "one box, not two")
		assert.Len(t, boxes[0].Members, 2)
		assert.Equal(t, []string{audit.AuditSubscriptionCreated}, auditActions(t, tx, added.ID))
	})

	t.Run("inherits free shipping only when the whole box has it", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newManageFixture(t, tx)
		subsStore := store.NewSubscriptionStore(nil)
		require.NoError(t, subsStore.SetShippingGrandfathered(ctx, tx, f.sub.ID, true))

		added, err := svc.AddToBox(ctx, tx, f.customer.ID, app.AddToBoxParams{
			Key: boxKey(f), VariantID: f.ground.ID, PlanID: f.monthly.ID, Quantity: 1,
		}, actor)
		require.NoError(t, err)
		assert.True(t, added.ShippingGrandfathered(),
			"a batch waives shipping only when every member is grandfathered; without the flag the whole box would start paying")

		// A box that is already mixed stays mixed.
		require.NoError(t, subsStore.SetShippingGrandfathered(ctx, tx, added.ID, false))
		third, err := svc.AddToBox(ctx, tx, f.customer.ID, app.AddToBoxParams{
			Key: boxKey(f), VariantID: f.twoLb.ID, PlanID: f.monthly.ID, Quantity: 1,
		}, actor)
		require.NoError(t, err)
		assert.False(t, third.ShippingGrandfathered())
	})

	refusals := []struct {
		name  string
		setup func(t *testing.T, tx pgx.Tx, f manageFixture) (customerID uuid.UUID, p app.AddToBoxParams)
		want  error
	}{
		{
			name: "the box is gone (renewed or skipped since the page was drawn)",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.AddToBoxParams) {
				key := boxKey(f)
				key.NextOrderAt = key.NextOrderAt.Add(time.Hour)
				return f.customer.ID, app.AddToBoxParams{Key: key, VariantID: f.ground.ID, PlanID: f.monthly.ID, Quantity: 1}
			},
			want: app.ErrSubscriptionChanged,
		},
		{
			name: "another customer's box key",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.AddToBoxParams) {
				return testutil.CreateCustomer(t, tx).ID, app.AddToBoxParams{Key: boxKey(f), VariantID: f.ground.ID, PlanID: f.monthly.ID, Quantity: 1}
			},
			want: app.ErrSubscriptionChanged,
		},
		{
			name: "a member is past due",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.AddToBoxParams) {
				execT(t, tx, `UPDATE subscriptions SET status = 'past_due' WHERE id = $1`, f.sub.ID)
				return f.customer.ID, app.AddToBoxParams{Key: boxKey(f), VariantID: f.ground.ID, PlanID: f.monthly.ID, Quantity: 1}
			},
			want: app.ErrSubscriptionPastDue,
		},
		{
			name: "a renewal holds a member",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.AddToBoxParams) {
				execT(t, tx, `UPDATE subscriptions SET renewal_claimed_at = now() WHERE id = $1`, f.sub.ID)
				return f.customer.ID, app.AddToBoxParams{Key: boxKey(f), VariantID: f.ground.ID, PlanID: f.monthly.ID, Quantity: 1}
			},
			want: app.ErrSubscriptionRenewing,
		},
		{
			name: "the box is due: it would miss the batch and ship alone",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.AddToBoxParams) {
				due := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
				execT(t, tx, `UPDATE subscriptions SET current_period_end = $2, next_order_at = $2 WHERE id = $1`, f.sub.ID, due)
				key := domain.SubscriptionBoxKey{ShippingAddressID: f.addr.ID, NextOrderAt: due}
				return f.customer.ID, app.AddToBoxParams{Key: key, VariantID: f.ground.ID, PlanID: f.monthly.ID, Quantity: 1}
			},
			want: app.ErrSubscriptionRenewing,
		},
		{
			name: "that coffee on that plan is already in the box",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.AddToBoxParams) {
				return f.customer.ID, app.AddToBoxParams{Key: boxKey(f), VariantID: f.wholeBean.ID, PlanID: f.monthly.ID, Quantity: 1}
			},
			want: app.ErrAlreadyInBox,
		},
		{
			name: "the box is full",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.AddToBoxParams) {
				for i := 0; i < 9; i++ {
					v := pricedVariant(t, tx, f.product.ID, 1800)
					execT(t, tx, `
						INSERT INTO subscriptions (customer_id, plan_id, variant_id, quantity, status, shipping_address_id,
						                           current_period_start, current_period_end, next_order_at)
						VALUES ($1, $2, $3, 1, 'active', $4, $5, $6, $6)`,
						f.customer.ID, f.monthly.ID, v.ID, f.addr.ID, f.sub.CurrentPeriodStart, f.sub.NextOrderAt)
				}
				return f.customer.ID, app.AddToBoxParams{Key: boxKey(f), VariantID: f.ground.ID, PlanID: f.monthly.ID, Quantity: 1}
			},
			want: app.ErrBoxFull,
		},
		{
			name: "a coffee not sold on subscription",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.AddToBoxParams) {
				mug := testutil.CreateProduct(t, tx)
				v := pricedVariant(t, tx, mug.ID, 1500)
				return f.customer.ID, app.AddToBoxParams{Key: boxKey(f), VariantID: v.ID, PlanID: f.monthly.ID, Quantity: 1}
			},
			want: app.ErrVariantNotSubscribable,
		},
		{
			name: "quantity zero",
			setup: func(t *testing.T, tx pgx.Tx, f manageFixture) (uuid.UUID, app.AddToBoxParams) {
				return f.customer.ID, app.AddToBoxParams{Key: boxKey(f), VariantID: f.ground.ID, PlanID: f.monthly.ID, Quantity: 0}
			},
			want: app.ErrInvalidQuantity,
		},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			tx := testutil.NewTestTx(t, testPool)
			f := newManageFixture(t, tx)
			customerID, p := tc.setup(t, tx, f)
			before, err := svc.ListSubscriptionsByCustomer(ctx, tx, f.customer.ID)
			require.NoError(t, err)

			_, err = svc.AddToBox(ctx, tx, customerID, p, actor)
			require.ErrorIs(t, err, tc.want)
			after, err := svc.ListSubscriptionsByCustomer(ctx, tx, f.customer.ID)
			require.NoError(t, err)
			assert.Len(t, after, len(before), "no subscription added")
		})
	}
}

// The end-to-end promise of adding to a box: the added coffee renews with the
// rest, as one order with one shipping charge.
func TestAddToBox_RenewsWithTheBoxAsOneOrder(t *testing.T) {
	withRenewalFlatShipping(t)
	ctx := context.Background()
	provider := paymentstest.New()
	renewals := newClaimRenewalService(provider)

	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	f := newManageFixture(t, tx)
	execT(t, tx, `UPDATE customers SET stripe_customer_id = $2 WHERE id = $1`, f.customer.ID, "cus_"+uuid.NewString())
	added, err := newSubscriptionServiceWithCatalog().AddToBox(ctx, tx, f.customer.ID, app.AddToBoxParams{
		Key:       domain.SubscriptionBoxKey{ShippingAddressID: f.addr.ID, NextOrderAt: f.sub.NextOrderAt},
		VariantID: f.ground.ID, PlanID: f.monthly.ID, Quantity: 2,
	}, testutil.TestActor())
	require.NoError(t, err)

	// The box comes due.
	hourAgo := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	execT(t, tx, `UPDATE subscriptions SET current_period_end = $2, next_order_at = $2 WHERE id = ANY($1)`,
		[]uuid.UUID{f.sub.ID, added.ID}, hourAgo)
	require.NoError(t, tx.Commit(ctx))
	cleanupCommittedCustomer(t, f.customer.ID, []uuid.UUID{f.product.ID}, []uuid.UUID{f.monthly.ID, f.weekly.ID})

	order, err := renewals.RenewBatch(ctx, testPool, []uuid.UUID{f.sub.ID, added.ID})
	require.NoError(t, err)
	require.NotNil(t, order)

	lines, stored := orderLines(t, order.ID)
	assert.Len(t, lines, 2, "the added coffee is on the box's order")
	assert.Equal(t, 2, lineFor(t, lines, f.ground.ID).Quantity)
	assert.Equal(t, renewalFlatRate, stored.ShippingTotal, "shipping once, for the box")
	assert.Equal(t, 1, provider.ChargeCount())
	assert.True(t, readSub(t, f.sub.ID).NextOrderAt.Equal(readSub(t, added.ID).NextOrderAt), "still one box afterwards")
}

func execT(t *testing.T, tx pgx.Tx, sql string, args ...any) {
	t.Helper()
	_, err := tx.Exec(context.Background(), sql, args...)
	require.NoError(t, err)
}

func TestSubscriptionEdit_Notice(t *testing.T) {
	before := &domain.Subscription{ID: uuid.New(), CustomerID: uuid.New(), Quantity: 1, VariantID: uuid.New(), PlanID: uuid.New()}
	after := *before
	after.Quantity = 2
	after.PlanID = uuid.New()
	edit := &app.SubscriptionEdit{Before: before, After: &after, QuantityChanged: true, PlanChanged: true}

	n := edit.Notice()
	assert.Equal(t, 1, n.OldQuantity)
	assert.Equal(t, 2, n.NewQuantity)
	assert.Nil(t, n.NewVariantID, "an unchanged field is left out")
	require.NotNil(t, n.OldPlanID)
	assert.Equal(t, before.PlanID, *n.OldPlanID)
	assert.Equal(t, after.PlanID, *n.NewPlanID)
	assert.False(t, n.Added)
}
