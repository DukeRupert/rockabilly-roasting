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
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// The code box takes membership from the session and nowhere else. A guest is
// sent to sign in, carrying a member's customer ID in the body changes
// nothing, and the same member signed in is let through.
//
// Commits, like everything driving a handler here — see setup_test.go.
func TestCheckoutApplyCoupon_SubscribersOnly(t *testing.T) {
	ctx := context.Background()
	f := newCheckoutCouponFixture(t)
	d, _ := newCheckoutPaymentDeps(t)

	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	discount := testutil.CreateDiscount(t, tx,
		testutil.WithDiscountValue(20),
		testutil.WithEligibility(domain.DiscountEligibilitySubscribers))
	code := testutil.CreateCouponCode(t, tx, discount.ID,
		testutil.WithCouponCode("MEMBERS-"+uuid.New().String()[:8])).Code
	f.subscribeActive(t, tx)
	require.NoError(t, tx.Commit(ctx))

	apply := func(customerInBody bool, session *domain.Customer) checkoutApplyCouponResponse {
		t.Helper()
		req := checkoutApplyCouponRequest{CartID: f.cartID.String(), Code: code}
		if customerInBody {
			req.CustomerID = f.customer.ID.String()
		}
		body, err := json.Marshal(req)
		require.NoError(t, err)
		r := httptest.NewRequest(http.MethodPost, "/api/checkout/coupon", strings.NewReader(string(body)))
		if session != nil {
			r = r.WithContext(auth.WithCustomer(r.Context(), session))
		}
		w := httptest.NewRecorder()
		d.handleCheckoutApplyCoupon(w, r)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var resp checkoutApplyCouponResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		return resp
	}

	guest := apply(true, nil)
	assert.False(t, guest.Valid, "a member's customer ID in the body proves nothing")
	assert.Equal(t, "/account/login?next=%2Fcheckout", guest.SignInURL)

	member := apply(true, f.customer)
	assert.True(t, member.Valid, member.ErrorMessage)
	assert.Empty(t, member.SignInURL)
}

// subscribeActive gives the fixture's customer an active subscription, written
// straight to the table: membership reads only customer and status.
func (f checkoutCouponFixture) subscribeActive(t *testing.T, tx pgx.Tx) {
	t.Helper()
	ctx := context.Background()
	var planID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO subscription_plans (name, interval, interval_count, is_active)
		 VALUES ($1, 'month', 1, true) RETURNING id`, "Plan "+uuid.NewString()[:8]).Scan(&planID))
	now := time.Now()
	_, err := tx.Exec(ctx,
		`INSERT INTO subscriptions
		     (customer_id, plan_id, status, shipping_address_id, variant_id, quantity,
		      current_period_start, current_period_end, next_order_at)
		 VALUES ($1, $2, 'active', $3, $4, 1, $5, $6, $6)`,
		f.customer.ID, planID, f.addressID, f.variantID, now, now.AddDate(0, 1, 0))
	require.NoError(t, err)
}

// membersCodeOnCart creates a 20%-off members-only code, puts it on the
// fixture's cart, and gives the customer an active subscription. Committed.
func membersCodeOnCart(t *testing.T, f checkoutCouponFixture) (*domain.Discount, string) {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	discount := testutil.CreateDiscount(t, tx,
		testutil.WithDiscountValue(20),
		testutil.WithEligibility(domain.DiscountEligibilitySubscribers))
	coupon := testutil.CreateCouponCode(t, tx, discount.ID,
		testutil.WithCouponCode("MEMBERS-"+uuid.New().String()[:8]))
	f.subscribeActive(t, tx)
	orders := app.NewOrderService(store.NewOrderStore(nil), audit.NewAuditWriter(), metrics.NewRegistry())
	_, err = orders.UpdateCartDiscount(ctx, tx, f.cartID, &discount.ID, &coupon.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	return discount, coupon.Code
}

// The payment intent holds a members-only code to the session in both of its
// places: phase 1 prices it, phase 3 places the order. Dropping the session
// from either would refuse the code to every member — phase 1 by pricing it
// out, phase 3 by refusing the order.
func TestCheckoutPaymentIntent_SubscribersOnly(t *testing.T) {
	t.Run("a signed-in member is charged the discounted price", func(t *testing.T) {
		f := newCheckoutCouponFixture(t)
		d, _ := newCheckoutPaymentDeps(t)
		membersCodeOnCart(t, f)

		w := f.postIntentAs(t, d, f.customer)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		resp := decodeCheckoutIntentResponse(t, w)
		assert.Equal(t, 1000, resp.DiscountTotal, "20% of 5000")
		assert.Empty(t, resp.CouponNotice)
	})

	t.Run("a guest has it dropped, told why, and given a way to sign in", func(t *testing.T) {
		f := newCheckoutCouponFixture(t)
		d, _ := newCheckoutPaymentDeps(t)
		membersCodeOnCart(t, f)

		w := f.postIntentAs(t, d, nil)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		resp := decodeCheckoutIntentResponse(t, w)
		assert.Zero(t, resp.DiscountTotal)
		assert.Contains(t, resp.CouponNotice, "Sign in")
		assert.Equal(t, "/account/login?next=%2Fcheckout", resp.CouponSignInURL)
	})
}

// The cart endpoint restores an applied code on reload, and for a members-only
// code that depends on who is signed in.
func TestCheckoutCart_SubscribersOnlyCodeFollowsTheSession(t *testing.T) {
	f := newCheckoutCouponFixture(t)
	d, _ := newCheckoutPaymentDeps(t)
	_, code := membersCodeOnCart(t, f)

	get := func(session *domain.Customer) checkoutCartResponse {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/checkout/cart", nil)
		r.AddCookie(&http.Cookie{Name: cartCookieName, Value: f.cartID.String()})
		if session != nil {
			r = r.WithContext(auth.WithCustomer(r.Context(), session))
		}
		w := httptest.NewRecorder()
		d.handleCheckoutCart(w, r)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var resp checkoutCartResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		return resp
	}

	signedIn := get(f.customer)
	require.NotNil(t, signedIn.Coupon, "a signed-in member sees their code applied")
	assert.Equal(t, code, signedIn.Coupon.Code)

	assert.Nil(t, get(nil).Coupon, "a guest does not")
}
