package app_test

import (
	"context"
	"testing"

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

// Where a coupon is spent, and when.
//
// It used to be spent at placement, inside PlaceOrder. The payment step
// re-runs /api/checkout/payment-intent on every shipping-method or pricing
// change and each run places a fresh order, so the code was spent by an order
// the same customer was still trying to pay for — and the next run, finding it
// spent, quietly priced the cart at full price. See
// internal/web/checkout_coupon_reuse_test.go for that seam; these are the
// service-level facts the fix rests on.

// placeWithCoupon puts an order on the books holding a 10%-off code, the way
// phase 3 of the checkout endpoint does, and hands back everything the
// assertions need to follow the code afterwards.
func placeWithCoupon(t *testing.T, tx pgx.Tx, svc *app.CheckoutService, piID string) (*domain.Order, *domain.CouponCode) {
	t.Helper()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 5000, "USD")

	discount := testutil.CreateDiscount(t, tx,
		testutil.WithDiscountType(domain.DiscountTypePercentage),
		testutil.WithDiscountValue(10),
		testutil.WithDiscountActive(true))
	coupon := testutil.CreateCouponCode(t, tx, discount.ID)

	code := coupon.Code
	order, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
		CustomerID:        customer.ID,
		ShippingAddressID: addr.ID,
		BillingAddressID:  addr.ID,
		CurrencyCode:      "USD",
		Items:             []app.CartItem{{VariantID: variant.ID, Quantity: 1, UnitPrice: 5000}},
		CouponCode:        &code,
	}, testutil.TestActor())
	require.NoError(t, err)

	_, err = store.NewOrderStore(nil).UpdateOrderStripePaymentIntentID(ctx, tx, order.ID, piID)
	require.NoError(t, err)

	return order, coupon
}

func TestCheckoutService_CouponIsSpentAtCaptureNotAtPlacement(t *testing.T) {
	ctx := context.Background()
	svc := newCheckoutService()
	discounts := store.NewDiscountStore()
	actor := testutil.TestActor()

	t.Run("placing an order prices the coupon in but does not spend it", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		order, coupon := placeWithCoupon(t, tx, svc, "pi_placed_only")

		assert.Equal(t, 500, order.DiscountTotal, "10% of 5000 — the order is priced with the code")

		after, err := discounts.GetCouponCodeByID(ctx, tx, coupon.ID)
		require.NoError(t, err)
		assert.Nil(t, after.RedeemedAt,
			"an order nobody has paid for must leave the code available")

		// And the order remembers which code it holds, or capture would have
		// nothing to redeem.
		assert.Equal(t, coupon.ID.String(), order.Metadata["coupon_code_id"])
	})

	t.Run("capturing the payment spends it and ties it to the order", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		order, coupon := placeWithCoupon(t, tx, svc, "pi_captured")

		_, transitioned, err := svc.ConfirmCheckoutPayment(ctx, tx, "pi_captured", actor)
		require.NoError(t, err)
		require.True(t, transitioned)

		after, err := discounts.GetCouponCodeByID(ctx, tx, coupon.ID)
		require.NoError(t, err)
		require.NotNil(t, after.RedeemedAt, "the money moved, so the code is spent")
		require.NotNil(t, after.RedeemedByOrderID)
		assert.Equal(t, order.ID, *after.RedeemedByOrderID)
	})

	t.Run("cancelling an unpaid order leaves the code usable", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		order, coupon := placeWithCoupon(t, tx, svc, "pi_cancelled")

		// The 24-hour abandoned-order sweep and an admin cancel both land
		// here. Its release is a no-op now that nothing was redeemed — which
		// is the point: the code was never taken out of circulation.
		orders := newOrderService().WithDiscounts(discounts)
		_, err := orders.CancelOrder(ctx, tx, order.ID, actor)
		require.NoError(t, err)

		after, err := discounts.GetCouponCodeByID(ctx, tx, coupon.ID)
		require.NoError(t, err)
		assert.Nil(t, after.RedeemedAt)
	})
}

// Two orders can hold the same single-use code between placement and capture —
// that is the price of moving redemption to capture. The first to pay gets the
// code. The second is not blocked: their money has already moved at Stripe by
// the time this runs, and stranding a paid order to protect a coupon helps
// nobody. What the merchant gets instead is an audit entry saying a single-use
// code went out twice.
func TestCheckoutService_SecondCaptureOnOneCodeStillCaptures(t *testing.T) {
	ctx := context.Background()
	svc := newCheckoutService()
	actor := testutil.TestActor()
	tx := testutil.NewTestTx(t, testPool)

	winner, coupon := placeWithCoupon(t, tx, svc, "pi_race_winner")

	// A second order on the same code, placed while the first is still unpaid.
	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 5000, "USD")
	code := coupon.Code
	loser, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
		CustomerID:        customer.ID,
		ShippingAddressID: addr.ID,
		BillingAddressID:  addr.ID,
		CurrencyCode:      "USD",
		Items:             []app.CartItem{{VariantID: variant.ID, Quantity: 1, UnitPrice: 5000}},
		CouponCode:        &code,
	}, actor)
	require.NoError(t, err, "nothing holds the code yet, so the second order places fine")
	_, err = store.NewOrderStore(nil).UpdateOrderStripePaymentIntentID(ctx, tx, loser.ID, "pi_race_loser")
	require.NoError(t, err)

	_, transitioned, err := svc.ConfirmCheckoutPayment(ctx, tx, "pi_race_winner", actor)
	require.NoError(t, err)
	require.True(t, transitioned)

	got, transitioned, err := svc.ConfirmCheckoutPayment(ctx, tx, "pi_race_loser", actor)
	require.NoError(t, err, "a paid order must not be stranded over a coupon")
	assert.True(t, transitioned)
	assert.Equal(t, domain.PaymentStatusCaptured, got.PaymentStatus)
	assert.Equal(t, 500, got.DiscountTotal, "they were charged the discounted total; it stands")

	// The code stays with whoever paid first.
	after, err := store.NewDiscountStore().GetCouponCodeByID(ctx, tx, coupon.ID)
	require.NoError(t, err)
	require.NotNil(t, after.RedeemedByOrderID)
	assert.Equal(t, winner.ID, *after.RedeemedByOrderID)

	entry := testutil.LastAuditEntryWithAction(t, tx, "order", loser.ID, audit.AuditCouponRedemptionLost)
	assert.Equal(t, audit.AuditCouponRedemptionLost, entry.Action)
}

