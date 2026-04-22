package app

import (
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/email"
)

// EmailEnv bundles the dependencies services need to compose and send
// transactional email. It is injected into any service that exposes Send*
// methods (AuthService, OrderService, SubscriptionService, InvoiceService,
// WholesaleService).
//
// Fields are non-nil after NewX wiring in main; a service that is constructed
// without a populated EmailEnv must not call its Send* methods.
type EmailEnv struct {
	Mailer     email.Sender
	Renderer   *emailtemplates.Renderer
	FromAddr   string
	BaseURL    string
	StoreName  string
	StaffEmail string // only used by WholesaleService.SendApplicationNotice; safe to leave empty otherwise
}
