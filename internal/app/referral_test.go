package app_test

import (
	"context"
	"regexp"
	"testing"
	"time"

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

// The referral program: one discount is the referral offer, every active
// member gets their own code on it, and a friend's paid first order with that
// code earns the member a free bag.

func newDiscountService() *app.DiscountService {
	return app.NewDiscountService(store.NewDiscountStore(), audit.NewAuditWriter(), metrics.NewRegistry())
}

// newReferralOffer creates a WELCOME50-shaped discount and makes it the
// referral offer.
func newReferralOffer(t *testing.T, tx pgx.Tx) *domain.Discount {
	t.Helper()
	d, _ := newWelcomeCode(t, tx) // the code typed at creation; ownerless
	require.NoError(t, newDiscountService().SetReferralOffer(context.Background(), tx, d.ID, testutil.TestActor()))
	d.IsReferral = true
	return d
}

// member is an active Daily Grind member with their referral code.
func newMember(t *testing.T, tx pgx.Tx) (shopper, *domain.CouponCode) {
	t.Helper()
	m := newShopper(t, tx)
	m.subscribe(t, tx, domain.SubscriptionStatusActive)
	code, offer, err := newDiscountService().MemberReferralCode(context.Background(), tx, m.customer)
	require.NoError(t, err)
	require.NotNil(t, offer)
	require.NotNil(t, code)
	return m, code
}

func rewardsFor(t *testing.T, tx pgx.Tx, referrer shopper) []string {
	t.Helper()
	rows, err := tx.Query(context.Background(),
		`SELECT status FROM referral_rewards WHERE referrer_customer_id = $1 ORDER BY created_at`, referrer.customer.ID)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		out = append(out, s)
	}
	return out
}

func TestReferral_MemberCodes(t *testing.T) {
	ctx := context.Background()
	svc := newDiscountService()

	t.Run("an active member gets one code, named for them, and keeps it", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		offer := newReferralOffer(t, tx)
		m, code := newMember(t, tx)

		assert.Equal(t, offer.ID, code.DiscountID)
		require.NotNil(t, code.CustomerID)
		assert.Equal(t, m.customer.ID, *code.CustomerID)
		assert.Regexp(t, regexp.MustCompile(`^[A-Z]{1,8}-[ABCDEFGHJKMNPQRSTUVWXYZ2-9]{4}$`), code.Code)

		again, _, err := svc.MemberReferralCode(ctx, tx, m.customer)
		require.NoError(t, err)
		assert.Equal(t, code.ID, again.ID, "the same code every visit")
	})

	t.Run("nothing to share without an active offer or an active membership", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		offer := newReferralOffer(t, tx)

		paused := newShopper(t, tx)
		paused.subscribe(t, tx, domain.SubscriptionStatusPaused)
		code, _, err := svc.MemberReferralCode(ctx, tx, paused.customer)
		require.NoError(t, err)
		assert.Nil(t, code, "a paused member is not an active one")

		active := newShopper(t, tx)
		active.subscribe(t, tx, domain.SubscriptionStatusActive)
		require.NoError(t, svc.SetActive(ctx, tx, offer.ID, false, testutil.TestActor()))
		code, _, err = svc.MemberReferralCode(ctx, tx, active.customer)
		require.NoError(t, err)
		assert.Nil(t, code, "an inactive offer has nothing to share")
	})

	t.Run("only a once-per-customer discount can be the offer, and there is one at a time", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		single := testutil.CreateDiscount(t, tx)
		assert.ErrorIs(t, svc.SetReferralOffer(ctx, tx, single.ID, testutil.TestActor()), app.ErrReferralOfferInvalid)

		first := newReferralOffer(t, tx)
		second, _ := newWelcomeCode(t, tx)
		require.NoError(t, svc.SetReferralOffer(ctx, tx, second.ID, testutil.TestActor()))

		got, err := svc.ReferralOffer(ctx, tx)
		require.NoError(t, err)
		assert.Equal(t, second.ID, got.ID)
		was, err := svc.GetDiscount(ctx, tx, first.ID)
		require.NoError(t, err)
		assert.False(t, was.IsReferral, "making a new offer unmarks the old one")
	})
}

