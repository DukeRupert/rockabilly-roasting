package auth

import (
	"context"

	"github.com/dukerupert/hiri/internal/domain"
)

type contextKey string

const (
	customerKey contextKey = "customer"
	staffKey    contextKey = "staff"
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

// WithStaff stores a staff member in the context.
func WithStaff(ctx context.Context, s *domain.Staff) context.Context {
	return context.WithValue(ctx, staffKey, s)
}

// StaffFromContext retrieves the authenticated staff member from context.
func StaffFromContext(ctx context.Context) (*domain.Staff, bool) {
	s, ok := ctx.Value(staffKey).(*domain.Staff)
	return s, ok
}
