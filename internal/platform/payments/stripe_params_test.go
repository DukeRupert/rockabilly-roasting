package payments

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The key is only worth anything if it reaches Stripe. These read the params
// StripeProvider builds rather than calling Stripe.

func TestStripeProvider_SetsTheIdempotencyKey(t *testing.T) {
	t.Run("payment intent", func(t *testing.T) {
		params := paymentIntentParams(CreatePaymentIntentRequest{
			AmountCents: 1500, Currency: "usd", IdempotencyKey: "renewal:abc:1:0",
		})
		require.NotNil(t, params.IdempotencyKey)
		assert.Equal(t, "renewal:abc:1:0", *params.IdempotencyKey)
	})
	t.Run("refund", func(t *testing.T) {
		params := refundParams(RefundRequest{PaymentIntentID: "pi_1", IdempotencyKey: "renewal-refund:pi_1"})
		require.NotNil(t, params.IdempotencyKey)
		assert.Equal(t, "renewal-refund:pi_1", *params.IdempotencyKey)
	})
}

func TestStripeProvider_SendsNoKeyWhenNoneIsGiven(t *testing.T) {
	// Every caller that predates the field keeps today's behaviour.
	assert.Nil(t, paymentIntentParams(CreatePaymentIntentRequest{AmountCents: 1500, Currency: "usd"}).IdempotencyKey)
	assert.Nil(t, refundParams(RefundRequest{PaymentIntentID: "pi_1"}).IdempotencyKey)
}
