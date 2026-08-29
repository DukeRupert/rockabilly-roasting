package domain

import (
	"time"

	"github.com/google/uuid"
)

// OrderChannel records the sales channel an order was placed through, frozen at
// placement time. It is a property of the order — not derived live from the
// customer's account_type — so the order keeps its original channel even if the
// customer is later converted, suspended, or deleted.
type OrderChannel string

const (
	OrderChannelRetail    OrderChannel = "retail"
	OrderChannelWholesale OrderChannel = "wholesale"
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
	PaymentStatusAwaiting       PaymentStatus = "awaiting"
	PaymentStatusAuthorized     PaymentStatus = "authorized"
	PaymentStatusCaptured       PaymentStatus = "captured"
	PaymentStatusPartial        PaymentStatus = "partial"
	PaymentStatusRefunded       PaymentStatus = "refunded"
	PaymentStatusFailed         PaymentStatus = "failed"
	PaymentStatusVoided         PaymentStatus = "voided"
	PaymentStatusPendingInvoice PaymentStatus = "pending_invoice"
	PaymentStatusInvoiced       PaymentStatus = "invoiced"
	PaymentStatusPartiallyPaid  PaymentStatus = "partially_paid"
	PaymentStatusOverdue        PaymentStatus = "overdue"
)

// FulfillmentStatus represents the fulfillment state of an order.
type FulfillmentStatus string

const (
	FulfillmentStatusUnfulfilled        FulfillmentStatus = "unfulfilled"
	FulfillmentStatusPartiallyFulfilled FulfillmentStatus = "partially_fulfilled"
	FulfillmentStatusFulfilled          FulfillmentStatus = "fulfilled"
	FulfillmentStatusReadyForPickup     FulfillmentStatus = "ready_for_pickup"
	FulfillmentStatusPartiallyShipped   FulfillmentStatus = "partially_shipped"
	FulfillmentStatusShipped            FulfillmentStatus = "shipped"
	FulfillmentStatusPartiallyDelivered FulfillmentStatus = "partially_delivered"
	FulfillmentStatusDelivered          FulfillmentStatus = "delivered"
	FulfillmentStatusReturned           FulfillmentStatus = "returned"
)

// ShippingMethod represents how an order is fulfilled.
type ShippingMethod string

const (
	ShippingMethodPickup        ShippingMethod = "pickup"
	ShippingMethodLocalDelivery ShippingMethod = "local_delivery"
	ShippingMethodShipped       ShippingMethod = "shipped"
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

// CartItem represents a line item in a shopping cart.
type CartItem struct {
	ID        uuid.UUID
	CartID    uuid.UUID
	VariantID uuid.UUID
	Quantity  int
	// UnitPrice is the resolved effective price in cents at the time the item
	// was added. For wholesale carts this reflects the customer's assigned
	// price list; for retail, the base price. This value is the contract:
	// PlaceOrder / PlaceWholesaleOrder writes it verbatim to the line item.
	UnitPrice int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Order represents a placed order.
type Order struct {
	ID                    uuid.UUID
	Number                string
	CustomerID            *uuid.UUID
	Channel               OrderChannel
	Status                OrderStatus
	PaymentStatus         PaymentStatus
	FulfillmentStatus     FulfillmentStatus
	CurrencyCode          string
	Subtotal              int
	DiscountTotal         int
	ShippingTotal         int
	TaxTotal              int
	Total                 int
	ShippingAddressID     uuid.UUID
	BillingAddressID      uuid.UUID
	SubscriptionID        *uuid.UUID
	DraftByUserID         *uuid.UUID
	TaxExempt             bool
	TaxExemptReason       *string
	StripeTaxID           *string
	StripePaymentIntentID *string
	QBInvoiceID           *string
	QBInvoiceNo           *string
	QBSyncedAt            *time.Time
	ShippingMethod        *ShippingMethod
	RequestedDeliveryDate *time.Time
	// ScheduledDeliveryDate is the local-delivery run this order was promised
	// to when it was placed, resolved against the delivery weekdays and cutoff
	// in ShippingConfig. Nil for every other fulfillment method, and for local
	// orders placed before the schedule existed.
	//
	// It is frozen at placement rather than recomputed on read: it is a promise
	// already sent in the confirmation email, so changing the route in admin
	// settings must not silently rewrite what a customer was told.
	ScheduledDeliveryDate *time.Time
	CustomerPONumber      *string
	InternalNote          *string
	Notes                 *string
	// OverdueReminderStage is the highest past-due reminder milestone (in days
	// since PlacedAt) already notified for this order's QB invoice. It is the
	// dedup ledger for the reconciliation poll so each milestone (7/14/21/30)
	// emails exactly once. Only populated by the QB-path order reads
	// (GetOrderByQBInvoiceID*, ListWholesaleOpenInvoiceOrders); zero elsewhere.
	OverdueReminderStage int
	Metadata             map[string]any
	PlacedAt             time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
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

// DailyRevenue is one day's revenue and order count, bucketed in the
// merchant's local timezone. Cancelled and refunded orders are excluded.
type DailyRevenue struct {
	Date       time.Time
	Cents      int
	OrderCount int
}

// ProductSales is one product's units sold and revenue over a window.
// Cancelled and refunded orders are excluded. WeightGrams is the total
// shipped weight across all units (qty × variant weight) — zero for
// products without configured variant weights.
type ProductSales struct {
	ProductID   uuid.UUID
	Title       string
	Units       int
	WeightGrams int
	Revenue     int
}

// DeliveryLoadLine is one product's total across a set of local-delivery
// orders — how much of that coffee has to be on the van before it leaves.
// Units is the bag count; WeightGrams is quantity × variant weight summed
// across every matching line item.
//
// UnitsMissingWeight counts the bags whose variant has no weight_grams
// configured. Those contribute nothing to WeightGrams, so the load list
// surfaces the count rather than quietly under-reporting the load — a driver
// trusting a short total is exactly the failure this feature exists to
// prevent.
type DeliveryLoadLine struct {
	ProductID          uuid.UUID
	Title              string
	Units              int
	WeightGrams        int
	UnitsMissingWeight int
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
