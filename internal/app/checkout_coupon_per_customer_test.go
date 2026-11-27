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
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// Once-per-customer codes: one code handed to a whole list (WELCOME50), good
// for any number of customers, each on one order. The single-use rules in
// checkout_coupon_lifecycle_test.go still hold for the other shape; these are
// the ones that differ.

// shopper is a customer with an address and a 5000-cent variant to buy.
type shopper struct {
	customer *domain.Customer
	addr     *domain.Address
	variant  *domain.Variant
}

func newShopper(t *testing.T, tx pgx.Tx) shopper {
	t.Helper()
	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 5000, "USD")
	return shopper{customer: customer, addr: addr, variant: variant}
}

// place puts an order on the books for s with code, linked to piID the way
// phase 3 of the checkout endpoint links it.
func (s shopper) place(t *testing.T, tx pgx.Tx, svc *app.CheckoutService, code, piID string) (*domain.Order, error) {
	t.Helper()
	order, err := svc.PlaceOrder(context.Background(), tx, app.PlaceOrderParams{
		CustomerID:        s.customer.ID,
		ShippingAddressID: s.addr.ID,
		BillingAddressID:  s.addr.ID,
		CurrencyCode:      "USD",
		Items:             []app.CartItem{{VariantID: s.variant.ID, Quantity: 1, UnitPrice: 5000}},
		CouponCode:        &code,
	}, testutil.TestActor())
	if err != nil {
		return nil, err
	}
	_, err = store.NewOrderStore(nil).UpdateOrderStripePaymentIntentID(context.Background(), tx, order.ID, piID)
	require.NoError(t, err)
	return order, nil
}

func newSharedCode(t *testing.T, tx pgx.Tx) (*domain.Discount, *domain.CouponCode) {
	t.Helper()
	discount := testutil.CreateDiscount(t, tx,
		testutil.WithDiscountValue(50),
		testutil.WithUsageLimit(domain.CouponUsageOncePerCustomer))
	return discount, testutil.CreateCouponCode(t, tx, discount.ID)
}

func capture(t *testing.T, tx pgx.Tx, svc *app.CheckoutService, piID string) *domain.Order {
	t.Helper()
	order, transitioned, err := svc.ConfirmCheckoutPayment(context.Background(), tx, piID, testutil.TestActor())
	require.NoError(t, err)
	require.True(t, transitioned)
	return order
}