// Re-entering capture on an order that already claimed its code is not a lost
// redemption, and must not be filed as one.
//
// The claim query cannot tell the two apart: RedeemCouponCode returns no rows
// for any coupon already spent, whoever spent it. And capture is re-enterable —
// a late payment_intent.payment_failed for an earlier declined attempt knocks a
// captured order back to failed, and the next success drives it forward again.
// Reading "already spent" as "somebody else got it" would tell the merchant a
// single-use code went out twice when it went to exactly one order.
func TestCheckoutService_RecapturingOneOrderIsNotALostRedemption(t *testing.T) {
	ctx := context.Background()
	svc := newCheckoutService()
	actor := testutil.TestActor()
	tx := testutil.NewTestTx(t, testPool)
	orders := store.NewOrderStore(nil)

	order, coupon := placeWithCoupon(t, tx, svc, "pi_recapture")

	_, transitioned, err := svc.ConfirmCheckoutPayment(ctx, tx, "pi_recapture", actor)
	require.NoError(t, err)
	require.True(t, transitioned)

	// The out-of-order failure webhook lands after the success.
	_, err = orders.UpdateOrderPaymentStatus(ctx, tx, order.ID, domain.PaymentStatusFailed)
	require.NoError(t, err)

	_, transitioned, err = svc.ConfirmCheckoutPayment(ctx, tx, "pi_recapture", actor)
	require.NoError(t, err)
	require.True(t, transitioned, "failed→captured is a transition this path accepts")

	after, err := store.NewDiscountStore().GetCouponCodeByID(ctx, tx, coupon.ID)
	require.NoError(t, err)
	require.NotNil(t, after.RedeemedByOrderID)
	assert.Equal(t, order.ID, *after.RedeemedByOrderID, "the code is still this order's")

	var lost int
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT count(*) FROM audit_log
		 WHERE resource_type = 'order' AND resource_id = $1 AND action = $2`,
		order.ID, audit.AuditCouponRedemptionLost).Scan(&lost))
	assert.Zero(t, lost, "nobody else took the code, so nothing was lost")
}

// What the admin order page asks for when it wants the code the customer
// typed. The adjustments table already shows the discount's name; the code is
// the thing support gets asked about.
//
// It used to be one lookup, keyed on redeemed_by_order_id. That field is only
// set at capture now, so an unpaid order answers nothing to it — and an order
// whose claim was lost to another customer never will. Both still have to
// answer with the code they were placed with.
func TestDiscountService_GetCouponCodeForOrder(t *testing.T) {
	ctx := context.Background()
	svc := newCheckoutService()
	discounts := app.NewDiscountService(store.NewDiscountStore(), audit.NewAuditWriter(), metrics.NewRegistry())
	actor := testutil.TestActor()

	t.Run("an unpaid order answers with the code it was placed with", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		order, coupon := placeWithCoupon(t, tx, svc, "pi_unpaid_lookup")

		got, err := discounts.GetCouponCodeForOrder(ctx, tx, order)
		require.NoError(t, err)
		require.NotNil(t, got, "support asking about a pending order still needs the code")
		assert.Equal(t, coupon.Code, got.Code)
	})

	t.Run("a captured order answers with the code it redeemed", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		order, coupon := placeWithCoupon(t, tx, svc, "pi_paid_lookup")
		_, _, err := svc.ConfirmCheckoutPayment(ctx, tx, "pi_paid_lookup", actor)
		require.NoError(t, err)

		got, err := discounts.GetCouponCodeForOrder(ctx, tx, order)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, coupon.Code, got.Code)
	})

	t.Run("an order that carried no coupon answers nothing", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		customer := testutil.CreateCustomer(t, tx)
		addr := testutil.CreateAddress(t, tx, customer.ID)
		product := testutil.CreateProduct(t, tx)
		variant := testutil.CreateVariant(t, tx, product.ID)
		testutil.SetBasePriceForVariant(t, tx, variant.ID, 5000, "USD")
		order, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID:        customer.ID,
			ShippingAddressID: addr.ID,
			BillingAddressID:  addr.ID,
			CurrencyCode:      "USD",
			Items:             []app.CartItem{{VariantID: variant.ID, Quantity: 1, UnitPrice: 5000}},
		}, actor)
		require.NoError(t, err)

		got, err := discounts.GetCouponCodeForOrder(ctx, tx, order)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}
