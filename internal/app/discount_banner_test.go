package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/testutil"
)

// The storefront's offer strip: staff's banner line for each live discount,
// shown only to the shoppers who could use it.

// bannerDiscount creates a discount promoted with a line unique to this test,
// and returns that line.
func bannerDiscount(t *testing.T, tx pgx.Tx, svc *app.DiscountService, opts ...testutil.DiscountOption) (*domain.Discount, string) {
	t.Helper()
	d := testutil.CreateDiscount(t, tx, opts...)
	text := "Offer " + uuid.NewString()[:8]
	require.NoError(t, svc.SetBannerText(context.Background(), tx, d.ID, text, testutil.TestActor()))
	return d, text
}

// bannerTexts is what the strip says to customer (nil for a guest).
func bannerTexts(t *testing.T, tx pgx.Tx, svc *app.DiscountService, customer *domain.Customer) []string {
	t.Helper()
	banners, err := svc.StorefrontBanners(context.Background(), tx, customer)
	require.NoError(t, err)
	out := make([]string, 0, len(banners))
	for _, b := range banners {
		out = append(out, b.Text)
	}
	return out
}

func bannerHref(t *testing.T, tx pgx.Tx, svc *app.DiscountService, customer *domain.Customer, text string) string {
	t.Helper()
	banners, err := svc.StorefrontBanners(context.Background(), tx, customer)
	require.NoError(t, err)
	for _, b := range banners {
		if b.Text == text {
			return b.Href
		}
	}
	t.Fatalf("no banner %q", text)
	return ""
}

func TestStorefrontBanners_Audience(t *testing.T) {
	svc := newDiscountService()

	t.Run("an open offer is shown to everyone and leads to the shop", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, text := bannerDiscount(t, tx, svc)
		s := newShopper(t, tx)

		assert.Contains(t, bannerTexts(t, tx, svc, nil), text)
		assert.Contains(t, bannerTexts(t, tx, svc, s.customer), text)
		assert.Equal(t, "/catalog", bannerHref(t, tx, svc, nil, text))
	})

	t.Run("a members-only offer is shown only to signed-in active members", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, text := bannerDiscount(t, tx, svc, testutil.WithEligibility(domain.DiscountEligibilitySubscribers))
		member := newShopper(t, tx)
		member.subscribe(t, tx, domain.SubscriptionStatusActive)
		paused := newShopper(t, tx)
		paused.subscribe(t, tx, domain.SubscriptionStatusPaused)

		assert.NotContains(t, bannerTexts(t, tx, svc, nil), text, "a guest")
		assert.NotContains(t, bannerTexts(t, tx, svc, newShopper(t, tx).customer), text, "a customer who never joined")
		assert.NotContains(t, bannerTexts(t, tx, svc, paused.customer), text, "a paused member, whom checkout refuses too")
		assert.Contains(t, bannerTexts(t, tx, svc, member.customer), text)
	})

	t.Run("a new-subscriber offer is hidden only from customers who have subscribed", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, text := bannerDiscount(t, tx, svc,
			testutil.WithAppliesTo(domain.DiscountAppliesToSubscriptionSignup),
			testutil.WithEligibility(domain.DiscountEligibilityNewSubscribers))
		former := newShopper(t, tx)
		former.subscribe(t, tx, domain.SubscriptionStatusCancelled)

		assert.Contains(t, bannerTexts(t, tx, svc, nil), text, "a guest may be new")
		assert.Contains(t, bannerTexts(t, tx, svc, newShopper(t, tx).customer), text)
		assert.NotContains(t, bannerTexts(t, tx, svc, former.customer), text, "a cancelled subscription still counts")
		assert.Equal(t, "/subscriptions", bannerHref(t, tx, svc, nil, text), "a signup code is used from the Daily Grind page")
	})

	t.Run("the referral offer is shown to members, who share it, not to the friends it discounts", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		d, text := bannerDiscount(t, tx, svc,
			testutil.WithAppliesTo(domain.DiscountAppliesToSubscriptionSignup),
			testutil.WithEligibility(domain.DiscountEligibilityNewSubscribers),
			testutil.WithUsageLimit(domain.CouponUsageOncePerCustomer))
		require.NoError(t, svc.SetReferralOffer(context.Background(), tx, d.ID, testutil.TestActor()))
		member := newShopper(t, tx)
		member.subscribe(t, tx, domain.SubscriptionStatusActive)

		assert.NotContains(t, bannerTexts(t, tx, svc, nil), text)
		assert.NotContains(t, bannerTexts(t, tx, svc, newShopper(t, tx).customer), text)
		assert.Contains(t, bannerTexts(t, tx, svc, member.customer), text)
		assert.Equal(t, "/account/subscriptions", bannerHref(t, tx, svc, member.customer, text), "where their own code is")
	})

	t.Run("a wholesale account sees no retail offers", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, text := bannerDiscount(t, tx, svc)
		wholesale := *newShopper(t, tx).customer
		wholesale.AccountType = domain.AccountTypeWholesale

		assert.NotContains(t, bannerTexts(t, tx, svc, &wholesale), text)
	})
}

