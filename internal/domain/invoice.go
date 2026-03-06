package domain

import (
	"time"

	"github.com/google/uuid"
)

// InvoiceStatus represents the lifecycle state of an invoice.
type InvoiceStatus string

const (
	InvoiceStatusDraft        InvoiceStatus = "draft"
	InvoiceStatusSent         InvoiceStatus = "sent"
	InvoiceStatusPartiallyPaid InvoiceStatus = "partially_paid"
	InvoiceStatusPaid         InvoiceStatus = "paid"
	InvoiceStatusVoid         InvoiceStatus = "void"
)

// PaymentMethod represents how an invoice payment was made.
type PaymentMethod string

const (
	PaymentMethodStripe PaymentMethod = "stripe"
	PaymentMethodACH    PaymentMethod = "ach"
	PaymentMethodCheck  PaymentMethod = "check"
	PaymentMethodCash   PaymentMethod = "cash"
	PaymentMethodOther  PaymentMethod = "other"
)

// Invoice represents a bill sent to a customer for a wholesale order.
type Invoice struct {
	ID           uuid.UUID
	OrderID      uuid.UUID
	Number       string
	Status       InvoiceStatus
	Subtotal     int
	Shipping     int
	TaxTotal     int
	Total        int
	AmountPaid   int
	AmountDue    int
	DueDate      *time.Time
	Notes        *string
	InternalNote *string
	SentAt       *time.Time
	PaidAt       *time.Time
	VoidedAt     *time.Time
	CreatedBy    *uuid.UUID
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// InvoiceLine represents a single line item on an invoice.
type InvoiceLine struct {
	ID          uuid.UUID
	InvoiceID   uuid.UUID
	VariantID   *uuid.UUID
	Description string
	Quantity    int
	UnitPrice   int
	Total       int
}

// InvoicePayment represents a payment recorded against an invoice.
type InvoicePayment struct {
	ID         uuid.UUID
	InvoiceID  uuid.UUID
	Amount     int
	Method     PaymentMethod
	Reference  *string
	Note       *string
	RecordedBy *uuid.UUID
	PaidAt     time.Time
}
