package app

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

func TestPickRenewalLocalMethod(t *testing.T) {
	pickup := domain.ShippingMethodPickup
	delivery := domain.ShippingMethodLocalDelivery

	bothEnabled := &domain.ShippingConfig{
		LocalZipCodes:        []string{"99336"},
		LocalDeliveryEnabled: true,
		LocalPickupEnabled:   true,
	}
	deliveryOnly := &domain.ShippingConfig{
		LocalZipCodes:        []string{"99336"},
		LocalDeliveryEnabled: true,
	}
	pickupOnly := &domain.ShippingConfig{
		LocalZipCodes:      []string{"99336"},
		LocalPickupEnabled: true,
	}

	t.Run("non-local zip returns nil", func(t *testing.T) {
		assert.Nil(t, pickRenewalLocalMethod(bothEnabled, "90210", &pickup))
	})

	t.Run("preference honored when eligible", func(t *testing.T) {
		got := pickRenewalLocalMethod(bothEnabled, "99336", &pickup)
		if assert.NotNil(t, got) {
			assert.Equal(t, domain.ShippingMethodPickup, *got)
		}
	})

	t.Run("preference ignored when not eligible", func(t *testing.T) {
		// Customer prefers pickup but merchant disabled it — fall back to
		// the only eligible option (delivery).
		got := pickRenewalLocalMethod(deliveryOnly, "99336", &pickup)
		if assert.NotNil(t, got) {
			assert.Equal(t, domain.ShippingMethodLocalDelivery, *got)
		}
	})

	t.Run("no preference, single eligible option", func(t *testing.T) {
		got := pickRenewalLocalMethod(pickupOnly, "99336", nil)
		if assert.NotNil(t, got) {
			assert.Equal(t, domain.ShippingMethodPickup, *got)
		}
	})

	t.Run("no preference, both eligible defaults to delivery", func(t *testing.T) {
		got := pickRenewalLocalMethod(bothEnabled, "99336", nil)
		if assert.NotNil(t, got) {
			assert.Equal(t, domain.ShippingMethodLocalDelivery, *got,
				"renewals should default to the legacy delivery channel until customer opts in")
		}
	})

	t.Run("preference matches single eligible (delivery only)", func(t *testing.T) {
		got := pickRenewalLocalMethod(deliveryOnly, "99336", &delivery)
		if assert.NotNil(t, got) {
			assert.Equal(t, domain.ShippingMethodLocalDelivery, *got)
		}
	})
}
