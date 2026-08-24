package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/platform/payments"
)

// stubPaymentMethods is a payments.Provider that answers only the two calls
// pickRenewalPaymentMethod makes. Everything else panics: if a future change
// starts calling something new from this path, the test should say so loudly
// rather than quietly returning zero values.
type stubPaymentMethods struct {
	payments.Provider
	defaultPM string
	attached  []string
}

func (s *stubPaymentMethods) GetCustomer(context.Context, string) (*payments.Customer, error) {
	return &payments.Customer{DefaultPaymentMethodID: s.defaultPM}, nil
}

func (s *stubPaymentMethods) ListPaymentMethods(context.Context, string) ([]payments.PaymentMethod, error) {
	out := make([]payments.PaymentMethod, len(s.attached))
	for i, id := range s.attached {
		out[i] = payments.PaymentMethod{ID: id, Type: "card"}
	}
	return out, nil
}

// TestPickRenewalPaymentMethodAvoidsDeadCard covers the card that releases the
// hard-decline latch.
//
// The latch is released by the resolved payment method differing from the one
// that died, so whether a customer's replacement card is ever charged comes down
// entirely to this function. The subtle case is a customer who adds a card
// without it becoming the default: preferring a known-dead default over a
// working second card would strand exactly the person who did what the dunning
// email asked.
func TestPickRenewalPaymentMethodAvoidsDeadCard(t *testing.T) {
	ctx := context.Background()
	pick := func(defaultPM string, attached []string, avoid string) string {
		s := &RenewalService{payments: &stubPaymentMethods{defaultPM: defaultPM, attached: attached}}
		got, err := s.pickRenewalPaymentMethod(ctx, "cus_x", avoid)
		require.NoError(t, err)
		return got
	}

	t.Run("no dead card: default wins", func(t *testing.T) {
		assert.Equal(t, "pm_default", pick("pm_default", []string{"pm_other"}, ""))
	})

	t.Run("no default: first attached", func(t *testing.T) {
		assert.Equal(t, "pm_first", pick("", []string{"pm_first", "pm_second"}, ""))
	})

	t.Run("dead card is the default, a working card exists", func(t *testing.T) {
		// The case that matters: Stripe's full billing portal need not promote a
		// newly added card to default.
		assert.Equal(t, "pm_new", pick("pm_dead", []string{"pm_dead", "pm_new"}, "pm_dead"))
	})

	t.Run("dead card is not the default", func(t *testing.T) {
		assert.Equal(t, "pm_default", pick("pm_default", []string{"pm_dead", "pm_default"}, "pm_dead"))
	})

	t.Run("dead card is the only card: return it so the gate blocks", func(t *testing.T) {
		// Must not return "" — that would take the "no payment method on file"
		// branch and tell the customer something untrue.
		assert.Equal(t, "pm_dead", pick("pm_dead", []string{"pm_dead"}, "pm_dead"))
		assert.Equal(t, "pm_dead", pick("", []string{"pm_dead"}, "pm_dead"))
	})

	t.Run("nothing on file at all", func(t *testing.T) {
		assert.Empty(t, pick("", nil, "pm_dead"))
		assert.Empty(t, pick("", nil, ""))
	})
}
