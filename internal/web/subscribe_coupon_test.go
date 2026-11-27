package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/testutil"
)

// Coupons on a subscription signup: WELCOME50, 50% off the first order of a
// new subscription, new subscribers only, once per customer. The signup has no
// cart, so the code rides on the payment-intent request, and a refused code is
// priced out and named in coupon_notice rather than refusing the signup.
//
// Commits, like everything driving a handler here — see setup_test.go.

// newSignupCode commits a discount with one code and returns the code.
func newSignupCode(t *testing.T, opts ...testutil.DiscountOption) string {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	d := testutil.CreateDiscount(t, tx, opts...)
	code := testutil.CreateCouponCode(t, tx, d.ID,
		testutil.WithCouponCode("SIGNUP-"+strings.ToUpper(uuid.NewString()[:8]))).Code
	require.NoError(t, tx.Commit(ctx))
	return code
}

func welcomeCode(t *testing.T) string {
	t.Helper()
	return newSignupCode(t,
		testutil.WithDiscountValue(50),
		testutil.WithAppliesTo(domain.DiscountAppliesToSubscriptionSignup),
		testutil.WithEligibility(domain.DiscountEligibilityNewSubscribers),
		testutil.WithUsageLimit(domain.CouponUsageOncePerCustomer))
}

func withCoupon(code string) func(map[string]any) {
	return func(b map[string]any) { b["coupon_code"] = code }
}

func withEmail(email string) func(map[string]any) {
	return func(b map[string]any) { b["email"] = email }
}

func decodeSubscribeIntent(t *testing.T, w *httptest.ResponseRecorder) subscribePaymentIntentResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp subscribePaymentIntentResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

func TestSubscribePaymentIntent_Coupon(t *testing.T) {
	f := newSubscribeFixture(t)

	t.Run("a welcome code takes half off the first order, and the charge agrees", func(t *testing.T) {
		d, fake := newSubscribePaymentDeps(t)
		withFlatShippingRate(t, d, 0)
		code := welcomeCode(t)

		resp := decodeSubscribeIntent(t, postSubscribePaymentIntent(t, d,
			subscribePaymentIntentBody(f.planID, f.variantID, 1, withCoupon(strings.ToLower(code)))))

		assert.Equal(t, 1620, resp.Subtotal, "1800 less the plan's 10%")
		assert.Equal(t, 810, resp.DiscountTotal, "half of the plan price, on this order")
		assert.Equal(t, code, resp.CouponCode)
		assert.Empty(t, resp.CouponNotice)
		assert.Equal(t, resp.Subtotal-resp.DiscountTotal+resp.ShippingTotal+resp.TaxTotal, resp.Amount)

		require.NotEmpty(t, fake.createdIntents)
		assert.EqualValues(t, resp.Amount, fake.createdIntents[len(fake.createdIntents)-1].AmountCents,
			"the card is charged the discounted total")

		var discountTotal int
		require.NoError(t, testPool.QueryRow(context.Background(),
			`SELECT discount_total FROM orders WHERE stripe_payment_intent_id = $1`,
			strings.TrimSuffix(resp.ClientSecret, "_secret")).Scan(&discountTotal))
		assert.Equal(t, 810, discountTotal, "the signup order records the discount")
	})

	t.Run("a cart-checkout code is priced out and the shopper told why", func(t *testing.T) {
		d, _ := newSubscribePaymentDeps(t)
		code := newSignupCode(t, testutil.WithDiscountValue(20))

		resp := decodeSubscribeIntent(t, postSubscribePaymentIntent(t, d,
			subscribePaymentIntentBody(f.planID, f.variantID, 1, withCoupon(code))))

		assert.Zero(t, resp.DiscountTotal)
		assert.Empty(t, resp.CouponCode)
		assert.Contains(t, resp.CouponNotice, "can't be used on a subscription")
	})

	t.Run("someone who has subscribed before is not a new subscriber", func(t *testing.T) {
		d, _ := newSubscribePaymentDeps(t)
		code := welcomeCode(t)

		email := "returning-" + uuid.NewString()[:8] + "@example.test"
		ctx := context.Background()
		tx, err := testPool.Begin(ctx)
		require.NoError(t, err)
		customer := testutil.CreateCustomer(t, tx, testutil.WithEmail(email))
		addr := testutil.CreateAddress(t, tx, customer.ID)
		now := time.Now()
		_, err = tx.Exec(ctx,
			`INSERT INTO subscriptions
			     (customer_id, plan_id, status, shipping_address_id, variant_id, quantity,
			      current_period_start, current_period_end, next_order_at, cancelled_at)
			 VALUES ($1, $2, 'cancelled', $3, $4, 1, $5, $6, $6, $5)`,
			customer.ID, f.planID, addr.ID, f.variantID, now, now.AddDate(0, 1, 0))
		require.NoError(t, err)
		require.NoError(t, tx.Commit(ctx))

		resp := decodeSubscribeIntent(t, postSubscribePaymentIntent(t, d,
			subscribePaymentIntentBody(f.planID, f.variantID, 1, withCoupon(code), withEmail(email))))

		assert.Zero(t, resp.DiscountTotal, "a cancelled subscription still counts as having subscribed")
		assert.Contains(t, resp.CouponNotice, "new Daily Grind members")
	})
}

// A signup code is no good at cart checkout: it is for joining, and the cart
// would give it to anyone buying a bag.
func TestCheckoutApplyCoupon_RefusesASignupCode(t *testing.T) {
	f := newCheckoutCouponFixture(t)
	d, _ := newCheckoutPaymentDeps(t)
	code := welcomeCode(t)

	body, err := json.Marshal(checkoutApplyCouponRequest{CartID: f.cartID.String(), Code: code})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	d.handleCheckoutApplyCoupon(w, httptest.NewRequest(http.MethodPost, "/api/checkout/coupon", strings.NewReader(string(body))))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp checkoutApplyCouponResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Valid)
	assert.Contains(t, resp.ErrorMessage, "new Daily Grind subscriptions")
}
