package domain

import (
	"time"

	"github.com/google/uuid"
)

// FulfillmentItemStatus represents the status of an individual fulfillment.
type FulfillmentItemStatus string

const (
	FulfillmentItemStatusPending   FulfillmentItemStatus = "pending"
	FulfillmentItemStatusPacked    FulfillmentItemStatus = "packed"
	FulfillmentItemStatusShipped   FulfillmentItemStatus = "shipped"
	FulfillmentItemStatusDelivered FulfillmentItemStatus = "delivered"
	FulfillmentItemStatusCancelled FulfillmentItemStatus = "cancelled"
)

// StockLocation represents a warehouse or fulfillment center.
type StockLocation struct {
	ID        uuid.UUID
	Name      string
	AddressID *uuid.UUID
	IsActive  bool
}

// InventoryItem tracks inventory for a variant.
type InventoryItem struct {
	ID               uuid.UUID
	VariantID        uuid.UUID
	TrackInventory   bool
	RequiresShipping bool
}

// StockLevel represents the inventory count at a specific location.
type StockLevel struct {
	ID                uuid.UUID
	InventoryItemID   uuid.UUID
	LocationID        uuid.UUID
	QuantityOnHand    int
	QuantityReserved  int
	QuantityAvailable int
}

// Fulfillment represents a shipment of items from an order.
type Fulfillment struct {
	ID             uuid.UUID
	OrderID        uuid.UUID
	LocationID     uuid.UUID
	Status         FulfillmentItemStatus
	TrackingNumber *string
	TrackingURL    *string
	Provider       *string
	ShippedAt      *time.Time
	DeliveredAt    *time.Time
	Metadata       map[string]any
}

// FulfillmentItem represents a line item included in a fulfillment.
type FulfillmentItem struct {
	ID            uuid.UUID
	FulfillmentID uuid.UUID
	LineItemID    uuid.UUID
	Quantity      int
}
