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
	"github.com/dukerupert/hiri/internal/platform/auth"
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
	var planID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO subscription_plans (name, interval, interval_count, is_active)
		 VALUES ($1, 'month', 1, true) RETURNING id`, "Plan "+uuid.NewString()[:8]).Scan(&planID))
	now := time.Now()
	_, err = tx.Exec(ctx,
		`INSERT INTO subscriptions
		     (customer_id, plan_id, status, shipping_address_id, variant_id, quantity,
		      current_period_start, current_period_end, next_order_at)
		 VALUES ($1, $2, 'active', $3, $4, 1, $5, $6, $6)`,
		f.customer.ID, planID, f.addressID, f.variantID, now, now.AddDate(0, 1, 0))
	require.NoError(t, err)
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