func TestStorefrontBanners_OnlyLiveDiscounts(t *testing.T) {
	svc := newDiscountService()
	now := time.Now()
	for name, opts := range map[string][]testutil.DiscountOption{
		"inactive":    {testutil.WithDiscountActive(false)},
		"not started": {testutil.WithDiscountStart(now.Add(time.Hour))},
		"expired":     {testutil.WithDiscountExpiry(now.Add(-time.Hour))},
	} {
		t.Run(name, func(t *testing.T) {
			tx := testutil.NewTestTx(t, testPool)
			_, text := bannerDiscount(t, tx, svc, opts...)
			assert.NotContains(t, bannerTexts(t, tx, svc, nil), text)
		})
	}

	t.Run("a discount without a banner line is not promoted", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		d, _ := bannerDiscount(t, tx, svc)
		require.NoError(t, svc.SetBannerText(context.Background(), tx, d.ID, "  ", testutil.TestActor()))
		banners, err := svc.StorefrontBanners(context.Background(), tx, nil)
		require.NoError(t, err)
		for _, b := range banners {
			assert.NotEmpty(t, b.Text, "an unpromoted discount would be an empty, linked line")
		}
	})

	t.Run("soonest to end comes first", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, later := bannerDiscount(t, tx, svc, testutil.WithDiscountExpiry(now.AddDate(0, 0, 20)))
		_, sooner := bannerDiscount(t, tx, svc, testutil.WithDiscountExpiry(now.AddDate(0, 0, 10)))
		_, never := bannerDiscount(t, tx, svc)

		var order []string
		for _, text := range bannerTexts(t, tx, svc, nil) {
			if text == later || text == sooner || text == never {
				order = append(order, text)
			}
		}
		assert.Equal(t, []string{sooner, later, never}, order)
	})
}

func TestDiscountService_SetBannerText(t *testing.T) {
	ctx := context.Background()
	svc := newDiscountService()

	t.Run("collapses whitespace, so a pasted line break cannot split the strip", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		d := testutil.CreateDiscount(t, tx)
		require.NoError(t, svc.SetBannerText(ctx, tx, d.ID, "  20% off\n  with GIFT20 ", testutil.TestActor()))
		got, err := svc.GetDiscount(ctx, tx, d.ID)
		require.NoError(t, err)
		assert.Equal(t, "20% off with GIFT20", got.BannerText)
	})

	t.Run("refuses a line over the limit, counting characters not bytes", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		d := testutil.CreateDiscount(t, tx)
		assert.NoError(t, svc.SetBannerText(ctx, tx, d.ID, strings.Repeat("é", domain.MaxBannerTextLen), testutil.TestActor()))
		assert.ErrorIs(t, svc.SetBannerText(ctx, tx, d.ID, strings.Repeat("a", domain.MaxBannerTextLen+1), testutil.TestActor()),
			app.ErrBannerTextTooLong)
	})

	t.Run("is audited with what it said before", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		d := testutil.CreateDiscount(t, tx)
		require.NoError(t, svc.SetBannerText(ctx, tx, d.ID, "first", testutil.TestActor()))
		require.NoError(t, svc.SetBannerText(ctx, tx, d.ID, "second", testutil.TestActor()))
		require.NoError(t, svc.SetBannerText(ctx, tx, d.ID, "second", testutil.TestActor()))

		// One transaction stamps every row with the same time, so the test
		// asks for the change it expects rather than for the latest one.
		var n, changes int
		require.NoError(t, tx.QueryRow(ctx,
			`SELECT count(*), count(*) FILTER (WHERE metadata->>'from' = 'first' AND metadata->>'to' = 'second')
			 FROM audit_log WHERE action = 'discount.banner' AND resource_id = $1`, d.ID).Scan(&n, &changes))
		assert.Equal(t, 2, n, "saving the same line again records nothing")
		assert.Equal(t, 1, changes, "the entry says what it said before")
	})

	t.Run("an unknown discount", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		assert.ErrorIs(t, svc.SetBannerText(ctx, tx, uuid.New(), "x", testutil.TestActor()), app.ErrDiscountNotFound)
	})
}

