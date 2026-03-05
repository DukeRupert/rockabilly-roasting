package domain

import (
	"time"

	"github.com/google/uuid"
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
	IsGuest         bool
	TaxExempt       bool
	TaxExemptReason *string
	StripeCustomerID *string
	CustomerGroupID  *uuid.UUID
	Metadata         map[string]any
	CreatedAt       time.Time
	UpdatedAt       time.Time
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
