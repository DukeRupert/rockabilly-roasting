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
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// Subscribers-only codes: GIFT20, a members' offer on one-time orders.
//
// Membership has to come from the session. The checkout's customer is whoever
// the information step resolved from the email typed into a form, so trusting
// it would hand the discount to anyone who knows a member's address.

// subscribe gives s's customer a subscription in the given status, written
// straight to the table: eligibility reads only customer and status.
func (s shopper) subscribe(t *testing.T, tx pgx.Tx, status domain.SubscriptionStatus) {
	t.Helper()
	ctx := context.Background()
	var planID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO subscription_plans (name, interval, interval_count, is_active)
		 VALUES ($1, 'month', 1, true) RETURNING id`,
		"Plan "+uuid.NewString()[:8]).Scan(&planID))
	now := time.Now()
	_, err := tx.Exec(ctx,
		`INSERT INTO subscriptions
		     (customer_id, plan_id, status, shipping_address_id, variant_id, quantity,
		      current_period_start, current_period_end, next_order_at)
		 VALUES ($1, $2, $3, $4, $5, 1, $6, $7, $7)`,
		s.customer.ID, planID, string(status), s.addr.ID, s.variant.ID, now, now.AddDate(0, 1, 0))
	require.NoError(t, err)
}

// placeSignedIn is place, with the session's customer passed the way phase 3
// of the checkout endpoint passes it.
func (s shopper) placeSignedIn(t *testing.T, tx pgx.Tx, svc *app.CheckoutService, code string, signedIn *uuid.UUID) error {
	t.Helper()
	_, err := svc.PlaceOrder(context.Background(), tx, app.PlaceOrderParams{
		CustomerID:         s.customer.ID,
		SignedInCustomerID: signedIn,
		ShippingAddressID:  s.addr.ID,
		BillingAddressID:   s.addr.ID,
		CurrencyCode:       "USD",
		Items:              []app.CartItem{{VariantID: s.variant.ID, Quantity: 1, UnitPrice: 5000}},
		CouponCode:         &code,
	}, testutil.TestActor())
	return err
}

func newMembersCode(t *testing.T, tx pgx.Tx) *domain.CouponCode {
	t.Helper()
	discount := testutil.CreateDiscount(t, tx,
		testutil.WithDiscountValue(20),
		testutil.WithEligibility(domain.DiscountEligibilitySubscribers))
	return testutil.CreateCouponCode(t, tx, discount.ID)
}

func TestCheckoutService_SubscribersOnlyCode(t *testing.T) {
	ctx := context.Background()
	svc := newCheckoutService()

	t.Run("a signed-in member gets it on their own order", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		code := newMembersCode(t, tx)
		s := newShopper(t, tx)
		s.subscribe(t, tx, domain.SubscriptionStatusActive)

		_, _, err := svc.ApplyCoupon(ctx, tx, code.Code, 5000, app.CouponUse{SignedInCustomerID: &s.customer.ID})
		require.NoError(t, err, "the code box, before the information step")
		assert.NoError(t, s.placeSignedIn(t, tx, svc, code.Code, &s.customer.ID))
	})

	t.Run("a guest is asked to sign in, even one whose email belongs to a member", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		code := newMembersCode(t, tx)
		s := newShopper(t, tx)
		s.subscribe(t, tx, domain.SubscriptionStatusActive)

		_, _, err := svc.ApplyCoupon(ctx, tx, code.Code, 5000, app.CouponUse{CustomerID: &s.customer.ID})
		assert.ErrorIs(t, err, app.ErrCouponSignInRequired)
		assert.ErrorIs(t, s.placeSignedIn(t, tx, svc, code.Code, nil), app.ErrCouponSignInRequired,
			"a code applied while signed in must not survive signing out")
	})

	t.Run("only an active subscription counts", func(t *testing.T) {
		// The offer is for active members: paused and past-due subscribers
		// are refused along with cancelled, expired and never-subscribed.
		for _, status := range []domain.SubscriptionStatus{
			domain.SubscriptionStatusPaused, domain.SubscriptionStatusPastDue,
			domain.SubscriptionStatusCancelled, domain.SubscriptionStatusExpired, "",
		} {
			tx := testutil.NewTestTx(t, testPool)
			code := newMembersCode(t, tx)
			s := newShopper(t, tx)
			if status != "" {
				s.subscribe(t, tx, status)
			}
			assert.ErrorIs(t, s.placeSignedIn(t, tx, svc, code.Code, &s.customer.ID),
				app.ErrCouponSubscribersOnly, "status %q", status)
		}
	})

	t.Run("a member's sign-in does not discount someone else's order", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		code := newMembersCode(t, tx)
		member := newShopper(t, tx)
		member.subscribe(t, tx, domain.SubscriptionStatusActive)
		other := newShopper(t, tx)

		// Signed in as the member, but the information step resolved a
		// different customer from the email typed in.
		assert.ErrorIs(t, other.placeSignedIn(t, tx, svc, code.Code, &member.customer.ID),
			app.ErrCouponSubscribersOnly)
	})

	t.Run("codes for anyone ignore the session", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		discount := testutil.CreateDiscount(t, tx)
		code := testutil.CreateCouponCode(t, tx, discount.ID)
		assert.NoError(t, newShopper(t, tx).placeSignedIn(t, tx, svc, code.Code, nil))
	})
}

// Eligibility is editable, and the two other writes to a discount — activate
// or deactivate, and an edit that does not mention it — must leave it alone.
func TestDiscountService_EligibilitySurvivesOtherWrites(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	svc := app.NewDiscountService(store.NewDiscountStore(), audit.NewAuditWriter(), metrics.NewRegistry())
	actor := testutil.TestActor()

	d, err := svc.CreateWithCode(ctx, tx, app.CreateDiscountParams{
		Name: "Gift 20", Type: domain.DiscountTypePercentage, Value: 20,
		Code: "GIFT-" + uuid.NewString()[:8], Eligibility: domain.DiscountEligibilitySubscribers,
	}, actor)
	require.NoError(t, err)
	assert.Equal(t, domain.DiscountEligibilitySubscribers, d.Eligibility)

	require.NoError(t, svc.SetActive(ctx, tx, d.ID, false, actor))
	got, err := svc.GetDiscount(ctx, tx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.DiscountEligibilitySubscribers, got.Eligibility, "deactivating keeps it")

	got, err = svc.Update(ctx, tx, d.ID, app.EditDiscountParams{
		Name: "Gift 20", Type: domain.DiscountTypePercentage, Value: 25,
	}, actor)
	require.NoError(t, err)
	assert.Equal(t, domain.DiscountEligibilitySubscribers, got.Eligibility, "an edit that omits it keeps it")

	got, err = svc.Update(ctx, tx, d.ID, app.EditDiscountParams{
		Name: "Gift 20", Type: domain.DiscountTypePercentage, Value: 25,
		Eligibility: domain.DiscountEligibilityAnyone,
	}, actor)
	require.NoError(t, err)
	assert.Equal(t, domain.DiscountEligibilityAnyone, got.Eligibility, "and an edit can change it")
}
