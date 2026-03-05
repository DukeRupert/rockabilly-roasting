package app

import (
	"github.com/google/uuid"

	"github.com/dukerupert/hiri/internal/domain"
)

// Actor represents the entity performing an action, used for audit records.
type Actor struct {
	Type domain.AuditActorType
	ID   *uuid.UUID
	Name string
}
