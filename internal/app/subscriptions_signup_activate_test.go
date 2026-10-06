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
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// One signup order, several lines, one subscription per line. Each line keeps
// its own plan; lines on one plan start in one box because they share
// next_order_at exactly, and lines on different plans do not.

func planEvery(t *testing.T, tx pgx.Tx, interval domain.SubscriptionInterval) *domain.SubscriptionPlan {
	t.Helper()
	plan, err := store.NewSubscriptionStore(nil).CreatePlan(context.Background(), tx, store.CreatePlanParams{
		Name:          string(interval),
		Interval:      interval,
		IntervalCount: 1,
		IsActive:      true,
	})
	require.NoError(t, err)
	return plan
}

// signupItem is a signup line at its undiscounted price: the plans above take
// no discount, so the catalog price is the price.
func signupItem(variant *domain.Variant, qty, unit int, planID uuid.UUID) app.CartItem {
	return app.CartItem{VariantID: variant.ID, Quantity: qty, UnitPrice: unit, SubscriptionPlanID: &planID}
}

func signupMeta() map[string]any {
	return map[string]any{"subscription_signup": true, "payment_intent_id": "pi_" + uuid.NewString()}
}

func subFor(t *testing.T, subs []*domain.Subscription, variantID uuid.UUID) *domain.Subscription {
	t.Helper()
	for _, s := range subs {
		if s.VariantID == variantID {
			return s
		}
	}
	t.Fatalf("no subscription for variant %s", variantID)
	return nil
}

