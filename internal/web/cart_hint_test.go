package web

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

func TestFreeShippingHint(t *testing.T) {
	threshold := 5000
	cfg := &domain.ShippingConfig{FlatRateCents: 600, FreeShippingThreshold: &threshold}

	t.Run("under threshold names the gap", func(t *testing.T) {
		assert.Equal(t, "You're $32.00 away from free shipping.", freeShippingHint(cfg, 1800, false))
	})
	t.Run("coupon applied says the gap is after the coupon", func(t *testing.T) {
		// $54 cart, 20% off -> $43.20 qualifies; the page shows $54 so the
		// copy has to explain why there is still a gap.
		assert.Equal(t, "You're $6.80 away from free shipping after your coupon.", freeShippingHint(cfg, 4320, true))
	})
	t.Run("at threshold is unlocked", func(t *testing.T) {
		assert.Equal(t, "You've unlocked free shipping.", freeShippingHint(cfg, 5000, true))
	})
	t.Run("no threshold or config says nothing", func(t *testing.T) {
		assert.Empty(t, freeShippingHint(&domain.ShippingConfig{FlatRateCents: 600}, 100, false))
		assert.Empty(t, freeShippingHint(nil, 100, false))
	})
}
