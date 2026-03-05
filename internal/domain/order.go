package domain

import (
	"time"

	"github.com/google/uuid"
)

// OrderStatus represents the lifecycle state of an order.
type OrderStatus string

const (
	OrderStatusPending    OrderStatus = "pending"
	OrderStatusConfirmed  OrderStatus = "confirmed"
	OrderStatusProcessing OrderStatus = "processing"
	OrderStatusOnHold     OrderStatus = "on_hold"
	OrderStatusComplete   OrderStatus = "complete"
	OrderStatusCancelled  OrderStatus = "cancelled"
	OrderStatusRefunded   OrderStatus = "refunded"
)

// PaymentStatus represents the payment state of an order.
type PaymentStatus string

const (
	PaymentStatusAwaiting   PaymentStatus = "awaiting"
	PaymentStatusAuthorized PaymentStatus = "authorized"
	PaymentStatusCaptured   PaymentStatus = "captured"
	PaymentStatusPartial    PaymentStatus = "partial"
	PaymentStatusRefunded   PaymentStatus = "refunded"
	PaymentStatusFailed     PaymentStatus = "failed"
	PaymentStatusVoided     PaymentStatus = "voided"
)

// FulfillmentStatus represents the fulfillment state of an order.
type FulfillmentStatus string

const (
	FulfillmentStatusUnfulfilled        FulfillmentStatus = "unfulfilled"
	FulfillmentStatusPartiallyFulfilled FulfillmentStatus = "partially_fulfilled"
	FulfillmentStatusFulfilled          FulfillmentStatus = "fulfilled"
	FulfillmentStatusPartiallyShipped   FulfillmentStatus = "partially_shipped"
	FulfillmentStatusShipped            FulfillmentStatus = "shipped"
	FulfillmentStatusPartiallyDelivered FulfillmentStatus = "partially_delivered"
	FulfillmentStatusDelivered          FulfillmentStatus = "delivered"
	FulfillmentStatusReturned           FulfillmentStatus = "returned"
)

// Cart represents a shopping cart.
type Cart struct {
	ID                  uuid.UUID
	CustomerID          *uuid.UUID
	CurrencyCode        string
	ShippingAddressID   uuid.UUID
	BillingAddressID    uuid.UUID
	AppliedDiscountID   *uuid.UUID
	AppliedCouponCodeID *uuid.UUID
	Metadata            map[string]any
	ExpiresAt           *time.Time
	CreatedAt           time.Time
}

// Order represents a placed order.
type Order struct {
	ID                uuid.UUID
	Number            string
	CustomerID        *uuid.UUID
	Status            OrderStatus
	PaymentStatus     PaymentStatus
	FulfillmentStatus FulfillmentStatus
	CurrencyCode      string
	Subtotal          int
	DiscountTotal     int
	ShippingTotal     int
	TaxTotal          int
	Total             int
	ShippingAddressID uuid.UUID
	BillingAddressID  uuid.UUID
	SubscriptionID    *uuid.UUID
	DraftByUserID     *uuid.UUID
	TaxExempt         bool
	TaxExemptReason   *string
	StripeTaxID            *string
	StripePaymentIntentID  *string
	Notes                  *string
	Metadata          map[string]any
	PlacedAt          time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// LineItem represents a single line in an order.
type LineItem struct {
	ID            uuid.UUID
	OrderID       uuid.UUID
	VariantID     uuid.UUID
	Quantity      int
	UnitPrice     int
	Subtotal      int
	DiscountTotal int
	TaxTotal      int
	Total         int
	Metadata      map[string]any
}

// Adjustment represents an order-level or line-item-level adjustment.
type Adjustment struct {
	ID         uuid.UUID
	OrderID    uuid.UUID
	LineItemID *uuid.UUID
	Label      string
	Amount     int
	SourceType string
	SourceID   uuid.UUID
}
