package auth

import (
	"context"

	"github.com/dukerupert/hiri/internal/domain"
)

type contextKey string

const (
	customerKey     contextKey = "customer"
	customerUserKey contextKey = "customer_user"
	staffKey        contextKey = "staff"
)

// WithCustomer stores a customer in the context.
func WithCustomer(ctx context.Context, c *domain.Customer) context.Context {
	return context.WithValue(ctx, customerKey, c)
}

// CustomerFromContext retrieves the authenticated customer from context.
func CustomerFromContext(ctx context.Context) (*domain.Customer, bool) {
	c, ok := ctx.Value(customerKey).(*domain.Customer)
	return c, ok
}

// WithCustomerUser stores the acting additional login in the context. This is
// set ALONGSIDE WithCustomer, never instead of it: the customer is the account
// that owns the data, the customer user is the human who signed in. Data access
// must always scope by the customer; the customer user is for display and for
// naming the actor in audit records.
//
// Absent for the account's primary login, which has no customer_users row.
func WithCustomerUser(ctx context.Context, u *domain.CustomerUser) context.Context {
	return context.WithValue(ctx, customerUserKey, u)
}

// CustomerUserFromContext retrieves the acting additional login, if the request
// was authenticated as one. Returns false for the account's primary login.
func CustomerUserFromContext(ctx context.Context) (*domain.CustomerUser, bool) {
	u, ok := ctx.Value(customerUserKey).(*domain.CustomerUser)
	return u, ok
}

// WithStaff stores a staff member in the context.
func WithStaff(ctx context.Context, s *domain.Staff) context.Context {
	return context.WithValue(ctx, staffKey, s)
}

// StaffFromContext retrieves the authenticated staff member from context.
func StaffFromContext(ctx context.Context) (*domain.Staff, bool) {
	s, ok := ctx.Value(staffKey).(*domain.Staff)
	return s, ok
}
