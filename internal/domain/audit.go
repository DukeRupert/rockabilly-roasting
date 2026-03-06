package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// AuditActorType identifies the type of actor performing an audited action.
type AuditActorType string

const (
	AuditActorTypeStaff    AuditActorType = "staff"
	AuditActorTypeCustomer AuditActorType = "customer"
	AuditActorTypeSystem   AuditActorType = "system"
)

// StaffActor represents the staff member performing an action (for audit).
type StaffActor struct {
	ID   uuid.UUID
	Name string
	Type AuditActorType
}

// SystemActor is the sentinel actor for system-initiated actions.
var SystemActor = StaffActor{
	ID:   uuid.Nil,
	Name: "system",
	Type: AuditActorTypeSystem,
}

// AuditEntry represents a row in the audit_log table.
type AuditEntry struct {
	ID            uuid.UUID
	ActorType     AuditActorType
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
	CreatedAt     time.Time
}