func TestReferral_CodeRules(t *testing.T) {
	ctx := context.Background()
	svc := newCheckoutService()

	t.Run("the code typed when the offer was created names nobody, so it does not work", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		offer := newReferralOffer(t, tx)
		codes, err := store.NewDiscountStore().ListCouponCodesByDiscount(ctx, tx, offer.ID)
		require.NoError(t, err)
		require.Len(t, codes, 1)

		_, err = newShopper(t, tx).placeOrderKind(t, tx, svc, codes[0].Code, "pi_ref_ownerless", true)
		assert.ErrorIs(t, err, app.ErrCouponNotFound)
	})

	t.Run("a member cannot use their own code", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		newReferralOffer(t, tx)
		m, code := newMember(t, tx)

		_, _, err := svc.ApplyCoupon(ctx, tx, code.Code, 5000, app.CouponUse{SubscriptionSignup: true, CustomerID: &m.customer.ID})
		assert.ErrorIs(t, err, app.ErrCouponOwnCode)
	})

	t.Run("a member's code stops working when the offer ends", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		offer := newReferralOffer(t, tx)
		_, code := newMember(t, tx)
		require.NoError(t, newDiscountService().ClearReferralOffer(ctx, tx, offer.ID, testutil.TestActor()))

		_, err := newShopper(t, tx).placeOrderKind(t, tx, svc, code.Code, "pi_ref_ended", true)
		assert.ErrorIs(t, err, app.ErrCouponNotFound)
	})
}

func TestReferral_Rewards(t *testing.T) {
	ctx := context.Background()
	svc := newCheckoutService()
	discounts := newDiscountService()

	t.Run("a friend's paid first order earns the member a bag, once", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		newReferralOffer(t, tx)
		m, code := newMember(t, tx)
		friend := newShopper(t, tx)

		order, err := friend.placeOrderKind(t, tx, svc, code.Code, "pi_ref_friend", true)
		require.NoError(t, err)
		assert.Equal(t, 2500, order.DiscountTotal, "the friend gets the offer")
		assert.Empty(t, rewardsFor(t, tx, m), "nothing earned before payment")

		capture(t, tx, svc, "pi_ref_friend")
		assert.Equal(t, []string{"owed"}, rewardsFor(t, tx, m))

		// The out-of-order failure webhook, then a second success.
		_, err = store.NewOrderStore(nil).UpdateOrderPaymentStatus(ctx, tx, order.ID, domain.PaymentStatusFailed)
		require.NoError(t, err)
		capture(t, tx, svc, "pi_ref_friend")
		assert.Equal(t, []string{"owed"}, rewardsFor(t, tx, m), "capturing again earns no second bag")

		n, err := discounts.ReferralFriendsCount(ctx, tx, m.customer.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, n)
	})

	t.Run("a friend's second order, lost at capture, earns nothing more", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		newReferralOffer(t, tx)
		m, code := newMember(t, tx)
		friend := newShopper(t, tx)

		_, err := friend.placeOrderKind(t, tx, svc, code.Code, "pi_ref_tab_one", true)
		require.NoError(t, err)
		_, err = friend.placeOrderKind(t, tx, svc, code.Code, "pi_ref_tab_two", true)
		require.NoError(t, err)
		capture(t, tx, svc, "pi_ref_tab_one")
		capture(t, tx, svc, "pi_ref_tab_two")

		assert.Equal(t, []string{"owed"}, rewardsFor(t, tx, m))
	})

	t.Run("cancelling the friend's order takes the bag back, unless it was sent", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		newReferralOffer(t, tx)
		m, code := newMember(t, tx)
		orders := newOrderService().WithDiscounts(store.NewDiscountStore())

		unsent, err := newShopper(t, tx).placeOrderKind(t, tx, svc, code.Code, "pi_ref_cancel_owed", true)
		require.NoError(t, err)
		capture(t, tx, svc, "pi_ref_cancel_owed")

		sent, err := newShopper(t, tx).placeOrderKind(t, tx, svc, code.Code, "pi_ref_cancel_sent", true)
		require.NoError(t, err)
		capture(t, tx, svc, "pi_ref_cancel_sent")
		owed, err := discounts.ListReferralRewards(ctx, tx, domain.ReferralRewardOwed, 500)
		require.NoError(t, err)
		for _, r := range owed {
			if r.OrderID == sent.ID {
				require.NoError(t, discounts.MarkReferralRewardSent(ctx, tx, r.ID, testutil.TestActor()))
				assert.ErrorIs(t, discounts.MarkReferralRewardSent(ctx, tx, r.ID, testutil.TestActor()),
					app.ErrReferralRewardNotOwed, "a bag goes out once")
			}
		}

		_, err = orders.CancelOrder(ctx, tx, unsent.ID, testutil.TestActor())
		require.NoError(t, err)
		_, err = orders.CancelOrder(ctx, tx, sent.ID, testutil.TestActor())
		require.NoError(t, err)

		assert.ElementsMatch(t, []string{"void", "sent"}, rewardsFor(t, tx, m))
	})
}

