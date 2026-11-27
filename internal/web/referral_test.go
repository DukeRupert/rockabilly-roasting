package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/testutil"
)

// The referral program through its handlers: staff choose the offer, a member
// finds their code on the subscriptions page, a friend signs up with it, and
// the bag it earns is worked off the referrals page.
//
// Commits, like everything driving a handler here — see setup_test.go. Only
// one discount can be the referral offer, so each test makes its own the
// offer, which unmarks whatever an earlier test left.

// makeReferralOffer creates a WELCOME-shaped discount and makes it the offer
// through the admin handler, as staff would.
func makeReferralOffer(t *testing.T, d *Deps) uuid.UUID {
	t.Helper()
	code := welcomeCode(t)
	id := discountByCode(t, d, code).ID.String()
	w := postDiscountForm(t, d.handleAdminDiscountReferralSet, "/admin/discounts/"+id+"/referral", url.Values{}, id)
	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
	require.NotContains(t, w.Header().Get("Location"), "error=")
	return uuid.MustParse(id)
}

var memberCodeOnPage = regexp.MustCompile(`data-code="([A-Z]+-[A-Z0-9]{4})"`)

func subscriptionsPageAs(t *testing.T, d *Deps, customer *domain.Customer) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/account/subscriptions", nil)
	r = r.WithContext(auth.WithCustomer(r.Context(), customer))
	w := httptest.NewRecorder()
	d.handleAccountSubscriptions(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	return w.Body.String()
}

func TestReferral_EndToEnd(t *testing.T) {
	d := newAccountBoxDeps(t)
	d.PaymentProvider = &fakePaymentProvider{}
	makeReferralOffer(t, d)
	member := newManageWebFixture(t) // an active member

	// The member's page shows their code, the offer, and no friends yet.
	page := subscriptionsPageAs(t, d, member.customer)
	assert.Contains(t, page, "Share the Daily Grind")
	assert.Contains(t, page, "50% off")
	assert.Contains(t, page, "No friends have joined with your code yet.")
	m := memberCodeOnPage.FindStringSubmatch(page)
	require.Len(t, m, 2, "the page carries the member's code")
	code := m[1]
	assert.Equal(t, m[1], memberCodeOnPage.FindStringSubmatch(subscriptionsPageAs(t, d, member.customer))[1],
		"the same code on every visit")

	// A friend signs up with it and is charged the discounted price.
	sub := newSubscribeFixture(t)
	resp := decodeSubscribeIntent(t, postSubscribePaymentIntent(t, d,
		subscribePaymentIntentBody(sub.planID, sub.variantID, 1, withCoupon(code))))
	require.Equal(t, code, resp.CouponCode, resp.CouponNotice)
	assert.Equal(t, 810, resp.DiscountTotal)

	// Payment captured: the member is owed a bag.
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	_, transitioned, err := d.CheckoutService.ConfirmCheckoutPayment(ctx, tx,
		strings.TrimSuffix(resp.ClientSecret, "_secret"), testutil.TestActor())
	require.NoError(t, err)
	require.True(t, transitioned)
	require.NoError(t, tx.Commit(ctx))

	assert.Contains(t, subscriptionsPageAs(t, d, member.customer), "1 friend has joined with your code.")

	// Staff see it on the referrals page and mark it sent.
	w := httptest.NewRecorder()
	d.handleAdminReferralList(w, httptest.NewRequest(http.MethodGet, "/admin/referrals", nil))
	require.Equal(t, http.StatusOK, w.Code)
	owedSection := strings.SplitN(w.Body.String(), "Recently sent", 2)[0]
	require.Contains(t, owedSection, code, "the bag is listed as owed")
	rewardID := regexp.MustCompile(`/admin/referrals/([0-9a-f-]{36})/sent`).FindStringSubmatch(owedSection)
	require.Len(t, rewardID, 2)

	w = postDiscountForm(t, d.handleAdminReferralSent, "/admin/referrals/"+rewardID[1]+"/sent", url.Values{}, rewardID[1])
	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())

	w = httptest.NewRecorder()
	d.handleAdminReferralList(w, httptest.NewRequest(http.MethodGet, "/admin/referrals", nil))
	assert.NotContains(t, strings.SplitN(w.Body.String(), "Recently sent", 2)[0], "/admin/referrals/"+rewardID[1]+"/sent",
		"no longer owed")
}

// A member trying their own code at signup is told it is for a friend.
func TestReferral_OwnCodeAtSignup(t *testing.T) {
	d := newAccountBoxDeps(t)
	d.PaymentProvider = &fakePaymentProvider{}
	makeReferralOffer(t, d)
	member := newManageWebFixture(t)
	code := memberCodeOnPage.FindStringSubmatch(subscriptionsPageAs(t, d, member.customer))[1]

	sub := newSubscribeFixture(t)
	resp := decodeSubscribeIntent(t, postSubscribePaymentIntent(t, d,
		subscribePaymentIntentBody(sub.planID, sub.variantID, 1, withCoupon(code), withEmail(member.customer.Email))))
	assert.Zero(t, resp.DiscountTotal)
	assert.Contains(t, resp.CouponNotice, "your own referral code")
}

// Customers who are not active members see no card.
func TestReferral_NoCardWithoutAnActiveSubscription(t *testing.T) {
	d := newAccountBoxDeps(t)
	makeReferralOffer(t, d)

	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	stranger := testutil.CreateCustomer(t, tx)
	require.NoError(t, tx.Commit(ctx))

	assert.NotContains(t, subscriptionsPageAs(t, d, stranger), "Share the Daily Grind")
}

// A discount that fails the offer's rules is refused, and the handler says why.
func TestAdminDiscountReferralSet_RefusesASingleUseDiscount(t *testing.T) {
	d := newDiscountAdminDeps(t)
	code := "SINGLE" + strings.ToUpper(uuid.NewString()[:8])
	w := postDiscountForm(t, d.handleAdminDiscountCreate, "/admin/discounts", url.Values{
		"name": {"Single"}, "code": {code}, "type": {"percentage"}, "value": {"10"},
	}, "")
	require.Equal(t, http.StatusSeeOther, w.Code)
	id := discountByCode(t, d, code).ID.String()

	w = postDiscountForm(t, d.handleAdminDiscountReferralSet, "/admin/discounts/"+id+"/referral", url.Values{}, id)
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "error=")
	assert.False(t, discountByCode(t, d, code).IsReferral)
}

// The edit form cannot move the offer off the first subscription order, and
// a hand-made request that tries is refused with a reason.
func TestAdminDiscountUpdate_KeepsTheOfferOnSignups(t *testing.T) {
	d := newAccountBoxDeps(t)
	id := makeReferralOffer(t, d).String()

	w := postDiscountForm(t, d.handleAdminDiscountUpdate, "/admin/discounts/"+id, url.Values{
		"name": {"Friends"}, "type": {"percentage"}, "value": {"50"}, "applies_to": {"one_time"},
	}, id)
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "error=")

	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	got, err := d.DiscountService.GetDiscount(ctx, tx, uuid.MustParse(id))
	require.NoError(t, err)
	assert.Equal(t, domain.DiscountAppliesToSubscriptionSignup, got.AppliesTo)
	assert.True(t, got.IsReferral)
}
