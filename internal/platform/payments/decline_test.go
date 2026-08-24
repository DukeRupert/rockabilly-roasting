package payments

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v82"
)

// TestDeclineError_Permanent pins the hard/soft split. Getting this wrong is
// expensive in both directions: calling a soft decline permanent kills a
// subscription one retry would have saved, and calling a hard decline soft
// racks up network fines against a card that will never work.
func TestDeclineError_Permanent(t *testing.T) {
	hard := []string{
		"lost_card",
		"stolen_card",
		"pickup_card",
		"restricted_card",
		"revocation_of_authorization",
		"revocation_of_all_authorizations",
		"do_not_try_again",
		"invalid_account",
		"no_account",
		"card_not_supported",
		"transaction_not_allowed",
		"stop_payment_order",
	}
	for _, code := range hard {
		t.Run("hard/"+code, func(t *testing.T) {
			e := &DeclineError{Code: "card_declined", DeclineCode: code}
			assert.True(t, e.Permanent(), "%s must stop the retry ladder", code)
		})
	}

	soft := []string{
		"insufficient_funds",
		"do_not_honor",
		"generic_decline",
		"processing_error",
		"expired_card",
		"card_velocity_exceeded",
		"issuer_not_available",
		"try_again_later",
		"incorrect_cvc",
		"", // a decline with no issuer reason at all
	}
	for _, code := range soft {
		t.Run("soft/"+code, func(t *testing.T) {
			e := &DeclineError{Code: "card_declined", DeclineCode: code}
			assert.False(t, e.Permanent(), "%q must stay retryable", code)
		})
	}

	// An unrecognised code — a new one from the issuer, a typo, anything — must
	// read as soft. Defaulting to "permanent" would let an unknown string kill
	// subscriptions silently.
	unknown := &DeclineError{Code: "card_declined", DeclineCode: "some_future_code"}
	assert.False(t, unknown.Permanent(), "unknown decline codes default to retryable")
}

func TestDeclineError_Error(t *testing.T) {
	withCode := &DeclineError{Code: "card_declined", DeclineCode: "lost_card", Message: "Your card was declined."}
	assert.Contains(t, withCode.Error(), "lost_card")
	assert.Contains(t, withCode.Error(), "Your card was declined.")

	noCode := &DeclineError{Code: "card_declined", Message: "Your card was declined."}
	assert.NotContains(t, noCode.Error(), "//", "an absent decline code must not leave an empty slot")
}

// TestAsDeclineError covers the Stripe unwrapping: card errors become a typed
// decline, everything else passes through untouched so it stays retryable.
func TestAsDeclineError(t *testing.T) {
	t.Run("card error becomes a DeclineError", func(t *testing.T) {
		in := &stripe.Error{
			Type:        stripe.ErrorTypeCard,
			Code:        stripe.ErrorCodeCardDeclined,
			DeclineCode: stripe.DeclineCodeStolenCard,
			Msg:         "Your card was declined.",
		}
		var got *DeclineError
		require.True(t, errors.As(asDeclineError(in), &got))
		assert.Equal(t, "card_declined", got.Code)
		assert.Equal(t, "stolen_card", got.DeclineCode)
		assert.Equal(t, "Your card was declined.", got.Message)
		assert.True(t, got.Permanent())
	})

	t.Run("wrapped card error is still found", func(t *testing.T) {
		// The real call site wraps with fmt.Errorf, and recordRenewalFailure
		// reaches the decline through errors.As — so unwrapping must survive
		// however many layers sit in between.
		in := fmt.Errorf("create payment intent: %w", asDeclineError(&stripe.Error{
			Type:        stripe.ErrorTypeCard,
			DeclineCode: stripe.DeclineCodeInsufficientFunds,
		}))
		var got *DeclineError
		require.True(t, errors.As(in, &got))
		assert.False(t, got.Permanent())
	})

	t.Run("non-card Stripe error passes through", func(t *testing.T) {
		// An API or rate-limit failure says nothing about the card. Converting
		// one into a decline would advance the dunning ladder over an outage.
		in := &stripe.Error{Type: stripe.ErrorTypeAPI, Msg: "upstream down"}
		out := asDeclineError(in)
		var got *DeclineError
		assert.False(t, errors.As(out, &got))
		assert.Equal(t, in, out)
	})

	t.Run("plain error passes through", func(t *testing.T) {
		in := errors.New("connection reset")
		assert.Equal(t, in, asDeclineError(in))
	})
}
