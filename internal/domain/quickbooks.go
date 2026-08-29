package domain

import (
	"time"

	"github.com/google/uuid"
)

// QBCredentials holds the encrypted OAuth2 tokens for a QuickBooks Online connection.
// Token fields are encrypted at rest — decryption happens in the platform/quickbooks package.
type QBCredentials struct {
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

// QBBillingMode controls whether the QuickBooks chain is allowed to move
// money. It is stored on store_settings and flipped from the admin, so a proof
// period can be started and ended without a deploy.
type QBBillingMode string

const (
	// QBBillingModeShadow runs the whole invoice chain and records what it
	// would have billed, without writing anything to QBO or emailing anyone.
	// Read-only QBO lookups still happen: matching a wholesale account to a
	// QBO customer is the part most worth proving, and it cannot be proven
	// offline.
	QBBillingModeShadow QBBillingMode = "shadow"

	// QBBillingModeLive creates invoices in QBO and has QBO email them.
	QBBillingModeLive QBBillingMode = "live"
)

// DefaultQBBillingMode is what an unconfigured shop bills in. Shadow: a deploy
// must never be the thing that starts invoicing customers.
const DefaultQBBillingMode = QBBillingModeShadow

// Valid reports whether m is a mode this binary understands. An unknown value
// read from the database is treated as shadow by the caller rather than
// trusted — the safe direction for a column that decides whether real
// customers get billed.
func (m QBBillingMode) Valid() bool {
	return m == QBBillingModeShadow || m == QBBillingModeLive
}

// IsLive reports whether real invoices should be created and sent. Anything
// this binary does not recognise reads as not-live.
func (m QBBillingMode) IsLive() bool { return m == QBBillingModeLive }

// QBInvoiceLinePreview is one line of an invoice that would have been created.
type QBInvoiceLinePreview struct {
	Description string `json:"description"`
	Quantity    int    `json:"quantity"`
	UnitCents   int    `json:"unit_cents"`
	AmountCents int    `json:"amount_cents"`
}

// QBInvoicePreview is what a live run would have billed for one order. It is
// written by the invoice job while in shadow mode and read by the admin list
// and the weekly digest.
type QBInvoicePreview struct {
	ID         uuid.UUID
	OrderID    uuid.UUID
	CustomerID *uuid.UUID

	// QBCustomerID is the QBO customer a read-only lookup matched.
	// WouldCreateCustomer is true when nothing matched and a live run would
	// have created one — the most important thing a proof period surfaces.
	QBCustomerID        *string
	WouldCreateCustomer bool

	DocNumber     string
	BillEmail     string
	TermsDays     int
	DueDate       time.Time
	SubtotalCents int
	ShippingCents int
	TotalCents    int
	TermID        *string
	Lines         []QBInvoiceLinePreview

	// ExistingQBInvoiceID is set when QBO already holds an invoice with this
	// DocNumber, which during a proof period usually means someone billed the
	// order by hand.
	ExistingQBInvoiceID *string

	// LookupError records a failed read-only QBO call. The preview is still
	// written, because an order missing from the list would read as "nothing
	// to bill".
	LookupError *string

	// AutoBilled is false when the account is on manual billing and nothing
	// would have invoiced this order without a human. Such orders are still
	// recorded: absent from the list, they would read as nothing to bill.
	AutoBilled bool
	// BillingMethod as it stood when the order was previewed, because "why
	// was this one not billed" gets asked after the customer has changed.
	BillingMethod BillingMethod

	CreatedAt time.Time
	UpdatedAt time.Time
}

// NeedsAttention reports whether this preview is something a human should look
// at before going live, rather than a clean row.
//
// A manual account is not a problem — it is a decision already taken, and
// flagging every one of them would bury the rows that genuinely need someone.
func (p QBInvoicePreview) NeedsAttention() bool { return p.Problem() != "" }

// AwaitingManualInvoice reports that this order will not be billed by anything
// unless a person does it. Distinct from NeedsAttention: nothing is wrong, but
// the money does not arrive on its own.
func (p QBInvoicePreview) AwaitingManualInvoice() bool { return !p.AutoBilled }

// Problem states, in one phrase, what has to be resolved about this would-be
// invoice before billing goes live. Empty means the row is clean.
//
// It lives here rather than in the page or the digest so that both say the
// same thing: staff should not have to translate between the email that told
// them to look and the screen they look at.
func (p QBInvoicePreview) Problem() string {
	switch {
	// Deliberately first and deliberately empty-handed: an account nobody
	// bills automatically has no billing problem. The other checks below ask
	// whether an invoice would succeed, which is not a question about an
	// invoice that is never going to be attempted.
	case !p.AutoBilled:
		return ""
	case p.LookupError != nil:
		return "QuickBooks lookup failed: " + *p.LookupError
	case p.ExistingQBInvoiceID != nil:
		return "QuickBooks already has an invoice numbered " + p.DocNumber + " — it was probably billed by hand."
	case p.WouldCreateCustomer:
		return "No matching QuickBooks customer. Going live would create a new one."
	case p.BillEmail == "":
		return "No bill-to address, so QuickBooks could not email this invoice."
	}
	return ""
}
