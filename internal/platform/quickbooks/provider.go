package quickbooks

import (
	"context"
	"time"

	"github.com/dukerupert/hiri/internal/domain"
)

// InvoiceParams holds the data needed to create a QB invoice.
type InvoiceParams struct {
	CustomerID string
	DocNumber  string
	DueDate    time.Time
	Lines      []InvoiceLine
	Shipping   int // shipping amount in cents
}

// InvoiceLine represents a single line item on a QB invoice.
type InvoiceLine struct {
	Description string
	Quantity    int
	UnitAmount  int // in cents
	Amount      int // total in cents (quantity * unit_amount)
}

// Invoice represents a QuickBooks invoice.
type Invoice struct {
	ID        string  // QB internal invoice ID
	DocNumber string  // human-readable invoice number
	Balance   float64 // remaining balance (0 = fully paid)
}

// QBCustomer represents a customer record returned from a QBO query.
type QBCustomer struct {
	ID          string // QB internal customer ID
	DisplayName string
	Email       string
}

// PaymentParams holds the data needed to record a payment in QBO.
type PaymentParams struct {
	CustomerID  string  // QB customer ID
	InvoiceID   string  // QB invoice ID to apply payment against
	Amount      int     // payment amount in cents
	Method      string  // payment method (check, cash, etc.)
	Reference   string  // optional reference (check number, etc.)
}

// Payment represents a recorded payment in QBO.
type Payment struct {
	ID     string  // QB payment ID
	Amount float64 // payment amount
}

// Client is the interface for QuickBooks Online API operations.
type Client interface {
	// FindCustomer searches QBO for an existing customer by display name, then
	// by email. Returns nil (not an error) if no match is found.
	FindCustomer(ctx context.Context, displayName, email string) (*QBCustomer, error)

	// CreateCustomer creates a customer in QBO and returns their QB customer ID.
	CreateCustomer(ctx context.Context, c *domain.Customer) (qbCustomerID string, err error)

	// UpdateCustomer updates an existing QBO customer.
	UpdateCustomer(ctx context.Context, qbID string, c *domain.Customer) error

	// CreateInvoice creates an invoice in QBO.
	CreateInvoice(ctx context.Context, p InvoiceParams) (*Invoice, error)

	// GetInvoice fetches the current state of an invoice from QBO.
	GetInvoice(ctx context.Context, qbInvoiceID string) (*Invoice, error)

	// CreatePayment records a payment against a QB invoice.
	CreatePayment(ctx context.Context, p PaymentParams) (*Payment, error)
}
