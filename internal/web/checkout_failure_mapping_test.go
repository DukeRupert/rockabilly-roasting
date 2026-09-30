package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// Both checkout endpoints used to answer every terminal error except a spent
// coupon with a 500. respond.go had a status for each of them the whole time —
// the mappings were simply unreachable, because the handlers branched per
// sentinel and knew about one. So a shopper told their price had moved, and an
// attacker told their address was refused, both got "failed to place order" and
// both were recorded as faults.
//
// This pins what the one rule that replaced the branching is allowed to say. The
// status is the point, and so is the message: routing through Error() is what
// makes these sentences customer-visible at all, so each has to read as advice
// rather than as a diagnostic.
func TestFailCheckoutAnswersWithTheMappedStatus(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantReason string
		wantMsg    string
	}{
		{
			name:       "an address that is not the buyer's",
			err:        app.ErrAddressNotFound,
			wantStatus: http.StatusNotFound,
			wantReason: "validation_error",
			wantMsg:    "not found",
		},
		{
			// Wrapped on purpose: refusePricesThatMoved names the line and both
			// prices, and no handler can decline that, so this is the one arm
			// that has to answer with the sentinel's own sentence.
			name:       "the price moved while they were paying",
			err:        app.ErrPriceMoved,
			wantStatus: http.StatusConflict,
			wantReason: "price_moved",
			wantMsg:    app.ErrPriceMoved.Error(),
		},
		{
			name:       "an empty cart",
			err:        app.ErrCartEmpty,
			wantStatus: http.StatusUnprocessableEntity,
			wantReason: "validation_error",
			wantMsg:    "cart is empty",
		},
		{
			name:       "a line with no quantity",
			err:        app.ErrInvalidQuantity,
			wantStatus: http.StatusUnprocessableEntity,
			wantReason: "validation_error",
			wantMsg:    "quantity must be greater than zero",
		},
		{
			name:       "a negative price",
			err:        app.ErrInvalidPrice,
			wantStatus: http.StatusUnprocessableEntity,
			wantReason: "validation_error",
			wantMsg:    "price must not be negative",
		},
		{
			name:       "a discount that expired mid-checkout",
			err:        app.ErrDiscountExpired,
			wantStatus: http.StatusUnprocessableEntity,
			wantReason: "validation_error",
			wantMsg:    "discount has expired",
		},
		{
			name:       "something that really did go wrong",
			err:        errors.New("insert order: connection refused"),
			wantStatus: http.StatusInternalServerError,
			wantReason: "internal_error",
			wantMsg:    "internal server error",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := metrics.NewRegistry()
			d := &Deps{Metrics: reg}

			rec := httptest.NewRecorder()
			d.failCheckout(rec, httptest.NewRequest(http.MethodPost, "/api/checkout/payment-intent", nil),
				"retail", tc.err)

			assert.Equal(t, tc.wantStatus, rec.Code)
			assert.JSONEq(t, `{"error":`+quoted(tc.wantMsg)+`}`, rec.Body.String())
			assert.Equal(t, 1.0, testutil.ToFloat64(reg.CheckoutFailed.WithLabelValues("retail", tc.wantReason)))
		})
	}
}

// The sentinels below were invisible before: PlaceOrder could return any of
// them and the handler answered 500 "failed to place order". Routing through
// Error() is what makes them reach a shopper, so what they say is now copy, and
// this pins that what mapError answers for each of them — the sentinel's own
// sentence on some arms, a literal of its own on others — is never worded as a
// diagnostic: no phase prefix, no pricing arithmetic. The "catalog says" case is
// that second point about ErrPriceMoved specifically — the sentinel's own
// sentence carries the advice, and the line and both prices live only in
// refusePricesThatMoved's wrapper inside app/, where the log can have them.
//
// It passes bare sentinels, so it says nothing about either handler. A caller
// that wrapped its PlaceOrder error — "place order: discount has expired" — is
// caught by TestPaymentIntentRefusalReadsAsAdviceNotAPrefixedDiagnostic below,
// which drives the retail endpoint end to end. This is the floor under that: the
// strings themselves, including any sentinel added to the list later.
func TestCheckoutSentinelsReadAsAdviceNotDiagnostics(t *testing.T) {
	for _, err := range []error{
		app.ErrCartEmpty, app.ErrDiscountExpired, app.ErrDiscountNotActive,
		app.ErrMinimumOrderNotMet, app.ErrInvalidQuantity, app.ErrInvalidPrice,
		app.ErrAddressNotFound, app.ErrPriceMoved,
	} {
		_, msg := mapError(err)
		assert.NotContains(t, msg, "place order", "%v", err)
		assert.NotContains(t, msg, "signup order", "%v", err)
		assert.NotContains(t, msg, "catalog says",
			"the arithmetic behind a moved price is for the log: %v", err)
	}
}

