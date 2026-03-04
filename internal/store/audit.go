package store

// AuditStore provides database access for audit log entries.
type AuditStore struct{}

// NewAuditStore creates a new AuditStore.
func NewAuditStore() *AuditStore {
	return &AuditStore{}
}
