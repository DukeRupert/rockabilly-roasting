package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// Promo links: ?promo=CODE on any storefront page keeps the code for checkout.
//
// Commits, like everything driving a handler here — see setup_test.go.

// promoCookie is the rr_promo cookie a response set, or nil.
func promoCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == promoCookieName {
			return c
		}
	}
	return nil
}

func withPending(r *http.Request, code string) *http.Request {
	r.AddCookie(&http.Cookie{Name: promoCookieName, Value: code})
	return r
}

func TestCapturePromoParam(t *testing.T) {
	reached := false
	h := capturePromoParam(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	serve := func(method, target string) *httptest.ResponseRecorder {
		reached = false
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, target, nil))
		return w
	}

	t.Run("keeps the code, uppercased, and redirects to the page without it", func(t *testing.T) {
		w := serve(http.MethodGet, "/subscriptions?utm_source=email&promo=welcome50")
		assert.False(t, reached)
		require.Equal(t, http.StatusFound, w.Code)
		assert.Equal(t, "/subscriptions?utm_source=email", w.Header().Get("Location"), "other parameters survive")

		c := promoCookie(w)
		require.NotNil(t, c)
		assert.Equal(t, "WELCOME50", c.Value)
		assert.Equal(t, "/", c.Path)
		assert.True(t, c.HttpOnly)
		assert.Equal(t, http.SameSiteLaxMode, c.SameSite, "Lax, or a link from an email arrives without it")
		assert.Positive(t, c.MaxAge)
	})

	t.Run("a referral code's shape is a code, and so is a staff code with an underscore", func(t *testing.T) {
		for raw, want := range map[string]string{"LOGAN-7K3Q": "LOGAN-7K3Q", " gift_20 ": "GIFT_20"} {
			c := promoCookie(serve(http.MethodGet, "/?promo="+url.QueryEscape(raw)))
			require.NotNil(t, c, "%q", raw)
			assert.Equal(t, want, c.Value)
		}
	})

	t.Run("the redirect can only name this site", func(t *testing.T) {
		// The path as the client sent it: this runs before ServeMux cleans it.
		for target, want := range map[string]string{
			"//evil.example/?promo=X":  "/evil.example/",
			"/\\evil.example/?promo=X": "/evil.example/",
			"///evil.example?promo=X":  "/evil.example",
		} {
			w := serve(http.MethodGet, target)
			require.Equal(t, http.StatusFound, w.Code, target)
			assert.Equal(t, want, w.Header().Get("Location"), target)
		}
	})

	t.Run("something that cannot be a code is dropped, and the URL still cleaned", func(t *testing.T) {
		for _, bad := range []string{"", "<script>", "GIFT 20", strings.Repeat("A", 65), "-LEADING"} {
			w := serve(http.MethodGet, "/catalog?promo="+url.QueryEscape(bad))
			assert.Nil(t, promoCookie(w), "%q", bad)
			assert.Equal(t, "/catalog", w.Header().Get("Location"), "%q", bad)
		}
	})

	t.Run("not a promo link: passes straight through", func(t *testing.T) {
		for _, tc := range []struct{ method, target string }{
			{http.MethodGet, "/catalog"},
			{http.MethodPost, "/cart/add?promo=GIFT20"},
			{http.MethodGet, "/admin/discounts?promo=GIFT20"},
			{http.MethodGet, "/api/checkout/cart?promo=GIFT20"},
			{http.MethodGet, "/webhooks/x?promo=GIFT20"},
		} {
			w := serve(tc.method, tc.target)
			assert.True(t, reached, "%s %s", tc.method, tc.target)
			assert.Nil(t, promoCookie(w), "%s %s", tc.method, tc.target)
		}
	})
}

// clearCartCoupon takes the fixture's applied coupon off its cart, the state a
// shopper arriving from a promo link is in.
func clearCartCoupon(t *testing.T, f checkoutCouponFixture) {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	orders := app.NewOrderService(store.NewOrderStore(nil), audit.NewAuditWriter(), metrics.NewRegistry())
	_, err = orders.UpdateCartDiscount(ctx, tx, f.cartID, nil, nil)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}

func TestCheckoutCart_OffersThePendingPromo(t *testing.T) {
	f := newCheckoutCouponFixture(t)
	d, _ := newCheckoutPaymentDeps(t)
	get := func() checkoutCartResponse {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/checkout/cart", nil)
		r.AddCookie(&http.Cookie{Name: cartCookieName, Value: f.cartID.String()})
		w := httptest.NewRecorder()
		d.handleCheckoutCart(w, withPending(r, "GIFT20"))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var resp checkoutCartResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		return resp
	}

	assert.Empty(t, get().PendingCoupon, "a cart that already holds a code is left alone")

	clearCartCoupon(t, f)
	assert.Equal(t, "GIFT20", get().PendingCoupon)
}

