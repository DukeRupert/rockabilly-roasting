package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// AuditStore provides database access for audit log entries.
type AuditStore struct{}

// NewAuditStore creates a new AuditStore.
func NewAuditStore() *AuditStore {
	return &AuditStore{}
}

// CreateAuditEntryParams holds the fields needed to create an audit entry.
type CreateAuditEntryParams struct {
	ActorType     domain.AuditActorType
	ActorID       *uuid.UUID
	ActorName     string
	Action        string
	ResourceType  string
	ResourceID    uuid.UUID
	AfterSnapshot json.RawMessage
	RequestID     string
	IPAddress     *string
	Reason        *string
	Metadata      map[string]any
}

// Create inserts a new audit log entry and returns it.
func (s *AuditStore) Create(ctx context.Context, tx pgx.Tx, p CreateAuditEntryParams) (*domain.AuditEntry, error) {
	row, err := sqlcgen.New(tx).CreateAuditEntry(ctx, sqlcgen.CreateAuditEntryParams{
		ID:            uuid.New(),
		ActorType:     string(p.ActorType),
		ActorID:       p.ActorID,
		ActorName:     p.ActorName,
		Action:        p.Action,
		ResourceType:  p.ResourceType,
		ResourceID:    p.ResourceID,
		AfterSnapshot: p.AfterSnapshot,
		RequestID:     p.RequestID,
		IpAddress:     p.IPAddress,
		Reason:        p.Reason,
		Metadata:      metadataToJSON(p.Metadata),
	})
	if err != nil {
		return nil, fmt.Errorf("insert audit entry: %w", err)
	}
	return auditEntryFromRow(row), nil
}

// ListByResource returns audit entries for a specific resource.
func (s *AuditStore) ListByResource(ctx context.Context, tx pgx.Tx, resourceType string, resourceID uuid.UUID) ([]domain.AuditEntry, error) {
	rows, err := sqlcgen.New(tx).ListAuditByResource(ctx, sqlcgen.ListAuditByResourceParams{
		ResourceType: resourceType,
		ResourceID:   resourceID,
	})
	if err != nil {
		return nil, fmt.Errorf("list audit by resource: %w", err)
	}
	return auditEntriesFromRows(rows), nil
}

// ListByActor returns audit entries for a specific actor.
func (s *AuditStore) ListByActor(ctx context.Context, tx pgx.Tx, actorID uuid.UUID) ([]domain.AuditEntry, error) {
	rows, err := sqlcgen.New(tx).ListAuditByActor(ctx, &actorID)
	if err != nil {
		return nil, fmt.Errorf("list audit by actor: %w", err)
	}
	return auditEntriesFromRows(rows), nil
}

// ListByAction returns audit entries for a specific action.
func (s *AuditStore) ListByAction(ctx context.Context, tx pgx.Tx, action string) ([]domain.AuditEntry, error) {
	rows, err := sqlcgen.New(tx).ListAuditByAction(ctx, action)
	if err != nil {
		return nil, fmt.Errorf("list audit by action: %w", err)
	}
	return auditEntriesFromRows(rows), nil
}

// --- Row converters ---

func auditEntryFromRow(r sqlcgen.AuditLog) *domain.AuditEntry {
	return &domain.AuditEntry{
		ID:            r.ID,
		ActorType:     domain.AuditActorType(r.ActorType),
		ActorID:       r.ActorID,
		ActorName:     r.ActorName,
		Action:        r.Action,
		ResourceType:  r.ResourceType,
		ResourceID:    r.ResourceID,
		AfterSnapshot: r.AfterSnapshot,
		RequestID:     r.RequestID,
		IPAddress:     r.IpAddress,
		Reason:        r.Reason,
		Metadata:      metadataFromJSON(r.Metadata),
		CreatedAt:     r.CreatedAt,
	}
}

func auditEntriesFromRows(rows []sqlcgen.AuditLog) []domain.AuditEntry {
	entries := make([]domain.AuditEntry, len(rows))
	for i, r := range rows {
		entries[i] = *auditEntryFromRow(r)
	}
	return entries
}
