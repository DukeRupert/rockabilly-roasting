package payments

import "context"

// PaymentIntentStatus represents the state of a payment intent.
type PaymentIntentStatus string

const (
	PaymentIntentStatusRequiresPaymentMethod PaymentIntentStatus = "requires_payment_method"
	PaymentIntentStatusRequiresConfirmation  PaymentIntentStatus = "requires_confirmation"
	PaymentIntentStatusRequiresAction        PaymentIntentStatus = "requires_action"
	PaymentIntentStatusProcessing            PaymentIntentStatus = "processing"
	PaymentIntentStatusSucceeded             PaymentIntentStatus = "succeeded"
	PaymentIntentStatusCanceled              PaymentIntentStatus = "canceled"
	PaymentIntentStatusRequiresCapture       PaymentIntentStatus = "requires_capture"
)

// RefundStatus represents the state of a refund.
type RefundStatus string

const (
	RefundStatusSucceeded RefundStatus = "succeeded"
	RefundStatusPending   RefundStatus = "pending"
	RefundStatusFailed    RefundStatus = "failed"
	RefundStatusCanceled  RefundStatus = "canceled"
)

// CreatePaymentIntentRequest contains the data needed to create a payment intent.
type CreatePaymentIntentRequest struct {
	AmountCents      int64
	Currency         string
	CustomerID       string            // Stripe customer ID (cus_xxx)
	PaymentMethodID  string            // optional, for off-session payments
	Metadata         map[string]string // order ID, subscription ID, etc.
	OffSession       bool              // true for subscription renewals
	SetupFutureUsage string            // "off_session" to save PM for later charges
	ShippingAddress  *ShippingAddress  // for automatic tax calculation
}

// ShippingAddress is the address passed to Stripe for tax calculation.
type ShippingAddress struct {
	Name       string
	Line1      string
	Line2      string
	City       string
	State      string
	PostalCode string
	Country    string
}

// PaymentIntent represents the result of a payment intent operation.
type PaymentIntent struct {
	ID              string
	ClientSecret    string
	Status          PaymentIntentStatus
	AmountCents     int64
	Currency        string
	PaymentMethodID string
	Metadata        map[string]string
}

// RefundRequest contains the data needed to create a refund.
type RefundRequest struct {
	PaymentIntentID string
	AmountCents     int64  // 0 = full refund
	Reason          string // duplicate, fraudulent, requested_by_customer
}

// RefundResult represents the result of a refund operation.
type RefundResult struct {
	ID          string
	Status      RefundStatus
	AmountCents int64
}

// CreateCustomerRequest contains the data needed to create a Stripe customer.
type CreateCustomerRequest struct {
	Email    string
	Name     string
	Metadata map[string]string
}

// Customer represents a Stripe customer.
type Customer struct {
	ID                     string
	Email                  string
	Name                   string
	DefaultPaymentMethodID string
}

// PaymentMethod represents a saved payment method.
type PaymentMethod struct {
	ID   string
	Type string
	Card *CardDetails
}

// CardDetails contains card-specific payment method details.
type CardDetails struct {
	Last4    string
	Brand    string
	ExpMonth int64
	ExpYear  int64
}

// WebhookEvent represents a verified Stripe webhook event.
type WebhookEvent struct {
	ID   string
	Type string
	Data []byte // raw JSON for downstream routing
}

// Provider is the interface for payment processing services.
type Provider interface {
	// CreatePaymentIntent creates a new payment intent for a checkout.
	CreatePaymentIntent(ctx context.Context, req CreatePaymentIntentRequest) (*PaymentIntent, error)

	// GetPaymentIntent retrieves an existing payment intent by ID.
	GetPaymentIntent(ctx context.Context, paymentIntentID string) (*PaymentIntent, error)

	// CancelPaymentIntent cancels a payment intent that has not been captured.
	CancelPaymentIntent(ctx context.Context, paymentIntentID string) error

	// Refund creates a refund for a payment intent.
	Refund(ctx context.Context, req RefundRequest) (*RefundResult, error)

	// CreateCustomer creates a Stripe customer for the given email.
	CreateCustomer(ctx context.Context, req CreateCustomerRequest) (*Customer, error)

	// GetCustomer retrieves a Stripe customer by ID.
	GetCustomer(ctx context.Context, customerID string) (*Customer, error)

	// AttachPaymentMethod attaches a payment method to a customer.
	AttachPaymentMethod(ctx context.Context, paymentMethodID string, customerID string) error

	// DetachPaymentMethod detaches a payment method from its customer.
	DetachPaymentMethod(ctx context.Context, paymentMethodID string) error

	// ListPaymentMethods lists a customer's saved payment methods.
	ListPaymentMethods(ctx context.Context, customerID string) ([]PaymentMethod, error)

	// CreatePortalSession creates a Stripe Billing Portal session for a customer
	// and returns the hosted URL the customer should be redirected to. The
	// returnURL is where the portal sends the customer when they are done.
	//
	// This opens the full portal: billing history, invoices, every saved payment
	// method. Only hand it to a customer who is already signed in.
	CreatePortalSession(ctx context.Context, customerID, returnURL string) (string, error)

	// CreatePaymentMethodUpdateSession creates a Billing Portal session scoped to
	// updating the payment method and nothing else.
	//
	// It exists so a past-due dunning email can offer a one-click "fix your card"
	// link. The holder of that link is not authenticated — it arrives by email and
	// could be forwarded — so the session must not expose billing history or
	// invoices the way the full portal does. Stripe enforces the narrowing on
	// their side via flow_data; do not reimplement this by calling
	// CreatePortalSession and hoping the customer only touches one page.
	CreatePaymentMethodUpdateSession(ctx context.Context, customerID, returnURL string) (string, error)

	// ConstructWebhookEvent verifies a webhook signature and parses the event.
	ConstructWebhookEvent(payload []byte, signature string) (*WebhookEvent, error)
}
