package quickbooks

import (
	"context"
	"math"
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
	ID        string    // QB internal invoice ID
	DocNumber string    // human-readable invoice number
	Balance   float64   // remaining balance in dollars (0 = fully paid)
	TotalAmt  float64   // invoice total in dollars; 0 on a voided invoice
	DueDate   time.Time // payment due date (net terms); zero if QB omitted it
}

// BalanceCents returns the remaining balance in integer cents, rounded to the
// nearest cent. QB carries money as float dollars; callers compare in cents.
func (i Invoice) BalanceCents() int { return dollarsToCents(i.Balance) }

// TotalCents returns the invoice total in integer cents, rounded to the nearest
// cent. A voided QB invoice zeroes its amounts, so TotalCents() <= 0 is the
// signal that an invoice is no longer a live bill.
func (i Invoice) TotalCents() int { return dollarsToCents(i.TotalAmt) }

// dollarsToCents converts QB's float dollars to integer cents. Rounding (not
// truncation) avoids float artifacts like 0.999999 collapsing to 99 cents.
func dollarsToCents(dollars float64) int {
	return int(math.Round(dollars * 100))
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
