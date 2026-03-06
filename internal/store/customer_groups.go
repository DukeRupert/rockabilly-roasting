package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// CustomerGroupStore provides database access for customer groups and memberships.
type CustomerGroupStore struct{}

// NewCustomerGroupStore creates a new CustomerGroupStore.
func NewCustomerGroupStore() *CustomerGroupStore {
	return &CustomerGroupStore{}
}

// Create inserts a new customer group and returns it.
func (s *CustomerGroupStore) Create(ctx context.Context, tx pgx.Tx, name string, metadata map[string]any) (*domain.CustomerGroup, error) {
	row, err := sqlcgen.New(tx).CreateCustomerGroup(ctx, sqlcgen.CreateCustomerGroupParams{
		ID:       uuid.New(),
		Name:     name,
		Metadata: metadataToJSON(metadata),
	})
	if err != nil {
		return nil, fmt.Errorf("insert customer group: %w", err)
	}
	return customerGroupFromRow(row), nil
}

// GetByID returns a customer group by ID.
func (s *CustomerGroupStore) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.CustomerGroup, error) {
	row, err := sqlcgen.New(tx).GetCustomerGroupByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get customer group %s: %w", id, err)
	}
	return customerGroupFromRow(row), nil
}

// List returns all customer groups.
func (s *CustomerGroupStore) List(ctx context.Context, tx pgx.Tx) ([]domain.CustomerGroup, error) {
	rows, err := sqlcgen.New(tx).ListCustomerGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("list customer groups: %w", err)
	}
	groups := make([]domain.CustomerGroup, len(rows))
	for i, r := range rows {
		groups[i] = *customerGroupFromRow(r)
	}
	return groups, nil
}

// Delete removes a customer group by ID.
func (s *CustomerGroupStore) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteCustomerGroup(ctx, id); err != nil {
		return fmt.Errorf("delete customer group: %w", err)
	}
	return nil
}

// AddMember adds a customer to a group.
func (s *CustomerGroupStore) AddMember(ctx context.Context, tx pgx.Tx, customerID, groupID uuid.UUID) error {
	if err := sqlcgen.New(tx).AddCustomerGroupMembership(ctx, sqlcgen.AddCustomerGroupMembershipParams{
		CustomerID:      customerID,
		CustomerGroupID: groupID,
	}); err != nil {
		return fmt.Errorf("add customer group membership: %w", err)
	}
	return nil
}

// RemoveMember removes a customer from a group.
func (s *CustomerGroupStore) RemoveMember(ctx context.Context, tx pgx.Tx, customerID, groupID uuid.UUID) error {
	if err := sqlcgen.New(tx).RemoveCustomerGroupMembership(ctx, sqlcgen.RemoveCustomerGroupMembershipParams{
		CustomerID:      customerID,
		CustomerGroupID: groupID,
	}); err != nil {
		return fmt.Errorf("remove customer group membership: %w", err)
	}
	return nil
}

// ListByCustomer returns all groups a customer belongs to.
func (s *CustomerGroupStore) ListByCustomer(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) ([]domain.CustomerGroup, error) {
	rows, err := sqlcgen.New(tx).ListCustomerGroupsByCustomer(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("list customer groups by customer: %w", err)
	}
	groups := make([]domain.CustomerGroup, len(rows))
	for i, r := range rows {
		groups[i] = *customerGroupFromRow(r)
	}
	return groups, nil
}

func customerGroupFromRow(r sqlcgen.CustomerGroup) *domain.CustomerGroup {
	return &domain.CustomerGroup{
		ID:       r.ID,
		Name:     r.Name,
		Metadata: metadataFromJSON(r.Metadata),
	}
}