func TestCheckoutApplyCoupon_SettlesThePendingPromo(t *testing.T) {
	d, _ := newCheckoutPaymentDeps(t)
	apply := func(f checkoutCouponFixture, code, pending string) (*httptest.ResponseRecorder, checkoutApplyCouponResponse) {
		t.Helper()
		body, err := json.Marshal(checkoutApplyCouponRequest{CartID: f.cartID.String(), Code: code})
		require.NoError(t, err)
		w := httptest.NewRecorder()
		d.handleCheckoutApplyCoupon(w, withPending(
			httptest.NewRequest(http.MethodPost, "/api/checkout/coupon", strings.NewReader(string(body))), pending))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var resp checkoutApplyCouponResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		return w, resp
	}
	// cleared: the response deletes the cookie, at the path it was set on —
	// a delete at another path leaves the cookie in place.
	cleared := func(w *httptest.ResponseRecorder) bool {
		c := promoCookie(w)
		return c != nil && c.MaxAge < 0 && c.Path == "/"
	}

	t.Run("applied: the cart holds it now", func(t *testing.T) {
		f := newCheckoutCouponFixture(t)
		clearCartCoupon(t, f)
		w, resp := apply(f, f.code, f.code)
		require.True(t, resp.Valid)
		assert.True(t, cleared(w))
	})

	t.Run("applied by hand instead: the cart holds a code, so the promo is done", func(t *testing.T) {
		f := newCheckoutCouponFixture(t)
		clearCartCoupon(t, f)
		w, resp := apply(f, f.code, "GIFT20")
		require.True(t, resp.Valid)
		assert.True(t, cleared(w))
	})

	t.Run("refused for good: stop offering it", func(t *testing.T) {
		f := newCheckoutCouponFixture(t)
		clearCartCoupon(t, f)
		w, resp := apply(f, "NOSUCHCODE", "NOSUCHCODE")
		require.False(t, resp.Valid)
		assert.True(t, cleared(w))
	})

	t.Run("refused until the shopper signs in: keep it for after", func(t *testing.T) {
		f := newCheckoutCouponFixture(t)
		clearCartCoupon(t, f)
		code := newSignupCode(t, testutil.WithEligibility("subscribers"))
		w, resp := apply(f, code, code)
		require.False(t, resp.Valid)
		require.NotEmpty(t, resp.SignInURL)
		assert.False(t, cleared(w))
	})

	t.Run("a signup code refused at the cart: kept for the subscribe page", func(t *testing.T) {
		f := newCheckoutCouponFixture(t)
		clearCartCoupon(t, f)
		code := welcomeCode(t)
		w, resp := apply(f, code, code)
		require.False(t, resp.Valid)
		assert.Nil(t, promoCookie(w))
	})

	t.Run("a different code typed and refused leaves the promo alone", func(t *testing.T) {
		f := newCheckoutCouponFixture(t)
		clearCartCoupon(t, f)
		w, resp := apply(f, "NOSUCHCODE", "GIFT20")
		require.False(t, resp.Valid)
		assert.Nil(t, promoCookie(w))
	})
}

func TestSubscribe_PendingPromo(t *testing.T) {
	f := newSubscribeFixture(t)

	t.Run("the page starts with it filled in", func(t *testing.T) {
		d, _ := newSubscribePaymentDeps(t)
		target := "/subscribe?line=" + url.QueryEscape(f.planID.String()+":"+f.variantID.String()+":1")
		w := httptest.NewRecorder()
		props, ok := d.subscribePageProps(w, withPending(httptest.NewRequest(http.MethodGet, target, nil), "WELCOME50"))
		require.True(t, ok, w.Body.String())
		assert.Equal(t, "WELCOME50", props.PendingCoupon)
	})

	t.Run("accepted: kept, as a signup has no cart to hold it", func(t *testing.T) {
		d, _ := newSubscribePaymentDeps(t)
		code := welcomeCode(t)
		r := httptest.NewRequest(http.MethodPost, "/api/subscribe/payment-intent",
			strings.NewReader(subscribePaymentIntentBody(f.planID, f.variantID, 1, withCoupon(code))))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		d.handleSubscribePaymentIntent(w, withPending(r, code))
		resp := decodeSubscribeIntent(t, w)
		require.Equal(t, code, resp.CouponCode)
		assert.Nil(t, promoCookie(w))
	})

	post := func(t *testing.T, code, pending string) (*httptest.ResponseRecorder, subscribePaymentIntentResponse) {
		t.Helper()
		d, _ := newSubscribePaymentDeps(t)
		r := httptest.NewRequest(http.MethodPost, "/api/subscribe/payment-intent",
			strings.NewReader(subscribePaymentIntentBody(f.planID, f.variantID, 1, withCoupon(code))))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		d.handleSubscribePaymentIntent(w, withPending(r, pending))
		return w, decodeSubscribeIntent(t, w)
	}

	t.Run("a code no checkout would take: stop filling it in", func(t *testing.T) {
		w, resp := post(t, "NOSUCHCODE", "NOSUCHCODE")
		require.NotEmpty(t, resp.CouponNotice)
		c := promoCookie(w)
		require.NotNil(t, c)
		assert.Negative(t, c.MaxAge)
		assert.Equal(t, "/", c.Path)
	})

	t.Run("a cart code refused on a signup: kept for cart checkout", func(t *testing.T) {
		code := newSignupCode(t)
		w, resp := post(t, code, code)
		require.NotEmpty(t, resp.CouponNotice)
		assert.Nil(t, promoCookie(w))
	})

	t.Run("refused until the shopper signs in: kept", func(t *testing.T) {
		code := newSignupCode(t,
			testutil.WithAppliesTo(domain.DiscountAppliesToSubscriptionSignup),
			testutil.WithEligibility(domain.DiscountEligibilitySubscribers))
		w, resp := post(t, code, code)
		require.NotEmpty(t, resp.CouponSignInURL)
		assert.Nil(t, promoCookie(w))
	})

	t.Run("a different code refused leaves the promo alone", func(t *testing.T) {
		w, resp := post(t, "NOSUCHCODE", "WELCOME50")
		require.NotEmpty(t, resp.CouponNotice)
		assert.Nil(t, promoCookie(w))
	})
}
