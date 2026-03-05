package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// CustomerService contains business logic for customer management.
type CustomerService struct {
	customers *store.CustomerStore
	audit     *audit.AuditWriter
	metrics   *metrics.Registry
}

// NewCustomerService creates a new CustomerService.
func NewCustomerService(customers *store.CustomerStore, audit *audit.AuditWriter, metrics *metrics.Registry) *CustomerService {
	return &CustomerService{
		customers: customers,
		audit:     audit,
		metrics:   metrics,
	}
}

// --- Self-service methods (no audit) ---

// GetCustomer returns a customer by ID.
func (s *CustomerService) GetCustomer(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Customer, error) {
	c, err := s.customers.GetByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("get customer: %w", err)
	}
	return c, nil
}

// GetCustomerByEmail returns a customer by email.
func (s *CustomerService) GetCustomerByEmail(ctx context.Context, tx pgx.Tx, email string) (*domain.Customer, error) {
	c, err := s.customers.GetByEmail(ctx, tx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("get customer by email: %w", err)
	}
	return c, nil
}

// UpdateEmail updates a customer's email after checking uniqueness.
func (s *CustomerService) UpdateEmail(ctx context.Context, tx pgx.Tx, id uuid.UUID, email string) (*domain.Customer, error) {
	existing, err := s.customers.GetByEmail(ctx, tx, email)
	if err == nil && existing.ID != id {
		return nil, ErrEmailAlreadyExists
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check email uniqueness: %w", err)
	}

	c, err := s.customers.UpdateEmail(ctx, tx, id, email)
	if err != nil {
		return nil, fmt.Errorf("update email: %w", err)
	}
	return c, nil
}

// UpdatePassword updates a customer's password hash.
func (s *CustomerService) UpdatePassword(ctx context.Context, tx pgx.Tx, id uuid.UUID, hash string) error {
	if err := s.customers.UpdatePassword(ctx, tx, id, hash); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return nil
}

// VerifyEmail marks a customer's email as verified.
func (s *CustomerService) VerifyEmail(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := s.customers.UpdateEmailVerified(ctx, tx, id, true); err != nil {
		return fmt.Errorf("verify email: %w", err)
	}
	return nil
}

// --- Staff-initiated methods (audited) ---

// UpdateCustomerGroup updates a customer's group and records an audit entry.
func (s *CustomerService) UpdateCustomerGroup(ctx context.Context, tx pgx.Tx, id uuid.UUID, groupID *uuid.UUID, actor Actor) error {
	if err := s.customers.UpdateCustomerGroup(ctx, tx, id, groupID); err != nil {
		return fmt.Errorf("update customer group: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerGroupChanged,
		ResourceType: "customer",
		ResourceID:   id,
		After:        map[string]any{"customer_group_id": groupID},
	}); err != nil {
		return fmt.Errorf("audit customer group changed: %w", err)
	}

	return nil
}

// GrantTaxExemption grants tax exemption to a customer and records an audit entry.
func (s *CustomerService) GrantTaxExemption(ctx context.Context, tx pgx.Tx, id uuid.UUID, reason string, actor Actor) error {
	if err := s.customers.UpdateTaxExempt(ctx, tx, id, true, &reason); err != nil {
		return fmt.Errorf("grant tax exemption: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerTaxExemptionGranted,
		ResourceType: "customer",
		ResourceID:   id,
		After:        map[string]any{"tax_exempt": true, "reason": reason},
	}); err != nil {
		return fmt.Errorf("audit tax exemption granted: %w", err)
	}

	return nil
}

// RevokeTaxExemption revokes tax exemption from a customer and records an audit entry.
func (s *CustomerService) RevokeTaxExemption(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) error {
	if err := s.customers.UpdateTaxExempt(ctx, tx, id, false, nil); err != nil {
		return fmt.Errorf("revoke tax exemption: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerTaxExemptionRevoked,
		ResourceType: "customer",
		ResourceID:   id,
		After:        map[string]any{"tax_exempt": false},
	}); err != nil {
		return fmt.Errorf("audit tax exemption revoked: %w", err)
	}

	return nil
}

// ListCustomers returns customers matching the given filter.
func (s *CustomerService) ListCustomers(ctx context.Context, tx pgx.Tx, f store.CustomerFilter) ([]domain.Customer, error) {
	customers, err := s.customers.List(ctx, tx, f)
	if err != nil {
		return nil, fmt.Errorf("list customers: %w", err)
	}
	return customers, nil
}

// --- Address methods ---

// CreateAddress creates a new address.
func (s *CustomerService) CreateAddress(ctx context.Context, tx pgx.Tx, p store.CreateAddressParams) (*domain.Address, error) {
	addr, err := s.customers.CreateAddress(ctx, tx, p)
	if err != nil {
		return nil, fmt.Errorf("create address: %w", err)
	}
	return addr, nil
}

// GetAddress returns an address by ID, scoped to a customer.
func (s *CustomerService) GetAddress(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID) (*domain.Address, error) {
	addr, err := s.customers.GetAddress(ctx, tx, id, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAddressNotFound
		}
		return nil, fmt.Errorf("get address: %w", err)
	}
	return addr, nil
}

// ListAddresses returns all addresses for a customer.
func (s *CustomerService) ListAddresses(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) ([]domain.Address, error) {
	addrs, err := s.customers.ListAddresses(ctx, tx, customerID)
	if err != nil {
		return nil, fmt.Errorf("list addresses: %w", err)
	}
	return addrs, nil
}

// DeleteAddress removes an address, scoped to a customer.
func (s *CustomerService) DeleteAddress(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID) error {
	if err := s.customers.DeleteAddress(ctx, tx, id, customerID); err != nil {
		return fmt.Errorf("delete address: %w", err)
	}
	return nil
}
