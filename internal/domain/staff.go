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

// CanManageStaff reports whether the role may access staff (team) management.
// This mirrors the PermManageStaff grant in platform/auth.rolePermissions — that
// map is the enforcement source of truth (the requirePermission middleware). This
// helper exists only so the UI layer (which may import domain but not
// platform/auth) can hide the Team nav affordance for non-managers; keep the two
// in sync if the permission is reassigned.
func (r StaffRole) CanManageStaff() bool {
	return r == StaffRoleAdmin
}

// CanManageSystem reports whether the role may reach the operator-level pages:
// failed background jobs and the Settings section. Admin only — retrying a job
// re-runs real work including sending mail, and a settings change moves the
// rules under every order at once. Mirrors the PermManageSystem grant in
// platform/auth.rolePermissions, which is the enforcement source of truth; this
// helper exists so the UI layer can hide the affordances. Keep the two in sync.
func (r StaffRole) CanManageSystem() bool {
	return r == StaffRoleAdmin
}

// Valid reports whether the role is one of the known staff roles.
func (r StaffRole) Valid() bool {
	switch r {
	case StaffRoleAdmin, StaffRoleFulfillment, StaffRoleFinance, StaffRoleCatalog, StaffRoleSupport:
		return true
	default:
		return false
	}
}

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
