package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
)

// AuditEntry represents a single audit log record to be written.
type AuditEntry struct {
	ActorType    domain.AuditActorType
	ActorID      *uuid.UUID
	ActorName    string
	Action       string
	ResourceType string
	ResourceID   uuid.UUID
	After        any
	RequestID    string
	IPAddress    *string
	Reason       *string
	Metadata     map[string]any
}

// AuditWriter writes audit entries to the database.
type AuditWriter struct{}

// NewAuditWriter creates a new AuditWriter.
func NewAuditWriter() *AuditWriter {
	return &AuditWriter{}
}

// Record writes an audit entry within the given transaction.
func (w *AuditWriter) Record(ctx context.Context, tx pgx.Tx, entry AuditEntry) error {
	afterJSON, err := json.Marshal(entry.After)
	if err != nil {
		return fmt.Errorf("audit marshal after: %w", err)
	}

	metadataJSON, err := json.Marshal(entry.Metadata)
	if err != nil {
		return fmt.Errorf("audit marshal metadata: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO audit_log (id, actor_type, actor_id, actor_name, action, resource_type, resource_id, after_snapshot, request_id, ip_address, reason, metadata, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, now())`,
		uuid.New(),
		entry.ActorType,
		entry.ActorID,
		entry.ActorName,
		entry.Action,
		entry.ResourceType,
		entry.ResourceID,
		afterJSON,
		entry.RequestID,
		entry.IPAddress,
		entry.Reason,
		metadataJSON,
	)
	if err != nil {
		return fmt.Errorf("audit insert: %w", err)
	}

	return nil
}
