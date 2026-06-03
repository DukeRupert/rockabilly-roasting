package domain

import (
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
	BillingMethodManual     BillingMethod = "manual"
	BillingMethodACH        BillingMethod = "ach"
	BillingMethodCreditCard BillingMethod = "credit_card"
)

// Customer represents a registered or guest customer.
type Customer struct {
	ID              uuid.UUID
	Email           string
	EmailVerified   bool
	PasswordHash    *string
	FirstName       string
	LastName        string
	Phone           *string
	TaxExempt       bool
	TaxExemptReason *string
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
	Metadata         map[string]any
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// IsApprovedWholesale reports whether the customer is a wholesale account in good
// standing — i.e. eligible for wholesale catalog visibility and pricing.
func (c *Customer) IsApprovedWholesale() bool {
	return c.AccountType == AccountTypeWholesale &&
		c.WholesaleStatus != nil &&
		*c.WholesaleStatus == WholesaleStatusApproved
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

// CustomerGroup represents a pricing group (e.g., Retail, Wholesale, VIP).
type CustomerGroup struct {
	ID       uuid.UUID
	Name     string
	Metadata map[string]any
}

// CustomerGroupMembership links a customer to a customer group.
type CustomerGroupMembership struct {
	CustomerID      uuid.UUID
	CustomerGroupID uuid.UUID
	AssignedAt      time.Time
}
