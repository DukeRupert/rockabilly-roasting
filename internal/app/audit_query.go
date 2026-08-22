package app

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
)

// AuditQueryService provides read-only access to the audit log for admin views.
// It never writes — audit entries are written by other services as part of their
// own transactions via the platform/audit package.
type AuditQueryService struct {
	entries *store.AuditStore
}

// NewAuditQueryService creates a new AuditQueryService.
func NewAuditQueryService(entries *store.AuditStore) *AuditQueryService {
	return &AuditQueryService{entries: entries}
}

// List returns audit entries matching the filter, paginated.
func (s *AuditQueryService) List(ctx context.Context, tx pgx.Tx, f store.AuditFilter) ([]domain.AuditEntry, error) {
	entries, err := s.entries.List(ctx, tx, f)
	if err != nil {
		return nil, fmt.Errorf("list audit entries: %w", err)
	}
	return entries, nil
}

// ListByResource returns all audit entries for a single resource, newest first.
// Used to render per-resource activity timelines on detail pages.
func (s *AuditQueryService) ListByResource(ctx context.Context, tx pgx.Tx, resourceType string, resourceID uuid.UUID) ([]domain.AuditEntry, error) {
	entries, err := s.entries.ListByResource(ctx, tx, resourceType, resourceID)
	if err != nil {
		return nil, fmt.Errorf("list audit by resource: %w", err)
	}
	return entries, nil
}

// ListByRelatedResource returns entries recorded against related resources —
// see AuditStore.ListByResourceIDsWithActionPrefix for why a page sometimes has
// to look somewhere other than its own resource_id.
func (s *AuditQueryService) ListByRelatedResource(ctx context.Context, tx pgx.Tx, resourceType string, ids []uuid.UUID, actionPrefix string) ([]domain.AuditEntry, error) {
	entries, err := s.entries.ListByResourceIDsWithActionPrefix(ctx, tx, resourceType, ids, actionPrefix)
	if err != nil {
		return nil, fmt.Errorf("list audit by related resource: %w", err)
	}
	return entries, nil
}

// ListForCustomer returns audit entries that relate to a single customer —
// either as actor (self-service actions, login, logout) or as resource (staff
// actions on the customer, wholesale events). Newest first, capped at limit.
func (s *AuditQueryService) ListForCustomer(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, limit int) ([]domain.AuditEntry, error) {
	entries, err := s.entries.ListForCustomer(ctx, tx, customerID, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit for customer: %w", err)
	}
	return entries, nil
}
