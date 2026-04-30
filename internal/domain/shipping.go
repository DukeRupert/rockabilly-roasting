package domain

import (
	"strings"
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

// ShippingConfig holds the merchant's shipping rules.
type ShippingConfig struct {
	FlatRateCents         int
	FreeShippingThreshold *int
	Currency              string
	LocalZipCodes         []string

	// Origin address. Captured for future live-rate work; the Pirate Ship
	// CSV round-trip uses Pirate Ship's own origin config, so these fields
	// are informational for that flow.
	OriginName    string
	OriginStreet1 string
	OriginStreet2 string
	OriginCity    string
	OriginState   string
	OriginZip     string
	OriginCountry string

	// TareWeightOz is the packaging weight (box + dunnage) added to every
	// computed shipment weight on export.
	TareWeightOz float64
}

// IsLocal reports whether a ship-to zip falls inside the local delivery zone.
// Accepts either 5-digit or ZIP+4 formats and ignores surrounding whitespace.
func (c ShippingConfig) IsLocal(shipToZip string) bool {
	z := normalizeZip(shipToZip)
	if z == "" {
		return false
	}
	for _, local := range c.LocalZipCodes {
		if normalizeZip(local) == z {
			return true
		}
	}
	return false
}

// Calculate returns the shipping cost in cents for a given subtotal and
// destination zip. Local zips always ship free; otherwise the flat rate applies
// unless the subtotal meets the free-shipping threshold.
func (c ShippingConfig) Calculate(subtotalCents int, shipToZip string) int {
	if c.IsLocal(shipToZip) {
		return 0
	}
	if c.FreeShippingThreshold != nil && subtotalCents >= *c.FreeShippingThreshold {
		return 0
	}
	return c.FlatRateCents
}

// normalizeZip trims whitespace and strips a ZIP+4 suffix so "99336-1234 "
// compares equal to "99336".
func normalizeZip(zip string) string {
	z := strings.TrimSpace(zip)
	if i := strings.Index(z, "-"); i >= 0 {
		z = z[:i]
	}
	return z
}

// Shipment represents a physical shipment with tracking. Several fields are
// pointer-typed because Pirate Ship CSV imports do not carry a label artifact
// or box dimensions — only EasyPost-purchased shipments populate them.
type Shipment struct {
	ID             uuid.UUID
	OrderID        uuid.UUID
	Status         ShipmentStatus
	Provider       string
	TrackingNumber string
	LabelURL       *string
	CarrierName    string
	ServiceName    string
	LabelCostCents int
	LabelCurrency  string
	WeightOz       float64
	LengthIn       *float64
	WidthIn        *float64
	HeightIn       *float64
	CreatedBy      uuid.UUID
	CreatedAt      time.Time
	LabelCreatedAt *time.Time
	ShippedAt      *time.Time
	DeliveredAt    *time.Time
	LabelR2Key     *string
	LabelFormat    *string
}
