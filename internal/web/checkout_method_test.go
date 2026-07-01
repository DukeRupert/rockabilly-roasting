package web

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

func TestResolveLocalMethod(t *testing.T) {
	pickup := domain.ShippingMethodPickup
	delivery := domain.ShippingMethodLocalDelivery
	shipped := domain.ShippingMethodShipped
	bothEligible := []domain.ShippingMethod{delivery, pickup}
	pickupOnly := []domain.ShippingMethod{pickup}

	t.Run("non-local returns nil regardless of input", func(t *testing.T) {
		assert.Nil(t, resolveLocalMethod(nil, "pickup", &pickup))
		assert.Nil(t, resolveLocalMethod([]domain.ShippingMethod{}, "", nil))
	})

	t.Run("explicit valid request wins over preference", func(t *testing.T) {
		got := resolveLocalMethod(bothEligible, "pickup", &delivery)
		if assert.NotNil(t, got) {
			assert.Equal(t, domain.ShippingMethodPickup, *got)
		}
	})

	t.Run("explicit shipped request is honored for a local zip", func(t *testing.T) {
		// A local customer opting to have it mailed instead of delivered.
		got := resolveLocalMethod(bothEligible, "shipped", &delivery)
		if assert.NotNil(t, got) {
			assert.Equal(t, domain.ShippingMethodShipped, *got)
		}
	})

	t.Run("shipped preference is honored when no request", func(t *testing.T) {
		got := resolveLocalMethod(bothEligible, "", &shipped)
		if assert.NotNil(t, got) {
			assert.Equal(t, domain.ShippingMethodShipped, *got)
		}
	})

	t.Run("unknown request falls through to preference", func(t *testing.T) {
		got := resolveLocalMethod(bothEligible, "carrier_pigeon", &delivery)
		if assert.NotNil(t, got) {
			assert.Equal(t, domain.ShippingMethodLocalDelivery, *got)
		}
	})

	t.Run("preference applied when no request", func(t *testing.T) {
		got := resolveLocalMethod(bothEligible, "", &pickup)
		if assert.NotNil(t, got) {
			assert.Equal(t, domain.ShippingMethodPickup, *got)
		}
	})

	t.Run("preference ignored when not eligible", func(t *testing.T) {
		got := resolveLocalMethod(pickupOnly, "", &delivery)
		if assert.NotNil(t, got) {
			assert.Equal(t, domain.ShippingMethodPickup, *got)
		}
	})

	t.Run("falls back to first eligible when nothing else applies", func(t *testing.T) {
		got := resolveLocalMethod(bothEligible, "", nil)
		if assert.NotNil(t, got) {
			assert.Equal(t, domain.ShippingMethodLocalDelivery, *got,
				"first eligible (delivery first) is the default")
		}
	})
}

func TestShippingDisplayLabel(t *testing.T) {
	pickup := domain.ShippingMethodPickup
	delivery := domain.ShippingMethodLocalDelivery
	cfg := &domain.ShippingConfig{LocalZipCodes: []string{"99336"}}

	t.Run("paid shipping uses default label", func(t *testing.T) {
		assert.Equal(t, "", shippingDisplayLabel(cfg, 600, "90210", nil))
	})
	t.Run("free non-local says free shipping", func(t *testing.T) {
		assert.Equal(t, "Free shipping", shippingDisplayLabel(cfg, 0, "90210", nil))
	})
	t.Run("pickup method takes priority", func(t *testing.T) {
		assert.Equal(t, "Free pickup at the shop", shippingDisplayLabel(cfg, 0, "99336", &pickup))
	})
	t.Run("local delivery method uses delivery label", func(t *testing.T) {
		assert.Equal(t, "Free local delivery", shippingDisplayLabel(cfg, 0, "99336", &delivery))
	})
	t.Run("local zip with no method falls back to delivery label", func(t *testing.T) {
		assert.Equal(t, "Free local delivery", shippingDisplayLabel(cfg, 0, "99336", nil))
	})
	t.Run("paid shipped from a local zip uses default label", func(t *testing.T) {
		shipped := domain.ShippingMethodShipped
		assert.Equal(t, "", shippingDisplayLabel(cfg, 600, "99336", &shipped))
	})
	t.Run("free shipped (over threshold) from a local zip says free shipping", func(t *testing.T) {
		shipped := domain.ShippingMethodShipped
		assert.Equal(t, "Free shipping", shippingDisplayLabel(cfg, 0, "99336", &shipped))
	})
}
