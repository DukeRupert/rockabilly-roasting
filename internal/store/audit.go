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

// ListForCustomer returns audit entries that relate to a single customer:
// either entries where the customer was the actor (self-service actions, login,
// logout) or where the customer was the resource (staff actions on the customer,
// wholesale events). Ordered newest first, capped at limit.
func (s *AuditStore) ListForCustomer(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, limit int) ([]domain.AuditEntry, error) {
	if limit <= 0 {
		limit = 25
	}
	query := `SELECT id, actor_type, actor_id, actor_name, action, resource_type, resource_id,
	                 after_snapshot, request_id, ip_address, reason, metadata, created_at
	          FROM audit_log
	          WHERE (actor_type = 'customer' AND actor_id = $1)
	             OR (resource_type = 'customer' AND resource_id = $1)
	          ORDER BY created_at DESC
	          LIMIT $2`
	rows, err := tx.Query(ctx, query, customerID, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit for customer: %w", err)
	}
	defer rows.Close()

	var entries []domain.AuditEntry
	for rows.Next() {
		var e domain.AuditEntry
		var actorType string
		var metadataJSON json.RawMessage
		if err := rows.Scan(
			&e.ID, &actorType, &e.ActorID, &e.ActorName,
			&e.Action, &e.ResourceType, &e.ResourceID,
			&e.AfterSnapshot, &e.RequestID, &e.IPAddress,
			&e.Reason, &metadataJSON, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan audit entry: %w", err)
		}
		e.ActorType = domain.AuditActorType(actorType)
		e.Metadata = metadataFromJSON(metadataJSON)
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit entries: %w", err)
	}
	return entries, nil
}

// AuditFilter holds optional filters for listing audit entries.
type AuditFilter struct {
	ActorType    *string
	Action       *string
	ResourceType *string
	Limit        int
	Offset       int
}

// List returns audit entries matching the given filter, paginated.
func (s *AuditStore) List(ctx context.Context, tx pgx.Tx, f AuditFilter) ([]domain.AuditEntry, error) {
	query := `SELECT id, actor_type, actor_id, actor_name, action, resource_type, resource_id,
	                  after_snapshot, request_id, ip_address, reason, metadata, created_at
	           FROM audit_log WHERE 1=1`
	args := []any{}
	argN := 1

	if f.ActorType != nil {
		query += fmt.Sprintf(" AND actor_type = $%d", argN)
		args = append(args, *f.ActorType)
		argN++
	}
	if f.Action != nil {
		query += fmt.Sprintf(" AND action = $%d", argN)
		args = append(args, *f.Action)
		argN++
	}
	if f.ResourceType != nil {
		query += fmt.Sprintf(" AND resource_type = $%d", argN)
		args = append(args, *f.ResourceType)
		argN++
	}

	query += " ORDER BY created_at DESC"

	if f.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argN)
		args = append(args, f.Limit)
		argN++
	}
	if f.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argN)
		args = append(args, f.Offset)
		argN++
	}

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit entries: %w", err)
	}
	defer rows.Close()

	var entries []domain.AuditEntry
	for rows.Next() {
		var e domain.AuditEntry
		var actorType string
		var metadataJSON json.RawMessage
		if err := rows.Scan(
			&e.ID, &actorType, &e.ActorID, &e.ActorName,
			&e.Action, &e.ResourceType, &e.ResourceID,
			&e.AfterSnapshot, &e.RequestID, &e.IPAddress,
			&e.Reason, &metadataJSON, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan audit entry: %w", err)
		}
		e.ActorType = domain.AuditActorType(actorType)
		e.Metadata = metadataFromJSON(metadataJSON)
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit entries: %w", err)
	}
	return entries, nil
}

// ListByResourceIDsWithActionPrefix returns audit entries for any of the given
// resource ids whose action starts with the prefix, newest first.
//
// It exists because some events are recorded against a *related* resource
// rather than the one whose page you are looking at: a skipped delivery stop is
// audited against the order, not the route, so a route's own resource_id lookup
// would miss it. The prefix keeps the join narrow — a route page wants the
// stop's "delivery_route.*" events, not the order's entire history.
func (s *AuditStore) ListByResourceIDsWithActionPrefix(
	ctx context.Context,
	tx pgx.Tx,
	resourceType string,
	ids []uuid.UUID,
	actionPrefix string,
) ([]domain.AuditEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	rows, err := tx.Query(ctx,
		`SELECT id, actor_type, actor_id, actor_name, action, resource_type, resource_id,
		        after_snapshot, request_id, ip_address, reason, metadata, created_at
		   FROM audit_log
		  WHERE resource_type = $1 AND resource_id = ANY($2) AND action LIKE $3 || '%'
		  ORDER BY created_at DESC`,
		resourceType, ids, actionPrefix,
	)
	if err != nil {
		return nil, fmt.Errorf("list audit by resource ids: %w", err)
	}
	defer rows.Close()

	var entries []domain.AuditEntry
	for rows.Next() {
		var e domain.AuditEntry
		var actorType string
		var metadataJSON json.RawMessage
		if err := rows.Scan(
			&e.ID, &actorType, &e.ActorID, &e.ActorName,
			&e.Action, &e.ResourceType, &e.ResourceID,
			&e.AfterSnapshot, &e.RequestID, &e.IPAddress,
			&e.Reason, &metadataJSON, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan audit entry: %w", err)
		}
		e.ActorType = domain.AuditActorType(actorType)
		e.Metadata = metadataFromJSON(metadataJSON)
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit entries: %w", err)
	}
	return entries, nil
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