// A price moving mid-checkout is the case the mapping exists for, and the only
// one reachable through a real handler without breaking the wiring. Phase 1
// prices the lines and quotes the payment provider from the answer; phase 3
// prices them again inside PlaceOrder and refuses if the two disagree. Creating
// the intent is the one moment in between, so moving the catalog price there
// reproduces the race rather than simulating it.
//
// Both endpoints are driven, because both had the defect and only one of them
// still carries a per-sentinel branch above the shared call — which is where a
// second one would be added.
func TestPaymentIntentPriceMovedIsAConflictNotAFault(t *testing.T) {
	t.Run("retail", func(t *testing.T) {
		f := newCheckoutCouponFixture(t)
		d, fake := newCheckoutPaymentDeps(t)
		fake.onCreate = func() { moveBasePrice(t, f.variantID, 999999) }

		w := f.postIntent(t, d, "")

		require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
		assertPriceMovedBody(t, w.Body.String())
		assert.NotEmpty(t, fake.canceledIDs(), "a refused order must not leave its PaymentIntent live")
	})

	t.Run("subscribe", func(t *testing.T) {
		f := newSubscribeFixture(t)
		d, fake := newSubscribePaymentDeps(t)
		fake.onCreate = func() { moveBasePrice(t, f.variantID, 999999) }

		w := postSubscribePaymentIntent(t,
			d, subscribePaymentIntentBody(f.planID, f.variantID, 1))

		require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
		assertPriceMovedBody(t, w.Body.String())
		assert.NotEmpty(t, fake.canceledIDs(), "a refused order must not leave its PaymentIntent live")
	})
}

// The wrap guard. Each endpoint has its own PlaceOrder call and its own chance
// to put a prefix on the sentence a shopper reads, and restoring
// fmt.Errorf("place order: %w", txErr) at one of them is invisible to a test
// that only drives the other. hiri-core drives both; this shop can drive only
// retail, for the reason at the end.
//
// ErrPriceMoved cannot serve as the sentinel here: its arm answers with the
// sentinel's own sentence precisely because app/ wraps it, so a prefix added by
// a handler never shows there. The sentinel has to be one whose arm answers with
// err.Error(), which is what makes a handler's prefix visible — "place order:
// discount has expired".
//
// The leg therefore reads the expected sentence off the sentinel rather than
// repeating it. A prefix still fails the comparison, which is the whole point,
// but rewriting the copy does not: this holds that nothing is prepended, not
// what the sentence says. The literal wording is pinned once, in the table at
// the top of this file.
//
// Retail raises it from inside CreatePaymentIntent, the one moment between the
// handler's two pricings, though it does not need that moment. Phase 1 reads the
// coupon but never its expiry — the condition at checkout.go's applied-discount
// block is RedeemedAt, Active and the order minimum — so an already-expired
// discount is priced in just the same and phase 3 refuses it either way.
// Expiring it from the hook is for realism, not reach: it puts the expiry in the
// window a real one would fall in.
//
// hiri-core's subscribe leg retires a recipe ingredient mid-intent to reach
// ErrRecipeIncomplete. This shop has no recipes, and nothing else that only
// PlaceOrder raises on the subscribe path answers with err.Error(): the address
// guard, the nearest candidate, answers a bare "not found", which would hide a
// prefix rather than show it. So subscribe.go's PlaceOrder call is not pinned
// end to end here. It does not wrap; keep it that way.
func TestPaymentIntentRefusalReadsAsAdviceNotAPrefixedDiagnostic(t *testing.T) {
	t.Run("retail", func(t *testing.T) {
		f := newCheckoutCouponFixture(t)
		d, fake := newCheckoutPaymentDeps(t)
		fake.onCreate = func() { expireDiscountFor(t, f.cartID) }

		w := f.postIntent(t, d, "")

		require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
		assert.JSONEq(t, `{"error":`+quoted(app.ErrDiscountExpired.Error())+`}`, w.Body.String(),
			"no handler prefix on the sentence the shopper reads")
	})
}

// expireDiscountFor moves the discount applied to a cart into the past, in its
// own committed transaction so it can run from inside the payment provider.
func expireDiscountFor(t *testing.T, cartID uuid.UUID) {
	t.Helper()
	err := store.Tx(context.Background(), testPool, func(tx pgx.Tx) error {
		_, execErr := tx.Exec(context.Background(),
			`UPDATE discounts SET expires_at = now() - interval '1 day'
			   WHERE id = (SELECT applied_discount_id FROM carts WHERE id = $1)`,
			cartID)
		return execErr
	})
	require.NoError(t, err, "expire the discount out from under the checkout")
}

func assertPriceMovedBody(t *testing.T, body string) {
	t.Helper()
	assert.Contains(t, body, "price changed",
		"the shopper needs the advice, which is to look at the new total")
	assert.NotContains(t, body, "catalog says",
		"and not the diagnostic detail, which belongs in the log")
	assert.NotContains(t, body, "order:",
		"nor either handler's own wrapping prefix")
}

// moveBasePrice rewrites a variant's existing base price in its own committed
// transaction, which is what lets it run from inside the payment provider —
// outside the handler's transaction, between the two pricings. price_sets is
// UNIQUE per variant and every fixture mints its own, so exactly one row moves
// and no other test can see it.
func moveBasePrice(t *testing.T, variantID uuid.UUID, amountCents int) {
	t.Helper()
	err := store.Tx(context.Background(), testPool, func(tx pgx.Tx) error {
		_, execErr := tx.Exec(context.Background(),
			`UPDATE prices SET amount = $1
			   WHERE price_set_id = (SELECT id FROM price_sets WHERE variant_id = $2)`,
			amountCents, variantID)
		return execErr
	})
	require.NoError(t, err, "move the catalog price out from under the checkout")
}

// quoted renders s as a JSON string so the table above can compare whole bodies.
func quoted(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err) // a string; unmarshalable means this file is wrong
	}
	return string(b)
}
