package domain

import (
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AccountType distinguishes retail from wholesale customers.
type AccountType string

const (
	AccountTypeRetail    AccountType = "retail"
	AccountTypeWholesale AccountType = "wholesale"
)

// WholesaleStatus represents the approval state of a wholesale account.
type WholesaleStatus string

const (
	WholesaleStatusPending   WholesaleStatus = "pending"
	WholesaleStatusApproved  WholesaleStatus = "approved"
	WholesaleStatusSuspended WholesaleStatus = "suspended"
	WholesaleStatusDeclined  WholesaleStatus = "declined"
)

// BillingMethod controls how a wholesale customer is billed.
type BillingMethod string

const (
	// BillingMethodManual is an account nobody has an invoicing and payment
	// agreement with. They are invoiced by hand, so automated QuickBooks
	// billing leaves them alone.
	BillingMethodManual BillingMethod = "manual"
	// BillingMethodACH and BillingMethodCreditCard are accounts that have
	// agreed to be invoiced and to pay online. Both are billed automatically;
	// they differ only in which pay button the invoice carries.
	BillingMethodACH        BillingMethod = "ach"
	BillingMethodCreditCard BillingMethod = "credit_card"
)

// AutoInvoiced reports whether QuickBooks should bill this account without
// anybody asking.
//
// Only an account with an agreement is billed automatically. Manual is the
// default and the honest answer for a customer who has never been told their
// invoices would start arriving from QuickBooks with a pay-now button on them
// — sending one is a change to a commercial relationship, not a feature.
//
// A value this binary does not recognise reads as manual, which is the safe
// direction: not billing someone is recoverable, billing them is not.
func (m BillingMethod) AutoInvoiced() bool {
	return m == BillingMethodACH || m == BillingMethodCreditCard
}

// OnlineACHAllowed reports whether an invoice for this account may carry an
// ACH pay button. Card accounts get the card button instead; a manual account
// gets neither, and is not invoiced automatically at all.
func (m BillingMethod) OnlineACHAllowed() bool { return m == BillingMethodACH }

// OnlineCardAllowed reports whether an invoice may carry a card pay button.
// Card fees are opt-in per account, so this is never on by default.
func (m BillingMethod) OnlineCardAllowed() bool { return m == BillingMethodCreditCard }

// DefaultPaymentTermsDays is the NET terms applied to wholesale invoicing when
// a customer has no explicit PaymentTermsDays set (net-7 is the house default).
const DefaultPaymentTermsDays = 7

// PaymentTermsDueOnReceipt is terms of zero days — payable immediately. It is
// a real selectable value, distinct from "no terms set" (a nil
// PaymentTermsDays), which falls back to DefaultPaymentTermsDays.
const PaymentTermsDueOnReceipt = 0

// PaymentTermsOptions are the NET terms a wholesale account may be put on, in
// days. The set mirrors QuickBooks Online's stock Terms — Due on receipt, Net
// 10, Net 15, Net 30, Net 60 — plus Net 7, which is the house default and what
// every account migrated from Orderspace runs on. Net 7 has no QBO stock
// equivalent and is created there as a custom Term.
//
// Net 21 was offered until 2026-08-29 and retired when nothing used it: it
// matched no QBO stock Term and so bought a second custom Term for nothing.
var PaymentTermsOptions = []int{
	PaymentTermsDueOnReceipt,
	7,
	10,
	15,
	30,
	60,
}

// ValidPaymentTermsDays reports whether d is a terms value an account may be
// set to. Callers that accept terms from outside (admin forms, importers)
// should gate on this rather than keeping their own list.
func ValidPaymentTermsDays(d int) bool {
	for _, opt := range PaymentTermsOptions {
		if d == opt {
			return true
		}
	}
	return false
}

// PaymentTermsLabel renders terms for display. Zero days is "Due on receipt",
// not "Net 0", and a negative value — which nothing in the app can set but the
// column does not forbid — is named as the nonsense it is rather than rendered
// as "Net -3".
func PaymentTermsLabel(days int) string {
	switch {
	case days == PaymentTermsDueOnReceipt:
		return "Due on receipt"
	case days < 0:
		return "Invalid terms"
	}
	return "Net " + strconv.Itoa(days)
}

// IsLegacyPaymentTerms reports whether days is a terms value the shop no
// longer offers but a customer may still be stored on — Net 21, retired
// 2026-08-29. It is deliberately not just !ValidPaymentTermsDays: a negative
// value is corrupt rather than legacy, and offering it back in a select the
// handler would reject makes a dead end out of the page's own suggestion.
func IsLegacyPaymentTerms(days int) bool {
	return days > 0 && !ValidPaymentTermsDays(days)
}

// Customer represents a registered or guest customer.
type Customer struct {
	ID               uuid.UUID
	Email            string
	EmailVerified    bool
	PasswordHash     *string
	FirstName        string
	LastName         string
	Phone            *string
	TaxExempt        bool
	TaxExemptReason  *string
	StripeCustomerID *string
	AccountType      AccountType
	WholesaleStatus  *WholesaleStatus
	CompanyName      *string
	Website          *string
	WholesaleNotes   *string
	ApprovedAt       *time.Time
	ApprovedBy       *uuid.UUID
	PaymentTermsDays *int
	BillingMethod    BillingMethod
	QBCustomerID     *string
	QBSyncedAt       *time.Time
	PriceListID      *uuid.UUID
	TwoFAEnabled     bool
	TwoFAMethod      *string
	// PreferredLocalFulfillment is the customer's saved choice for orders
	// shipping to a local zip. Nil means "ask each time at checkout".
	// Persisted only as ShippingMethodPickup or ShippingMethodLocalDelivery;
	// ShippingMethodShipped is never stored here (it's the non-local fallback).
	PreferredLocalFulfillment *ShippingMethod
	// OrderRemindersEnabled controls whether this account receives the weekly
	// wholesale order reminder. Staff can clear it per customer; defaults true.
	OrderRemindersEnabled bool
	Metadata              map[string]any
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// IsApprovedWholesale reports whether the customer is a wholesale account in good
// standing — i.e. eligible for wholesale catalog visibility and pricing.
func (c *Customer) IsApprovedWholesale() bool {
	return c.AccountType == AccountTypeWholesale &&
		c.WholesaleStatus != nil &&
		*c.WholesaleStatus == WholesaleStatusApproved
}

// OrderReminderRecipient is one account on the weekly wholesale order-reminder
// list: an approved wholesale customer that ordered inside the lookback window
// and has not opted out. LastOrderAt is carried so the admin preview can show
// staff why each account qualified.
type OrderReminderRecipient struct {
	CustomerID  uuid.UUID
	Email       string
	CompanyName *string
	FirstName   string
	LastName    string
	LastOrderAt time.Time
}

// DisplayName is the label staff recognize an account by — company name for a
// wholesale account, falling back to the contact's name, then the address.
func (r OrderReminderRecipient) DisplayName() string {
	if r.CompanyName != nil && strings.TrimSpace(*r.CompanyName) != "" {
		return strings.TrimSpace(*r.CompanyName)
	}
	if name := strings.TrimSpace(r.FirstName + " " + r.LastName); name != "" {
		return name
	}
	return r.Email
}

// Address represents a shipping or billing address.
type Address struct {
	ID          uuid.UUID
	CustomerID  *uuid.UUID
	FirstName   string
	LastName    string
	Company     *string
	Line1       string
	Line2       *string
	City        string
	State       string
	PostalCode  string
	CountryCode string
	IsDefault   bool
}

// NormalizeEmail canonicalizes an email address for storage and lookup.
//
// Email lookups are exact string matches (`WHERE email = $1`), so an address
// must be reduced to one canonical form before it is either stored or used to
// find a row. Without this, "Info@example.com" and "info@example.com" are two
// different accounts: sign-in fails for the customer who capitalizes, and
// registration happily creates a duplicate beside the row it could not see.
//
// The domain part of an address is case-insensitive per RFC 5321, and while the
// local part is technically case-sensitive, no mail provider in practice treats
// it otherwise. Lowercasing the whole address is the pragmatic choice every
// major platform makes, and matches what cmd/os-migrate already did on import.
//
// Apply at every boundary where an address enters the system -- both sides of
// the comparison must be normalized or the fix only half works.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