// Review found these. Each is the failure it names, run.
func TestReferral_ReviewFixes(t *testing.T) {
	ctx := context.Background()
	svc := newDiscountService()
	actor := testutil.TestActor()

	t.Run("switching to an older discount works", func(t *testing.T) {
		// The one-offer index is checked row by row. A single UPDATE that
		// unmarked one row and marked another failed with a unique violation
		// whenever it reached the new row first: older, so stored first.
		tx := testutil.NewTestTx(t, testPool)
		older, _ := newWelcomeCode(t, tx)
		newer, _ := newWelcomeCode(t, tx)
		require.NoError(t, svc.SetReferralOffer(ctx, tx, newer.ID, actor))
		require.NoError(t, svc.SetReferralOffer(ctx, tx, older.ID, actor))

		got, err := svc.ReferralOffer(ctx, tx)
		require.NoError(t, err)
		assert.Equal(t, older.ID, got.ID)

		// And the one it replaced has its own entry saying so.
		var n int
		require.NoError(t, tx.QueryRow(ctx,
			`SELECT count(*) FROM audit_log WHERE resource_type = 'discount' AND resource_id = $1
			   AND action = $2 AND metadata->>'is_referral' = 'false'`,
			newer.ID, audit.AuditDiscountReferralOffer).Scan(&n))
		assert.Equal(t, 1, n)
	})

	t.Run("no code is shown for an offer that has expired or not started", func(t *testing.T) {
		for name, opt := range map[string]testutil.DiscountOption{
			"expired":     testutil.WithDiscountExpiry(time.Now().Add(-time.Hour)),
			"not started": testutil.WithDiscountStart(time.Now().Add(24 * time.Hour)),
		} {
			// A subtest each: a test transaction rolls back when its test
			// ends, and two open at once would wait on each other's
			// uncommitted referral offer under the one-offer index.
			t.Run(name, func(t *testing.T) {
				tx := testutil.NewTestTx(t, testPool)
				d := testutil.CreateDiscount(t, tx, opt,
					testutil.WithUsageLimit(domain.CouponUsageOncePerCustomer),
					testutil.WithAppliesTo(domain.DiscountAppliesToSubscriptionSignup))
				require.NoError(t, svc.SetReferralOffer(ctx, tx, d.ID, actor))
				m := newShopper(t, tx)
				m.subscribe(t, tx, domain.SubscriptionStatusActive)

				code, _, err := svc.MemberReferralCode(ctx, tx, m.customer)
				require.NoError(t, err)
				assert.Nil(t, code, "a code a friend would be refused is not shown")
			})
		}
	})

	t.Run("a signed-in member cannot use their own code under another email", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		newReferralOffer(t, tx)
		m, code := newMember(t, tx)
		other := newShopper(t, tx) // the second email's customer

		_, _, err := newCheckoutService().ApplyCoupon(ctx, tx, code.Code, 5000, app.CouponUse{
			SubscriptionSignup: true, CustomerID: &other.customer.ID, SignedInCustomerID: &m.customer.ID,
		})
		assert.ErrorIs(t, err, app.ErrCouponOwnCode)
	})

	t.Run("only a first-subscription-order discount can be the offer", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		cart := testutil.CreateDiscount(t, tx, testutil.WithUsageLimit(domain.CouponUsageOncePerCustomer))
		assert.ErrorIs(t, svc.SetReferralOffer(ctx, tx, cart.ID, actor), app.ErrReferralOfferInvalid)
	})

	t.Run("clearing a discount that is not the offer leaves the offer alone", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		offer := newReferralOffer(t, tx)
		other := testutil.CreateDiscount(t, tx)
		require.NoError(t, svc.ClearReferralOffer(ctx, tx, other.ID, actor))

		got, err := svc.ReferralOffer(ctx, tx)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, offer.ID, got.ID)
	})

	t.Run("a cancelled friend no longer counts", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		newReferralOffer(t, tx)
		m, code := newMember(t, tx)
		order, err := newShopper(t, tx).placeOrderKind(t, tx, newCheckoutService(), code.Code, "pi_ref_count_cancel", true)
		require.NoError(t, err)
		capture(t, tx, newCheckoutService(), "pi_ref_count_cancel")
		_, err = newOrderService().WithDiscounts(store.NewDiscountStore()).CancelOrder(ctx, tx, order.ID, actor)
		require.NoError(t, err)

		n, err := svc.ReferralFriendsCount(ctx, tx, m.customer.ID)
		require.NoError(t, err)
		assert.Zero(t, n)
	})
}
