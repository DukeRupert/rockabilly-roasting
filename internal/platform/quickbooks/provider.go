package quickbooks

import (
	"context"
	"time"

	"github.com/google/uuid"

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

// Credentials holds the encrypted OAuth2 tokens for a QB connection.
type Credentials struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	RealmID          string
	AccessToken      string // encrypted
	RefreshToken     string // encrypted
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Client is the interface for QuickBooks Online API operations.
type Client interface {
	// CreateCustomer creates a customer in QBO and returns their QB customer ID.
	CreateCustomer(ctx context.Context, c *domain.Customer) (qbCustomerID string, err error)

	// UpdateCustomer updates an existing QBO customer.
	UpdateCustomer(ctx context.Context, qbID string, c *domain.Customer) error

	// CreateInvoice creates an invoice in QBO.
	CreateInvoice(ctx context.Context, p InvoiceParams) (*Invoice, error)

	// GetInvoice fetches the current state of an invoice from QBO.
	GetInvoice(ctx context.Context, qbInvoiceID string) (*Invoice, error)
}
