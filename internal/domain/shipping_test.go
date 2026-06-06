package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShippingConfig_IsLocal(t *testing.T) {
	cfg := ShippingConfig{
		LocalZipCodes: []string{"99336", "99337", "99352"},
	}

	assert.True(t, cfg.IsLocal("99336"))
	assert.True(t, cfg.IsLocal("99352-1234"), "ZIP+4 should normalize to 5 digits")
	assert.True(t, cfg.IsLocal(" 99337 "), "whitespace should be trimmed")
	assert.False(t, cfg.IsLocal("90210"))
	assert.False(t, cfg.IsLocal(""))
}

func TestShippingConfig_EligibleLocalMethods(t *testing.T) {
	bothEnabled := ShippingConfig{
		LocalZipCodes:        []string{"99336"},
		LocalDeliveryEnabled: true,
		LocalPickupEnabled:   true,
	}
	deliveryOnly := ShippingConfig{
		LocalZipCodes:        []string{"99336"},
		LocalDeliveryEnabled: true,
	}
	pickupOnly := ShippingConfig{
		LocalZipCodes:      []string{"99336"},
		LocalPickupEnabled: true,
	}
	bothDisabled := ShippingConfig{
		LocalZipCodes: []string{"99336"},
	}

	tests := []struct {
		name string
		cfg  ShippingConfig
		zip  string
		want []ShippingMethod
	}{
		{"both enabled, local zip", bothEnabled, "99336",
			[]ShippingMethod{ShippingMethodLocalDelivery, ShippingMethodPickup}},
		{"both enabled, non-local zip", bothEnabled, "90210", nil},
		{"delivery only", deliveryOnly, "99336",
			[]ShippingMethod{ShippingMethodLocalDelivery}},
		{"pickup only", pickupOnly, "99336",
			[]ShippingMethod{ShippingMethodPickup}},
		{"both disabled, local zip falls through", bothDisabled, "99336",
			[]ShippingMethod{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.EligibleLocalMethods(tt.zip)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestShippingConfig_WholesaleFulfillmentMethods(t *testing.T) {
	deliveryEnabled := ShippingConfig{
		LocalZipCodes:        []string{"99336"},
		LocalDeliveryEnabled: true,
	}
	deliveryDisabled := ShippingConfig{
		LocalZipCodes: []string{"99336"},
	}

	tests := []struct {
		name string
		cfg  ShippingConfig
		zip  string
		want []ShippingMethod
	}{
		{"local zip, delivery on — all three", deliveryEnabled, "99336",
			[]ShippingMethod{ShippingMethodLocalDelivery, ShippingMethodPickup, ShippingMethodShipped}},
		{"local zip+4, delivery on", deliveryEnabled, "99336-1234",
			[]ShippingMethod{ShippingMethodLocalDelivery, ShippingMethodPickup, ShippingMethodShipped}},
		{"non-local zip — pickup + shipping only", deliveryEnabled, "90210",
			[]ShippingMethod{ShippingMethodPickup, ShippingMethodShipped}},
		{"local zip but delivery disabled", deliveryDisabled, "99336",
			[]ShippingMethod{ShippingMethodPickup, ShippingMethodShipped}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.WholesaleFulfillmentMethods(tt.zip))
		})
	}
}

func TestShippingConfig_WholesaleMethodAllowed(t *testing.T) {
	cfg := ShippingConfig{LocalZipCodes: []string{"99336"}, LocalDeliveryEnabled: true}

	// Pickup and shipping are always allowed regardless of zip.
	assert.True(t, cfg.WholesaleMethodAllowed("90210", ShippingMethodPickup))
	assert.True(t, cfg.WholesaleMethodAllowed("90210", ShippingMethodShipped))
	// Local delivery only inside the zone.
	assert.True(t, cfg.WholesaleMethodAllowed("99336", ShippingMethodLocalDelivery))
	assert.False(t, cfg.WholesaleMethodAllowed("90210", ShippingMethodLocalDelivery))
	// Garbage method never allowed.
	assert.False(t, cfg.WholesaleMethodAllowed("99336", ShippingMethod("courier_pigeon")))
}

func TestShippingConfig_Calculate(t *testing.T) {
	threshold := 5000
	cfg := ShippingConfig{
		FlatRateCents:         600,
		FreeShippingThreshold: &threshold,
		LocalZipCodes:         []string{"99336"},
	}

	t.Run("local zip ships free regardless of subtotal", func(t *testing.T) {
		assert.Equal(t, 0, cfg.Calculate(100, "99336"))
	})
	t.Run("non-local under threshold pays flat rate", func(t *testing.T) {
		assert.Equal(t, 600, cfg.Calculate(2000, "90210"))
	})
	t.Run("non-local at threshold ships free", func(t *testing.T) {
		assert.Equal(t, 0, cfg.Calculate(5000, "90210"))
	})
	t.Run("nil threshold always charges flat rate off-zone", func(t *testing.T) {
		c := cfg
		c.FreeShippingThreshold = nil
		assert.Equal(t, 600, c.Calculate(100000, "90210"))
	})
}
