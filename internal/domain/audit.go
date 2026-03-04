package domain

import "github.com/google/uuid"

// AuditActorType identifies the type of actor performing an audited action.
type AuditActorType string

const (
	AuditActorTypeStaff  AuditActorType = "staff"
	AuditActorTypeSystem AuditActorType = "system"
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
