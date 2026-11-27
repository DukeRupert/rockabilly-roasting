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

// Signup codes: WELCOME50 and the referral offer. Each discount works on one
// kind of order — cart checkout or the first order of a new subscription — and
// "new subscribers" means a customer who has never held a subscription.

// placeOrderKind places s's order as a cart checkout or as a subscription
// signup, linked to piID the way the endpoints link it.
func (s shopper) placeOrderKind(t *testing.T, tx pgx.Tx, svc *app.CheckoutService, code, piID string, signup bool) (*domain.Order, error) {
	t.Helper()
	order, err := svc.PlaceOrder(context.Background(), tx, app.PlaceOrderParams{
		CustomerID:         s.customer.ID,
		ShippingAddressID:  s.addr.ID,
		BillingAddressID:   s.addr.ID,
		CurrencyCode:       "USD",
		Items:              []app.CartItem{{VariantID: s.variant.ID, Quantity: 1, UnitPrice: 5000}},
		CouponCode:         &code,
		SubscriptionSignup: signup,
	}, testutil.TestActor())
	if err != nil {
		return nil, err
	}
	_, err = store.NewOrderStore(nil).UpdateOrderStripePaymentIntentID(context.Background(), tx, order.ID, piID)
	require.NoError(t, err)
	return order, nil
}

func newWelcomeCode(t *testing.T, tx pgx.Tx) (*domain.Discount, *domain.CouponCode) {
	t.Helper()
	d := testutil.CreateDiscount(t, tx,
		testutil.WithDiscountValue(50),
		testutil.WithAppliesTo(domain.DiscountAppliesToSubscriptionSignup),
		testutil.WithEligibility(domain.DiscountEligibilityNewSubscribers),
		testutil.WithUsageLimit(domain.CouponUsageOncePerCustomer))
	return d, testutil.CreateCouponCode(t, tx, d.ID)
}

func TestCheckoutService_CouponOrderKind(t *testing.T) {
	svc := newCheckoutService()

	t.Run("a signup code works on a signup and is refused at the cart", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, code := newWelcomeCode(t, tx)
		s := newShopper(t, tx)

		_, err := s.placeOrderKind(t, tx, svc, code.Code, "pi_kind_cart", false)
		assert.ErrorIs(t, err, app.ErrCouponSignupOnly)

		order, err := s.placeOrderKind(t, tx, svc, code.Code, "pi_kind_signup", true)
		require.NoError(t, err)
		assert.Equal(t, 2500, order.DiscountTotal)
	})

	t.Run("a cart code is refused on a signup", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		d := testutil.CreateDiscount(t, tx)
		code := testutil.CreateCouponCode(t, tx, d.ID)

		_, err := newShopper(t, tx).placeOrderKind(t, tx, svc, code.Code, "pi_kind_cart_on_signup", true)
		assert.ErrorIs(t, err, app.ErrCouponOneTimeOnly)
	})
}

func TestCheckoutService_NewSubscribersOnly(t *testing.T) {
	ctx := context.Background()
	svc := newCheckoutService()

	t.Run("any subscription, in any status, means not new", func(t *testing.T) {
		for _, status := range []domain.SubscriptionStatus{
			domain.SubscriptionStatusActive, domain.SubscriptionStatusPaused, domain.SubscriptionStatusPastDue,
			domain.SubscriptionStatusCancelled, domain.SubscriptionStatusExpired,
		} {
			tx := testutil.NewTestTx(t, testPool)
			_, code := newWelcomeCode(t, tx)
			s := newShopper(t, tx)
			s.subscribe(t, tx, status)

			_, err := s.placeOrderKind(t, tx, svc, code.Code, "pi_new_"+string(status), true)
			assert.ErrorIs(t, err, app.ErrCouponNewSubscribersOnly, "status %q", status)
		}
	})

	t.Run("the code box lets it on before it knows who is joining", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, code := newWelcomeCode(t, tx)
		s := newShopper(t, tx)
		s.subscribe(t, tx, domain.SubscriptionStatusActive)

		_, _, err := svc.ApplyCoupon(ctx, tx, code.Code, 5000, app.CouponUse{SubscriptionSignup: true})
		assert.NoError(t, err)
		_, _, err = svc.ApplyCoupon(ctx, tx, code.Code, 5000, app.CouponUse{SubscriptionSignup: true, CustomerID: &s.customer.ID})
		assert.ErrorIs(t, err, app.ErrCouponNewSubscribersOnly)
	})

	t.Run("paying for the signup claims it once for that customer", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		discount, code := newWelcomeCode(t, tx)
		s := newShopper(t, tx)

		_, err := s.placeOrderKind(t, tx, svc, code.Code, "pi_welcome_paid", true)
		require.NoError(t, err)
		capture(t, tx, svc, "pi_welcome_paid")

		used, err := store.NewDiscountStore().CustomerHasRedeemed(ctx, tx, discount.ID, s.customer.ID)
		require.NoError(t, err)
		assert.True(t, used)
	})
}

// The order kind is editable, and must survive the writes that do not mention
// it: deactivating, and an edit form submitted without the field.
func TestDiscountService_AppliesToSurvivesOtherWrites(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	svc := app.NewDiscountService(store.NewDiscountStore(), audit.NewAuditWriter(), metrics.NewRegistry())
	actor := testutil.TestActor()

	d, err := svc.CreateWithCode(ctx, tx, app.CreateDiscountParams{
		Name: "Welcome 50", Type: domain.DiscountTypePercentage, Value: 50,
		Code: "WELCOME-" + uuid.NewString()[:8], AppliesTo: domain.DiscountAppliesToSubscriptionSignup,
	}, actor)
	require.NoError(t, err)
	assert.Equal(t, domain.DiscountAppliesToSubscriptionSignup, d.AppliesTo)

	require.NoError(t, svc.SetActive(ctx, tx, d.ID, false, actor))
	got, err := svc.GetDiscount(ctx, tx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.DiscountAppliesToSubscriptionSignup, got.AppliesTo, "deactivating keeps it")

	got, err = svc.Update(ctx, tx, d.ID, app.EditDiscountParams{Name: "Welcome 50", Type: domain.DiscountTypePercentage, Value: 40}, actor)
	require.NoError(t, err)
	assert.Equal(t, domain.DiscountAppliesToSubscriptionSignup, got.AppliesTo, "an edit that omits it keeps it")

	got, err = svc.Update(ctx, tx, d.ID, app.EditDiscountParams{
		Name: "Welcome 50", Type: domain.DiscountTypePercentage, Value: 40, AppliesTo: domain.DiscountAppliesToOneTime,
	}, actor)
	require.NoError(t, err)
	assert.Equal(t, domain.DiscountAppliesToOneTime, got.AppliesTo, "and an edit can change it")
}
