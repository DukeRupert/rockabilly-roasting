package app

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// CustomerGroupService manages customer groups and memberships.
// Groups gate access to restricted products (storefront visibility); pricing is
// handled separately by price lists.
type CustomerGroupService struct {
	groups  *store.CustomerGroupStore
	audit   *audit.AuditWriter
	metrics *metrics.Registry
}

// NewCustomerGroupService creates a new CustomerGroupService.
func NewCustomerGroupService(groups *store.CustomerGroupStore, auditWriter *audit.AuditWriter, m *metrics.Registry) *CustomerGroupService {
	return &CustomerGroupService{
		groups:  groups,
		audit:   auditWriter,
		metrics: m,
	}
}

// --- Reads ---

// List returns all customer groups.
func (s *CustomerGroupService) List(ctx context.Context, tx pgx.Tx) ([]domain.CustomerGroup, error) {
	groups, err := s.groups.List(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("list customer groups: %w", err)
	}
	return groups, nil
}

// ListByCustomer returns the groups a customer belongs to.
func (s *CustomerGroupService) ListByCustomer(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) ([]domain.CustomerGroup, error) {
	groups, err := s.groups.ListByCustomer(ctx, tx, customerID)
	if err != nil {
		return nil, fmt.Errorf("list customer groups by customer: %w", err)
	}
	return groups, nil
}

// --- Writes ---

// Create creates a new customer group and records an audit entry.
func (s *CustomerGroupService) Create(ctx context.Context, tx pgx.Tx, name string, actor Actor) (*domain.CustomerGroup, error) {
	group, err := s.groups.Create(ctx, tx, name, nil)
	if err != nil {
		return nil, fmt.Errorf("create customer group: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerGroupCreated,
		ResourceType: "customer_group",
		ResourceID:   group.ID,
		After:        group,
	}); err != nil {
		return nil, fmt.Errorf("audit customer group created: %w", err)
	}

	return group, nil
}

// Delete removes a customer group and records an audit entry.
func (s *CustomerGroupService) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) error {
	if err := s.groups.Delete(ctx, tx, id); err != nil {
		return fmt.Errorf("delete customer group: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerGroupDeleted,
		ResourceType: "customer_group",
		ResourceID:   id,
	}); err != nil {
		return fmt.Errorf("audit customer group deleted: %w", err)
	}

	return nil
}

// AddMember adds a customer to a group and records an audit entry.
func (s *CustomerGroupService) AddMember(ctx context.Context, tx pgx.Tx, customerID, groupID uuid.UUID, actor Actor) error {
	if err := s.groups.AddMember(ctx, tx, customerID, groupID); err != nil {
		return fmt.Errorf("add customer group member: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerGroupMemberAdded,
		ResourceType: "customer",
		ResourceID:   customerID,
		Metadata:     map[string]any{"customer_group_id": groupID.String()},
	}); err != nil {
		return fmt.Errorf("audit customer group member added: %w", err)
	}

	return nil
}

// RemoveMember removes a customer from a group and records an audit entry.
func (s *CustomerGroupService) RemoveMember(ctx context.Context, tx pgx.Tx, customerID, groupID uuid.UUID, actor Actor) error {
	if err := s.groups.RemoveMember(ctx, tx, customerID, groupID); err != nil {
		return fmt.Errorf("remove customer group member: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditCustomerGroupMemberRemoved,
		ResourceType: "customer",
		ResourceID:   customerID,
		Metadata:     map[string]any{"customer_group_id": groupID.String()},
	}); err != nil {
		return fmt.Errorf("audit customer group member removed: %w", err)
	}

	return nil
}
