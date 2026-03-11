package domain

import (
	"time"

	"github.com/google/uuid"
)

// ShipmentStatus represents the lifecycle state of a shipment.
type ShipmentStatus string

const (
	ShipmentStatusPending      ShipmentStatus = "pending"
	ShipmentStatusLabelCreated ShipmentStatus = "label_created"
	ShipmentStatusInTransit    ShipmentStatus = "in_transit"
	ShipmentStatusDelivered    ShipmentStatus = "delivered"
	ShipmentStatusException    ShipmentStatus = "exception"
)

// ShippingConfig holds flat-rate shipping configuration.
type ShippingConfig struct {
	FlatRateCents         int
	FreeShippingThreshold *int
	Currency              string
}

// Calculate returns the shipping cost in cents for a given subtotal.
func (c ShippingConfig) Calculate(subtotalCents int) int {
	if c.FreeShippingThreshold != nil && subtotalCents >= *c.FreeShippingThreshold {
		return 0
	}
	return c.FlatRateCents
}

// LocalDeliveryZone holds zip codes eligible for local delivery.
type LocalDeliveryZone struct {
	ZipCodes         map[string]bool
	DeliveryFeeCents int // 0 = free, 500 = $5 donation, etc.
}

// Contains returns true if the given zip code is in the local delivery zone.
func (z LocalDeliveryZone) Contains(postalCode string) bool {
	return z.ZipCodes[postalCode]
}

// ShippingRules encapsulates all shipping logic for the platform.
type ShippingRules struct {
	// FlatRateCents is the standard shipping rate for non-local, under-threshold orders.
	FlatRateCents int
	// FreeShippingWeightOz is the minimum weight for free shipping (e.g., 80oz = 5lb).
	FreeShippingWeightOz float64
	// LocalDelivery defines the local delivery zone and fee.
	LocalDelivery LocalDeliveryZone
}

// DetermineMethod returns the shipping method for an order based on the delivery address.
// If the address is in the local delivery zone, it returns local_delivery.
// Otherwise it returns shipped. Pickup is chosen explicitly by the customer.
func (r ShippingRules) DetermineMethod(postalCode string) ShippingMethod {
	if r.LocalDelivery.Contains(postalCode) {
		return ShippingMethodLocalDelivery
	}
	return ShippingMethodShipped
}

// CalculateCost returns the shipping cost in cents for a given method and total weight.
func (r ShippingRules) CalculateCost(method ShippingMethod, totalWeightOz float64) int {
	switch method {
	case ShippingMethodPickup:
		return 0
	case ShippingMethodLocalDelivery:
		return r.LocalDelivery.DeliveryFeeCents
	case ShippingMethodShipped:
		if r.FreeShippingWeightOz > 0 && totalWeightOz >= r.FreeShippingWeightOz {
			return 0
		}
		return r.FlatRateCents
	default:
		return r.FlatRateCents
	}
}

// TriCitiesZipCodes returns the set of zip codes in the Kennewick/Richland/Pasco
// Tri-Cities area used for local delivery.
func TriCitiesZipCodes() map[string]bool {
	return map[string]bool{
		// Kennewick
		"99336": true, "99337": true, "99338": true,
		// Richland
		"99352": true, "99354": true,
		// Pasco
		"99301": true, "99302": true,
		// West Richland
		"99353": true,
		// Benton City
		"99320": true,
		// Finley / Burbank
		"99323": true, "99345": true,
		// Prosser
		"99350": true,
		// Grandview
		"98930": true,
		// Sunnyside
		"98944": true,
	}
}

// DefaultShippingRules returns the standard Rockabilly Roasting shipping rules.
func DefaultShippingRules() ShippingRules {
	return ShippingRules{
		FlatRateCents:        600, // $6 flat rate
		FreeShippingWeightOz: 80,  // 5lb = 80oz
		LocalDelivery: LocalDeliveryZone{
			ZipCodes:         TriCitiesZipCodes(),
			DeliveryFeeCents: 0, // free local delivery (configurable to 500 for $5 donation)
		},
	}
}

// Shipment represents a physical shipment with tracking.
type Shipment struct {
	ID             uuid.UUID
	OrderID        uuid.UUID
	Status         ShipmentStatus
	Provider       string
	TrackingNumber string
	LabelURL       string
	CarrierName    string
	ServiceName    string
	LabelCostCents int
	LabelCurrency  string
	WeightOz       float64
	LengthIn       float64
	WidthIn        float64
	HeightIn       float64
	CreatedBy      uuid.UUID
	CreatedAt      time.Time
	LabelCreatedAt *time.Time
	ShippedAt      *time.Time
	DeliveredAt    *time.Time
	LabelR2Key     *string
	LabelFormat    *string
}