// A banner link carries the discount's code when there is exactly one it
// could mean, so checkout applies it; see web/promo_code.go.
func TestStorefrontBanners_LinkCarriesTheCode(t *testing.T) {
	svc := newDiscountService()

	t.Run("one shared code", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		d, text := bannerDiscount(t, tx, svc)
		code := testutil.CreateCouponCode(t, tx, d.ID, testutil.WithCouponCode("GIFT-"+strings.ToUpper(uuid.NewString()[:6]))).Code
		assert.Equal(t, "/catalog?promo="+code, bannerHref(t, tx, svc, nil, text))
	})

	t.Run("a signup code goes to the Daily Grind page with it", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		d, text := bannerDiscount(t, tx, svc, testutil.WithAppliesTo(domain.DiscountAppliesToSubscriptionSignup))
		code := testutil.CreateCouponCode(t, tx, d.ID).Code
		assert.Equal(t, "/subscriptions?promo="+code, bannerHref(t, tx, svc, nil, text))
	})

	t.Run("more than one code: the link cannot pick", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		d, text := bannerDiscount(t, tx, svc)
		testutil.CreateCouponCode(t, tx, d.ID)
		testutil.CreateCouponCode(t, tx, d.ID)
		assert.Equal(t, "/catalog", bannerHref(t, tx, svc, nil, text))
	})

	t.Run("a member's own code on it does not count against the shared one", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		d, text := bannerDiscount(t, tx, svc)
		code := testutil.CreateCouponCode(t, tx, d.ID).Code
		owned := testutil.CreateCouponCode(t, tx, d.ID)
		_, err := tx.Exec(context.Background(), `UPDATE coupon_codes SET customer_id = $2 WHERE id = $1`,
			owned.ID, newShopper(t, tx).customer.ID)
		require.NoError(t, err)
		assert.Equal(t, "/catalog?promo="+code, bannerHref(t, tx, svc, nil, text))
	})

	t.Run("a spent single-use code is not carried", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		d, text := bannerDiscount(t, tx, svc)
		cc := testutil.CreateCouponCode(t, tx, d.ID)
		_, err := tx.Exec(context.Background(), `UPDATE coupon_codes SET redeemed_at = now() WHERE id = $1`, cc.ID)
		require.NoError(t, err)
		assert.Equal(t, "/catalog", bannerHref(t, tx, svc, nil, text))
	})

	t.Run("the referral offer never carries one: each member shares their own", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		d, text := bannerDiscount(t, tx, svc,
			testutil.WithAppliesTo(domain.DiscountAppliesToSubscriptionSignup),
			testutil.WithUsageLimit(domain.CouponUsageOncePerCustomer))
		testutil.CreateCouponCode(t, tx, d.ID)
		require.NoError(t, svc.SetReferralOffer(context.Background(), tx, d.ID, testutil.TestActor()))
		member := newShopper(t, tx)
		member.subscribe(t, tx, domain.SubscriptionStatusActive)
		assert.Equal(t, "/account/subscriptions", bannerHref(t, tx, svc, member.customer, text))
	})
}
