package testutil

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// AuditRow is a lightweight representation of an audit_log row for test assertions.
type AuditRow struct {
	ID            uuid.UUID
	ActorType     string
	ActorName     string
	Action        string
	ResourceType  string
	ResourceID    uuid.UUID
	AfterSnapshot json.RawMessage
	Reason        *string
	CreatedAt     time.Time
}

// LastAuditEntry returns the most recent audit_log entry for the given resource.
// Fails the test if no entry is found. Ordered by id DESC to handle entries with
// identical created_at timestamps.
func LastAuditEntry(t *testing.T, tx pgx.Tx, resourceType string, resourceID uuid.UUID) AuditRow {
	t.Helper()
	var row AuditRow
	err := tx.QueryRow(context.Background(),
		`SELECT id, actor_type, actor_name, action, resource_type, resource_id,
		        after_snapshot, reason, created_at
		 FROM audit_log
		 WHERE resource_type = $1 AND resource_id = $2
		 ORDER BY created_at DESC, id DESC
		 LIMIT 1`,
		resourceType, resourceID,
	).Scan(
		&row.ID, &row.ActorType, &row.ActorName, &row.Action,
		&row.ResourceType, &row.ResourceID, &row.AfterSnapshot,
		&row.Reason, &row.CreatedAt,
	)
	require.NoError(t, err, "expected audit entry for %s/%s", resourceType, resourceID)
	return row
}

// LastAuditEntryWithAction returns the most recent audit_log entry for the given
// resource and action. Fails the test if no entry is found.
func LastAuditEntryWithAction(t *testing.T, tx pgx.Tx, resourceType string, resourceID uuid.UUID, action string) AuditRow {
	t.Helper()
	var row AuditRow
	err := tx.QueryRow(context.Background(),
		`SELECT id, actor_type, actor_name, action, resource_type, resource_id,
		        after_snapshot, reason, created_at
		 FROM audit_log
		 WHERE resource_type = $1 AND resource_id = $2 AND action = $3
		 ORDER BY created_at DESC, id DESC
		 LIMIT 1`,
		resourceType, resourceID, action,
	).Scan(
		&row.ID, &row.ActorType, &row.ActorName, &row.Action,
		&row.ResourceType, &row.ResourceID, &row.AfterSnapshot,
		&row.Reason, &row.CreatedAt,
	)
	require.NoError(t, err, "expected audit entry for %s/%s with action %s", resourceType, resourceID, action)
	return row
}

// AssertNoAuditEntry asserts that no audit_log entry exists for the given resource.
func AssertNoAuditEntry(t *testing.T, tx pgx.Tx, resourceType string, resourceID uuid.UUID) {
	t.Helper()
	var count int
	err := tx.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE resource_type = $1 AND resource_id = $2`,
		resourceType, resourceID,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count, "expected no audit entry for %s/%s, found %d", resourceType, resourceID, count)
}