func countSubscriptions(t *testing.T, tx pgx.Tx, customerID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, tx.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriptions WHERE customer_id = $1`, customerID).Scan(&n))
	return n
}

func TestActivateFromSignupOrder_OneSubscriptionPerLine(t *testing.T) {
	ctx := context.Background()
	actor := testutil.TestActor()

	t.Run("two lines on two plans", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newSignupFixture(t, tx)
		weekly := planEvery(t, tx, domain.SubscriptionIntervalEvery7Days)
		monthly := planEvery(t, tx, domain.SubscriptionIntervalEvery30Days)

		order, _ := f.place(t, tx, []app.CartItem{
			signupItem(f.first, 2, 1800, weekly.ID),
			signupItem(f.second, 1, 1000, monthly.ID),
		}, signupMeta())

		subs, err := newSubscriptionService().ActivateFromSignupOrder(ctx, tx, order, actor)
		require.NoError(t, err)
		require.Len(t, subs, 2)

		w := subFor(t, subs, f.first.ID)
		m := subFor(t, subs, f.second.ID)
		assert.Equal(t, weekly.ID, w.PlanID)
		assert.Equal(t, 2, w.Quantity)
		assert.Equal(t, monthly.ID, m.PlanID)
		assert.Equal(t, 1, m.Quantity)
		for _, s := range subs {
			assert.Equal(t, f.customer.ID, s.CustomerID)
			assert.Equal(t, f.addr.ID, s.ShippingAddressID)
			assert.Equal(t, domain.SubscriptionStatusActive, s.Status)
		}

		// One clock reading for the whole signup. The anchor is off in tests,
		// so next_order_at is the period end itself.
		assert.True(t, w.CurrentPeriodStart.Equal(m.CurrentPeriodStart),
			"both start at the same instant: %s vs %s", w.CurrentPeriodStart, m.CurrentPeriodStart)
		start := w.CurrentPeriodStart.In(time.Local)
		assert.True(t, w.NextOrderAt.Equal(start.AddDate(0, 0, 7)), "weekly: %s", w.NextOrderAt)
		assert.True(t, m.NextOrderAt.Equal(start.AddDate(0, 0, 30)), "monthly: %s", m.NextOrderAt)

		// Linked through subscription_orders, as a batched renewal is, and not
		// stamped: orders.subscription_id names one subscription.
		for _, s := range subs {
			links, err := store.NewSubscriptionStore(nil).ListSubscriptionOrders(ctx, tx, s.ID)
			require.NoError(t, err)
			require.Len(t, links, 1)
			assert.Equal(t, order.ID, links[0].OrderID)
		}
		refreshed, err := store.NewOrderStore(nil).GetOrderByIDAsStaff(ctx, tx, order.ID)
		require.NoError(t, err)
		assert.Nil(t, refreshed.SubscriptionID)

		// Each created entry names the signup, so the rows read as one signup
		// in the timeline.
		for _, s := range subs {
			var signupOrderID string
			require.NoError(t, tx.QueryRow(ctx, `
				SELECT metadata->>'signup_order_id' FROM audit_log
				WHERE resource_type = 'subscription' AND resource_id = $1 AND action = $2`,
				s.ID, "subscription.created").Scan(&signupOrderID))
			assert.Equal(t, order.ID.String(), signupOrderID)
		}
	})

	t.Run("two lines on one plan share next_order_at exactly", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newSignupFixture(t, tx)
		weekly := planEvery(t, tx, domain.SubscriptionIntervalEvery7Days)

		order, _ := f.place(t, tx, []app.CartItem{
			signupItem(f.first, 1, 1800, weekly.ID),
			signupItem(f.second, 1, 1000, weekly.ID),
		}, signupMeta())

		subs, err := newSubscriptionService().ActivateFromSignupOrder(ctx, tx, order, actor)
		require.NoError(t, err)
		require.Len(t, subs, 2)
		assert.True(t, subs[0].NextOrderAt.Equal(subs[1].NextOrderAt),
			"one box: %s vs %s", subs[0].NextOrderAt, subs[1].NextOrderAt)
	})

	t.Run("one line still stamps the order", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newSignupFixture(t, tx)
		weekly := planEvery(t, tx, domain.SubscriptionIntervalEvery7Days)

		order, _ := f.place(t, tx, []app.CartItem{signupItem(f.first, 1, 1800, weekly.ID)}, signupMeta())
		subs, err := newSubscriptionService().ActivateFromSignupOrder(ctx, tx, order, actor)
		require.NoError(t, err)
		require.Len(t, subs, 1)

		refreshed, err := store.NewOrderStore(nil).GetOrderByIDAsStaff(ctx, tx, order.ID)
		require.NoError(t, err)
		require.NotNil(t, refreshed.SubscriptionID)
		assert.Equal(t, subs[0].ID, *refreshed.SubscriptionID)
	})

	t.Run("a plan deactivated after checkout still activates", func(t *testing.T) {
		// The customer has paid. Signup guards were enforced when the intent
		// was created and are not re-run here.
		tx := testutil.NewTestTx(t, testPool)
		f := newSignupFixture(t, tx)
		weekly := planEvery(t, tx, domain.SubscriptionIntervalEvery7Days)
		monthly := planEvery(t, tx, domain.SubscriptionIntervalEvery30Days)

		order, _ := f.place(t, tx, []app.CartItem{
			signupItem(f.first, 1, 1800, weekly.ID),
			signupItem(f.second, 1, 1000, monthly.ID),
		}, signupMeta())
		require.NoError(t, newSubscriptionService().UpdatePlanActive(ctx, tx, monthly.ID, false, actor))

		subs, err := newSubscriptionService().ActivateFromSignupOrder(ctx, tx, order, actor)
		require.NoError(t, err)
		assert.Len(t, subs, 2)
	})

	t.Run("an order from before lines carried their plan activates one", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newSignupFixture(t, tx)
		monthly := planEvery(t, tx, domain.SubscriptionIntervalEvery30Days)

		order, _ := f.place(t, tx, []app.CartItem{{VariantID: f.first.ID, Quantity: 1, UnitPrice: 1800}},
			map[string]any{
				"subscription_signup":  true,
				"subscription_plan_id": monthly.ID.String(),
				"payment_intent_id":    "pi_legacy_activate",
			})

		subs, err := newSubscriptionService().ActivateFromSignupOrder(ctx, tx, order, actor)
		require.NoError(t, err)
		require.Len(t, subs, 1)
		assert.Equal(t, monthly.ID, subs[0].PlanID)
		assert.Equal(t, f.first.ID, subs[0].VariantID)
	})

	t.Run("an order with no lines is an error", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newSignupFixture(t, tx)
		weekly := planEvery(t, tx, domain.SubscriptionIntervalEvery7Days)

		order, _ := f.place(t, tx, []app.CartItem{signupItem(f.first, 1, 1800, weekly.ID)}, signupMeta())
		require.NoError(t, store.NewOrderStore(nil).DeleteLineItemsByOrder(ctx, tx, order.ID))

		_, err := newSubscriptionService().ActivateFromSignupOrder(ctx, tx, order, actor)
		assert.Error(t, err)
		assert.Zero(t, countSubscriptions(t, tx, f.customer.ID))
	})
}