func countLostRedemptions(t *testing.T, tx pgx.Tx, orderID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, tx.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log
		 WHERE resource_type = 'order' AND resource_id = $1 AND action = $2`,
		orderID, audit.AuditCouponRedemptionLost).Scan(&n))
	return n
}

func TestCheckoutService_OncePerCustomerCode(t *testing.T) {
	ctx := context.Background()
	svc := newCheckoutService()
	discounts := store.NewDiscountStore()

	t.Run("different customers each get it, and the code is never spent", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		discount, coupon := newSharedCode(t, tx)

		for i, piID := range []string{"pi_shared_a", "pi_shared_b", "pi_shared_c"} {
			order, err := newShopper(t, tx).place(t, tx, svc, coupon.Code, piID)
			require.NoError(t, err, "customer %d", i)
			assert.Equal(t, 2500, order.DiscountTotal, "50%% of 5000")
			capture(t, tx, svc, piID)
			assert.Zero(t, countLostRedemptions(t, tx, order.ID))
		}

		after, err := discounts.GetCouponCodeByID(ctx, tx, coupon.ID)
		require.NoError(t, err)
		assert.Nil(t, after.RedeemedAt, "a shared code is never stamped as spent")

		n, err := discounts.CountRedemptions(ctx, tx, discount.ID)
		require.NoError(t, err)
		assert.Equal(t, 3, n)
	})

	t.Run("the same customer cannot place a second order once the first is paid", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, coupon := newSharedCode(t, tx)
		s := newShopper(t, tx)

		_, err := s.place(t, tx, svc, coupon.Code, "pi_repeat_first")
		require.NoError(t, err)
		capture(t, tx, svc, "pi_repeat_first")

		_, err = s.place(t, tx, svc, coupon.Code, "pi_repeat_second")
		assert.ErrorIs(t, err, app.ErrCouponUsedByCustomer)
	})

	t.Run("an unpaid order does not use it up", func(t *testing.T) {
		// The payment step re-places the order on every pricing change; the
		// orders it leaves behind unpaid must not count against the customer.
		tx := testutil.NewTestTx(t, testPool)
		_, coupon := newSharedCode(t, tx)
		s := newShopper(t, tx)

		_, err := s.place(t, tx, svc, coupon.Code, "pi_unpaid_first")
		require.NoError(t, err)
		_, err = s.place(t, tx, svc, coupon.Code, "pi_unpaid_second")
		require.NoError(t, err)
	})

	t.Run("two orders by one customer paid in turn: first wins, second keeps its price", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		discount, coupon := newSharedCode(t, tx)
		s := newShopper(t, tx)

		first, err := s.place(t, tx, svc, coupon.Code, "pi_two_tabs_first")
		require.NoError(t, err)
		second, err := s.place(t, tx, svc, coupon.Code, "pi_two_tabs_second")
		require.NoError(t, err)

		capture(t, tx, svc, "pi_two_tabs_first")
		got := capture(t, tx, svc, "pi_two_tabs_second")
		assert.Equal(t, domain.PaymentStatusCaptured, got.PaymentStatus,
			"a paid order must not be stranded over a coupon")
		assert.Equal(t, 2500, got.DiscountTotal)

		held, err := discounts.GetRedemptionByOrderID(ctx, tx, first.ID)
		require.NoError(t, err)
		assert.Equal(t, discount.ID, held.DiscountID)
		_, err = discounts.GetRedemptionByOrderID(ctx, tx, second.ID)
		assert.ErrorIs(t, err, pgx.ErrNoRows)

		assert.Equal(t, 1, countLostRedemptions(t, tx, second.ID))
		assert.Zero(t, countLostRedemptions(t, tx, first.ID))
	})

	t.Run("re-capturing the same order is not a lost redemption", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, coupon := newSharedCode(t, tx)
		order, err := newShopper(t, tx).place(t, tx, svc, coupon.Code, "pi_shared_recapture")
		require.NoError(t, err)

		capture(t, tx, svc, "pi_shared_recapture")
		_, err = store.NewOrderStore(nil).UpdateOrderPaymentStatus(ctx, tx, order.ID, domain.PaymentStatusFailed)
		require.NoError(t, err)
		capture(t, tx, svc, "pi_shared_recapture")

		assert.Zero(t, countLostRedemptions(t, tx, order.ID))
	})

	t.Run("cancelling a paid order gives the customer the offer back", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, coupon := newSharedCode(t, tx)
		s := newShopper(t, tx)

		order, err := s.place(t, tx, svc, coupon.Code, "pi_cancel_release")
		require.NoError(t, err)
		capture(t, tx, svc, "pi_cancel_release")

		_, err = newOrderService().WithDiscounts(discounts).CancelOrder(ctx, tx, order.ID, testutil.TestActor())
		require.NoError(t, err)

		_, err = s.place(t, tx, svc, coupon.Code, "pi_cancel_release_again")
		assert.NoError(t, err)
	})

	t.Run("the code box checks the customer only when it knows them", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, coupon := newSharedCode(t, tx)
		s := newShopper(t, tx)
		_, err := s.place(t, tx, svc, coupon.Code, "pi_box_check")
		require.NoError(t, err)
		capture(t, tx, svc, "pi_box_check")

		_, _, err = svc.ApplyCoupon(ctx, tx, coupon.Code, 5000, app.CouponUse{})
		assert.NoError(t, err, "before the information step the shopper is anonymous")

		_, _, err = svc.ApplyCoupon(ctx, tx, coupon.Code, 5000, app.CouponUse{CustomerID: &s.customer.ID})
		assert.ErrorIs(t, err, app.ErrCouponUsedByCustomer)

		_, _, err = svc.CheckAppliedCoupon(ctx, tx, coupon.ID, 5000, app.CouponUse{CustomerID: &s.customer.ID})
		assert.ErrorIs(t, err, app.ErrCouponUsedByCustomer)
	})

	t.Run("one offer cannot be taken twice through two of its codes", func(t *testing.T) {
		// Per-member referral codes will all hang off one discount.
		tx := testutil.NewTestTx(t, testPool)
		discount, first := newSharedCode(t, tx)
		second := testutil.CreateCouponCode(t, tx, discount.ID)
		s := newShopper(t, tx)

		_, err := s.place(t, tx, svc, first.Code, "pi_two_codes")
		require.NoError(t, err)
		capture(t, tx, svc, "pi_two_codes")

		_, err = s.place(t, tx, svc, second.Code, "pi_two_codes_again")
		assert.ErrorIs(t, err, app.ErrCouponUsedByCustomer)
	})
}

// starts_at was stored and editable but never read, so a discount went live
// the moment it was created whatever its start date said.
func TestCheckoutService_CouponStartDate(t *testing.T) {
	ctx := context.Background()
	svc := newCheckoutService()
	tx := testutil.NewTestTx(t, testPool)

	future := testutil.CreateDiscount(t, tx, testutil.WithDiscountStart(time.Now().Add(24*time.Hour)))
	early := testutil.CreateCouponCode(t, tx, future.ID)
	_, _, err := svc.ApplyCoupon(ctx, tx, early.Code, 1000, app.CouponUse{})
	assert.ErrorIs(t, err, app.ErrDiscountNotStarted)

	_, err = newShopper(t, tx).place(t, tx, svc, early.Code, "pi_not_started")
	assert.ErrorIs(t, err, app.ErrDiscountNotStarted, "placement holds the same line as the code box")

	past := testutil.CreateDiscount(t, tx, testutil.WithDiscountStart(time.Now().Add(-time.Hour)))
	started := testutil.CreateCouponCode(t, tx, past.ID)
	_, _, err = svc.ApplyCoupon(ctx, tx, started.Code, 1000, app.CouponUse{})
	assert.NoError(t, err)
}
