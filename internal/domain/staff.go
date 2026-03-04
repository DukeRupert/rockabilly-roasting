package domain

import (
	"time"

	"github.com/google/uuid"
)

// StaffRole represents the role assigned to a staff member.
type StaffRole string

const (
	StaffRoleAdmin       StaffRole = "admin"
	StaffRoleFulfillment StaffRole = "fulfillment"
	StaffRoleFinance     StaffRole = "finance"
	StaffRoleCatalog     StaffRole = "catalog"
	StaffRoleSupport     StaffRole = "support"
)

// Staff represents an internal staff member.
type Staff struct {
	ID           uuid.UUID
	Email        string
	Name         string
	PasswordHash string
	Role         StaffRole
	IsActive     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
