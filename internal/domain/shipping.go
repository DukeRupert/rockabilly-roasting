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
