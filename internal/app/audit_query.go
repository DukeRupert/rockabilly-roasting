package app

import (
	"context"
	"fmt"

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
