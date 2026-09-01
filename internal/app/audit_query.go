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

// Count returns how many entries match the filter, ignoring pagination. The
// list page needs it for "X–Y of Z"; it must be given the same filter List got
// or the total contradicts the rows.
func (s *AuditQueryService) Count(ctx context.Context, tx pgx.Tx, f store.AuditFilter) (int, error) {
	count, err := s.entries.Count(ctx, tx, f)
	if err != nil {
		return 0, fmt.Errorf("count audit entries: %w", err)
	}
	return count, nil
}

// AuditFacets holds the dropdown options for the audit list, read from what the log
// actually contains rather than from the full set of action constants — see
// AuditStore.ListActionAreas for why.
//
// Actions is populated only when an area is selected: the whole point of the
// two-step control is that you never face a list of every action in the system.
type AuditFacets struct {
	Areas         []string
	Actions       []string
	ResourceTypes []string
}

// ListFacets returns the filter options for the audit list page.
func (s *AuditQueryService) ListFacets(ctx context.Context, tx pgx.Tx, area string) (AuditFacets, error) {
	var f AuditFacets
	var err error

	if f.Areas, err = s.entries.ListActionAreas(ctx, tx); err != nil {
		return AuditFacets{}, fmt.Errorf("list audit action areas: %w", err)
	}
	if f.ResourceTypes, err = s.entries.ListResourceTypes(ctx, tx); err != nil {
		return AuditFacets{}, fmt.Errorf("list audit resource types: %w", err)
	}
	if area != "" {
		if f.Actions, err = s.entries.ListActionsInArea(ctx, tx, area); err != nil {
			return AuditFacets{}, fmt.Errorf("list audit actions in area: %w", err)
		}
	}
	return f, nil
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
