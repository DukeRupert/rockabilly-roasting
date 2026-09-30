package web

import (
	"context"
	"errors"
	"fmt"
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
// coupon with a 500. respond.go mapped ErrAddressNotFound to 404, ErrPriceMoved
// to 409 and ErrPricingUnavailable to 503 the whole time — the mappings were
// simply unreachable, because the handlers branched per sentinel and knew about
// one. So a shopper told their price had moved, and an attacker told their
// address was refused, both got "failed to place order" and both were recorded
// as faults.
//
// This pins the rule that replaced the branching. The errors arrive wrapped, as
// they do in production — the handler wraps with "place order" and
// refusePricesThatMoved has already named the line and both prices — because a
// mapping that only works on a bare sentinel would be no fix at all.
func TestFailCheckoutAnswersWithTheMappedStatus(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantReason string
	}{
		{
			name:       "an address that is not the buyer's",
			err:        app.ErrAddressNotFound,
			wantStatus: http.StatusNotFound,
			wantReason: "validation_error",
		},
		{
			name:       "the price moved while they were paying",
			err:        fmt.Errorf("line 0 priced at 1620, catalog says 1800: %w", app.ErrPriceMoved),
			wantStatus: http.StatusConflict,
			wantReason: "validation_error",
		},
		{
			name:       "a binary with no pricing service wired",
			err:        app.ErrPricingUnavailable,
			wantStatus: http.StatusServiceUnavailable,
			wantReason: "internal_error",
		},
		{
			name:       "something that really did go wrong",
			err:        errors.New("insert order: connection refused"),
			wantStatus: http.StatusInternalServerError,
			wantReason: "internal_error",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := metrics.NewRegistry()
			d := &Deps{Metrics: reg}

			rec := httptest.NewRecorder()
			// Wrapped the way the handler wraps it.
			d.failCheckout(rec, httptest.NewRequest(http.MethodPost, "/api/checkout/payment-intent", nil),
				"retail", fmt.Errorf("place order: %w", tc.err))

			assert.Equal(t, tc.wantStatus, rec.Code)
			assert.NotContains(t, rec.Body.String(), "place order",
				"the handler's own wrapping is for the log, not the shopper")

			// The metric follows the status, so a refusal stops counting as the
			// service breaking.
			assert.Equal(t, 1.0, testutil.ToFloat64(reg.CheckoutFailed.WithLabelValues("retail", tc.wantReason)))
			other := "internal_error"
			if tc.wantReason == "internal_error" {
				other = "validation_error"
			}
			assert.Equal(t, 0.0, testutil.ToFloat64(reg.CheckoutFailed.WithLabelValues("retail", other)),
				"a failure is one kind or the other, not both")
		})
	}
}

// A price moving mid-checkout is the case the mapping exists for, and the only
// one reachable through a real handler without breaking the wiring. Phase 1
// prices the line and quotes the payment provider from the answer; phase 3
// prices it again inside PlaceOrder and refuses if the two disagree. Creating
// the intent is the one moment in between, so moving the catalog price there
// reproduces the race rather than simulating it: somebody re-priced the variant
// while the shopper was on the card form.
//
// 409, not 500. Nothing the shopper sent was wrong when they sent it.
func TestSubscribePaymentIntent_PriceMovedIsAConflictNotAFault(t *testing.T) {
	f := newSubscribeFixture(t)
	d, fake := newSubscribePaymentDeps(t)

	fake.onCreate = func() {
		moveBasePrice(t, f.variantID, 999999)
	}

	w := postSubscribePaymentIntent(t,
		d, subscribePaymentIntentBody(f.planID, f.variantID, 1))

	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "price changed",
		"the shopper needs the advice, which is to look at the new total")
	assert.NotContains(t, w.Body.String(), "catalog says",
		"and not the diagnostic detail, which belongs in the log")
	assert.NotContains(t, w.Body.String(), "place order")

	// The orphaned intent is still cleaned up on the refusal path.
	assert.NotEmpty(t, fake.canceledIDs(), "a refused order must not leave its PaymentIntent live")
}

// moveBasePrice rewrites a variant's existing base price in its own committed
// transaction, which is what lets it run from inside the payment provider —
// outside the handler's transaction, between the two pricings.
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
