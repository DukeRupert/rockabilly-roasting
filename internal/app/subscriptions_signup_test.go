package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func newSubscriptionService() *app.SubscriptionService {
	return app.NewSubscriptionService(
		store.NewSubscriptionStore(nil),
		store.NewOrderStore(nil),
		audit.NewAuditWriter(),
		metrics.NewRegistry(),
	)
}

func TestSubscriptionSignupMetadata_RoundTrip(t *testing.T) {
	planID := uuid.New()
	meta := app.SubscriptionSignupOrderMetadata(planID, "pi_test_123")

	got, ok := app.SubscriptionSignupPlanID(meta)
	require.True(t, ok)
	assert.Equal(t, planID, got)

	// A plain retail order's metadata must not read as a signup.
	_, ok = app.SubscriptionSignupPlanID(map[string]any{"cart_id": uuid.NewString()})
	assert.False(t, ok)
	_, ok = app.SubscriptionSignupPlanID(nil)
	assert.False(t, ok)
}

func TestSubscriptionService_ActivateFromSignupOrder(t *testing.T) {
	pool := testPool
	ctx := context.Background()
	actor := testutil.TestActor()
	checkout := newCheckoutService()
	subs := newSubscriptionService()

	t.Run("activates, links, and stamps the subscription", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)

		customer := testutil.CreateCustomer(t, tx)
		addr := testutil.CreateAddress(t, tx, customer.ID)
		product := testutil.CreateProduct(t, tx)
		variant := testutil.CreateVariant(t, tx, product.ID)
		plan, err := store.NewSubscriptionStore(nil).CreatePlan(ctx, tx, store.CreatePlanParams{
			Name:          "Monthly",
			Interval:      domain.SubscriptionIntervalEvery30Days,
			IntervalCount: 1,
			DiscountPct:   10,
			IsActive:      true,
		})
		require.NoError(t, err)

		order, err := checkout.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID:        customer.ID,
			ShippingAddressID: addr.ID,
			BillingAddressID:  addr.ID,
			CurrencyCode:      "USD",
			Items:             []app.CartItem{{VariantID: variant.ID, Quantity: 2, UnitPrice: 1620}},
			Metadata:          app.SubscriptionSignupOrderMetadata(plan.ID, "pi_test_activate"),
		}, actor)
		require.NoError(t, err)

		sub, err := subs.ActivateFromSignupOrder(ctx, tx, order, actor)
		require.NoError(t, err)
		assert.Equal(t, customer.ID, sub.CustomerID)
		assert.Equal(t, plan.ID, sub.PlanID)
		assert.Equal(t, variant.ID, sub.VariantID)
		assert.Equal(t, 2, sub.Quantity)
		assert.Equal(t, domain.SubscriptionStatusActive, sub.Status)

		// The order now points back at the subscription.
		refreshed, err := store.NewOrderStore(nil).GetOrderByIDAsStaff(ctx, tx, order.ID)
		require.NoError(t, err)
		require.NotNil(t, refreshed.SubscriptionID)
		assert.Equal(t, sub.ID, *refreshed.SubscriptionID)

		// And the subscription_orders link row exists for the first period.
		links, err := store.NewSubscriptionStore(nil).ListSubscriptionOrders(ctx, tx, sub.ID)
		require.NoError(t, err)
		require.Len(t, links, 1)
		assert.Equal(t, order.ID, links[0].OrderID)
	})

	t.Run("rejects an order without signup metadata", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		customer := testutil.CreateCustomer(t, tx)
		addr := testutil.CreateAddress(t, tx, customer.ID)
		product := testutil.CreateProduct(t, tx)
		variant := testutil.CreateVariant(t, tx, product.ID)

		order, err := checkout.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID:        customer.ID,
			ShippingAddressID: addr.ID,
			BillingAddressID:  addr.ID,
			CurrencyCode:      "USD",
			Items:             []app.CartItem{{VariantID: variant.ID, Quantity: 1, UnitPrice: 1000}},
			Metadata:          map[string]any{"cart_id": uuid.NewString()},
		}, actor)
		require.NoError(t, err)

		_, err = subs.ActivateFromSignupOrder(ctx, tx, order, actor)
		require.Error(t, err)
	})
}

// TestCheckoutService_ConfirmCheckoutPayment_RecoveryGuards covers the two
// transitions the subscribe recovery path depends on: a failed-then-succeeded
// PaymentIntent must still drive the order forward, and a payment that lands
// against an already-cancelled order must NOT silently resurrect it.
func TestCheckoutService_ConfirmCheckoutPayment_RecoveryGuards(t *testing.T) {
	pool := testPool
	ctx := context.Background()
	actor := testutil.TestActor()
	checkout := newCheckoutService()
	orders := store.NewOrderStore(nil)

	placeWithPI := func(t *testing.T, tx pgx.Tx, piID string) *domain.Order {
		customer := testutil.CreateCustomer(t, tx)
		addr := testutil.CreateAddress(t, tx, customer.ID)
		product := testutil.CreateProduct(t, tx)
		variant := testutil.CreateVariant(t, tx, product.ID)
		order, err := checkout.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID:        customer.ID,
			ShippingAddressID: addr.ID,
			BillingAddressID:  addr.ID,
			CurrencyCode:      "USD",
			Items:             []app.CartItem{{VariantID: variant.ID, Quantity: 1, UnitPrice: 1500}},
		}, actor)
		require.NoError(t, err)
		_, err = orders.UpdateOrderStripePaymentIntentID(ctx, tx, order.ID, piID)
		require.NoError(t, err)
		return order
	}

	t.Run("failed payment can still be captured on retry", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		order := placeWithPI(t, tx, "pi_failed_retry")

		// Simulate a declined first attempt: payment_status → failed.
		_, err := orders.UpdateOrderPaymentStatus(ctx, tx, order.ID, domain.PaymentStatusFailed)
		require.NoError(t, err)

		got, transitioned, err := checkout.ConfirmCheckoutPayment(ctx, tx, "pi_failed_retry", actor)
		require.NoError(t, err)
		assert.True(t, transitioned, "failed→captured must transition")
		assert.Equal(t, domain.PaymentStatusCaptured, got.PaymentStatus)
		assert.Equal(t, domain.OrderStatusConfirmed, got.Status)
	})

	t.Run("cancelled order is not resurrected by a late payment", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		order := placeWithPI(t, tx, "pi_cancelled_late")

		_, err := orders.UpdateOrderStatus(ctx, tx, order.ID, domain.OrderStatusCancelled)
		require.NoError(t, err)

		got, transitioned, err := checkout.ConfirmCheckoutPayment(ctx, tx, "pi_cancelled_late", actor)
		require.NoError(t, err)
		assert.False(t, transitioned, "cancelled order must not transition")
		assert.Equal(t, domain.OrderStatusCancelled, got.Status)
	})
}
