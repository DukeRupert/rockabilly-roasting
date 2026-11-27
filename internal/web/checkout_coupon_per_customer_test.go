package web

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// A once-per-customer code can sit on a cart before checkout knows who the
// shopper is: the code box is beside both steps. When the payment step names a
// customer who has already used the offer, the intent is priced without it —
// and, unlike the silent drop phase 1 used to do, the shopper is told why and
// the code comes off the cart so the summary and the charge agree.
//
// Commits, like everything driving a handler here — see setup_test.go.
func TestCheckoutPaymentIntent_RefusedCouponIsDroppedWithANotice(t *testing.T) {
	ctx := context.Background()
	f := newCheckoutCouponFixture(t)
	d, _ := newCheckoutPaymentDeps(t)

	// Swap the fixture's single-use code for a shared one this customer has
	// already used on an earlier paid order.
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck

	discount := testutil.CreateDiscount(t, tx,
		testutil.WithDiscountValue(50),
		testutil.WithUsageLimit(domain.CouponUsageOncePerCustomer))
	coupon := testutil.CreateCouponCode(t, tx, discount.ID,
		testutil.WithCouponCode("SHARED-"+uuid.New().String()[:8]))
	earlier := testutil.CreateOrder(t, tx, f.customer.ID, f.addressID, f.addressID)
	_, err = store.NewDiscountStore().CreateRedemption(ctx, tx, store.CreateRedemptionParams{
		DiscountID: discount.ID, CouponCodeID: coupon.ID,
		CustomerID: f.customer.ID, OrderID: earlier.ID,
	})
	require.NoError(t, err)
	orders := app.NewOrderService(store.NewOrderStore(nil), audit.NewAuditWriter(), metrics.NewRegistry())
	_, err = orders.UpdateCartDiscount(ctx, tx, f.cartID, &discount.ID, &coupon.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	w := f.postIntent(t, d, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	resp := decodeCheckoutIntentResponse(t, w)

	assert.Zero(t, resp.DiscountTotal, "the customer already had this offer")
	assert.Equal(t, 5000, resp.Subtotal)
	assert.Empty(t, resp.CouponCode)
	assert.Contains(t, resp.CouponNotice, "already used",
		"the shopper must be told why the price went up")

	check, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer check.Rollback(ctx) //nolint:errcheck
	cart, err := store.NewCartStore().GetCartByIDAsStaff(ctx, check, f.cartID)
	require.NoError(t, err)
	assert.Nil(t, cart.AppliedCouponCodeID, "the refused code is taken off the cart")
}

// The GA4 purchase event carries the code the order was placed with, so the
// client can see sales by promotion in Analytics. An order with no code sends
// no coupon parameter at all rather than an empty one.
func TestLoadOrderAnalytics_Coupon(t *testing.T) {
	ctx := context.Background()
	f := newCheckoutCouponFixture(t)
	d, _ := newCheckoutPaymentDeps(t)

	w := f.postIntent(t, d, "")
	require.Equal(t, 200, w.Code, w.Body.String())

	var withCode, without string
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT number FROM orders WHERE metadata->>'cart_id' = $1 ORDER BY created_at DESC LIMIT 1`,
		f.cartID.String()).Scan(&withCode))
	plain := testutil.CreateOrder(t, tx, f.customer.ID, f.addressID, f.addressID)
	without = plain.Number
	require.NoError(t, tx.Commit(ctx))

	got, err := d.loadOrderAnalytics(ctx, withCode)
	require.NoError(t, err)
	assert.Equal(t, f.code, got.Coupon)

	got, err = d.loadOrderAnalytics(ctx, without)
	require.NoError(t, err)
	assert.Empty(t, got.Coupon)
}
