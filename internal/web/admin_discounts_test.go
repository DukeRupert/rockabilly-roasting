package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// The admin forms are the only way staff set a discount's usage limit and
// eligibility, so each field has to reach the discount it names.
//
// Commits, like everything driving a handler here — see setup_test.go.

func newDiscountAdminDeps(t *testing.T) *Deps {
	t.Helper()
	d := newTestDeps()
	d.DiscountService = app.NewDiscountService(store.NewDiscountStore(), audit.NewAuditWriter(), metrics.NewRegistry())
	return d
}

func postDiscountForm(t *testing.T, handler http.HandlerFunc, path string, form url.Values, pathID string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if pathID != "" {
		r.SetPathValue("id", pathID)
	}
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

func discountByCode(t *testing.T, d *Deps, code string) *domain.Discount {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	discounts := store.NewDiscountStore()
	cc, err := discounts.GetCouponCodeByCode(ctx, tx, code)
	require.NoError(t, err)
	got, err := discounts.GetByID(ctx, tx, cc.DiscountID)
	require.NoError(t, err)
	return got
}

func TestAdminDiscountCreate_UsageAndEligibility(t *testing.T) {
	d := newDiscountAdminDeps(t)

	t.Run("both fields reach the discount", func(t *testing.T) {
		code := "GIFT" + strings.ToUpper(uuid.NewString()[:8])
		w := postDiscountForm(t, d.handleAdminDiscountCreate, "/admin/discounts", url.Values{
			"name": {"Gift 20"}, "code": {code}, "type": {"percentage"}, "value": {"20"},
			"usage_limit": {"once_per_customer"}, "eligibility": {"subscribers"},
		}, "")
		require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())

		got := discountByCode(t, d, code)
		assert.Equal(t, domain.CouponUsageOncePerCustomer, got.UsageLimit)
		assert.Equal(t, domain.DiscountEligibilitySubscribers, got.Eligibility)
	})

	t.Run("a signup code for new subscribers reaches the discount", func(t *testing.T) {
		code := "WELCOME" + strings.ToUpper(uuid.NewString()[:8])
		w := postDiscountForm(t, d.handleAdminDiscountCreate, "/admin/discounts", url.Values{
			"name": {"Welcome 50"}, "code": {code}, "type": {"percentage"}, "value": {"50"},
			"usage_limit": {"once_per_customer"}, "eligibility": {"new_subscribers"},
			"applies_to": {"subscription_signup"},
		}, "")
		require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())

		got := discountByCode(t, d, code)
		assert.Equal(t, domain.DiscountEligibilityNewSubscribers, got.Eligibility)
		assert.Equal(t, domain.DiscountAppliesToSubscriptionSignup, got.AppliesTo)
	})

	t.Run("left out, they default to single-use for anyone", func(t *testing.T) {
		code := "PLAIN" + strings.ToUpper(uuid.NewString()[:8])
		w := postDiscountForm(t, d.handleAdminDiscountCreate, "/admin/discounts", url.Values{
			"name": {"Plain"}, "code": {code}, "type": {"percentage"}, "value": {"10"},
		}, "")
		require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())

		got := discountByCode(t, d, code)
		assert.Equal(t, domain.CouponUsageSingleUse, got.UsageLimit)
		assert.Equal(t, domain.DiscountEligibilityAnyone, got.Eligibility)
		assert.Equal(t, domain.DiscountAppliesToOneTime, got.AppliesTo)
	})

	t.Run("a value that is not one of the options is refused", func(t *testing.T) {
		code := "BAD" + strings.ToUpper(uuid.NewString()[:8])
		w := postDiscountForm(t, d.handleAdminDiscountCreate, "/admin/discounts", url.Values{
			"name": {"Bad"}, "code": {code}, "type": {"percentage"}, "value": {"10"},
			"eligibility": {"everyone-please"},
		}, "")
		assert.NotEqual(t, http.StatusSeeOther, w.Code)
	})
}

func TestAdminDiscountUpdate_Eligibility(t *testing.T) {
	d := newDiscountAdminDeps(t)
	code := "EDIT" + strings.ToUpper(uuid.NewString()[:8])
	w := postDiscountForm(t, d.handleAdminDiscountCreate, "/admin/discounts", url.Values{
		"name": {"Edit me"}, "code": {code}, "type": {"percentage"}, "value": {"20"},
	}, "")
	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
	id := discountByCode(t, d, code).ID.String()

	edit := url.Values{"name": {"Edit me"}, "type": {"percentage"}, "value": {"20"}, "eligibility": {"subscribers"}}
	w = postDiscountForm(t, d.handleAdminDiscountUpdate, "/admin/discounts/"+id, edit, id)
	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
	assert.NotContains(t, w.Header().Get("Location"), "error=")
	assert.Equal(t, domain.DiscountEligibilitySubscribers, discountByCode(t, d, code).Eligibility)

	edit.Del("eligibility")
	w = postDiscountForm(t, d.handleAdminDiscountUpdate, "/admin/discounts/"+id, edit, id)
	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
	assert.Equal(t, domain.DiscountEligibilitySubscribers, discountByCode(t, d, code).Eligibility,
		"an edit that leaves the field out leaves it alone")
}

func TestAdminDiscountUpdate_AppliesTo(t *testing.T) {
	d := newDiscountAdminDeps(t)
	code := "KIND" + strings.ToUpper(uuid.NewString()[:8])
	w := postDiscountForm(t, d.handleAdminDiscountCreate, "/admin/discounts", url.Values{
		"name": {"Kind"}, "code": {code}, "type": {"percentage"}, "value": {"20"},
	}, "")
	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
	id := discountByCode(t, d, code).ID.String()

	edit := url.Values{"name": {"Kind"}, "type": {"percentage"}, "value": {"20"}, "applies_to": {"subscription_signup"}}
	w = postDiscountForm(t, d.handleAdminDiscountUpdate, "/admin/discounts/"+id, edit, id)
	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
	assert.NotContains(t, w.Header().Get("Location"), "error=")
	assert.Equal(t, domain.DiscountAppliesToSubscriptionSignup, discountByCode(t, d, code).AppliesTo)
}
