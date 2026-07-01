package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// StaffStore provides database access for staff members.
type StaffStore struct{}

// NewStaffStore creates a new StaffStore.
func NewStaffStore() *StaffStore {
	return &StaffStore{}
}

// GetByID returns a staff member by ID.
func (s *StaffStore) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Staff, error) {
	row, err := sqlcgen.New(tx).GetStaffByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get staff by id: %w", err)
	}
	return staffFromRow(row), nil
}

// GetByEmail returns a staff member by email.
func (s *StaffStore) GetByEmail(ctx context.Context, tx pgx.Tx, email string) (*domain.Staff, error) {
	row, err := sqlcgen.New(tx).GetStaffByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("get staff by email: %w", err)
	}
	return staffFromRow(row), nil
}

// Create inserts a new staff member.
func (s *StaffStore) Create(ctx context.Context, tx pgx.Tx, params sqlcgen.CreateStaffParams) (*domain.Staff, error) {
	row, err := sqlcgen.New(tx).CreateStaff(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("create staff: %w", err)
	}
	return staffFromRow(row), nil
}

// List returns staff members ordered by name, paginated.
func (s *StaffStore) List(ctx context.Context, tx pgx.Tx, limit, offset int32) ([]domain.Staff, error) {
	rows, err := sqlcgen.New(tx).ListStaff(ctx, sqlcgen.ListStaffParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list staff: %w", err)
	}
	staff := make([]domain.Staff, 0, len(rows))
	for _, r := range rows {
		staff = append(staff, *staffFromRow(r))
	}
	return staff, nil
}

// CountActiveByRole returns the number of active staff with the given role.
// Used to guard against removing the last active admin.
func (s *StaffStore) CountActiveByRole(ctx context.Context, tx pgx.Tx, role string) (int64, error) {
	n, err := sqlcgen.New(tx).CountActiveStaffByRole(ctx, role)
	if err != nil {
		return 0, fmt.Errorf("count active staff by role: %w", err)
	}
	return n, nil
}

// UpdateRole updates a staff member's role.
func (s *StaffStore) UpdateRole(ctx context.Context, tx pgx.Tx, id uuid.UUID, role string) error {
	if err := sqlcgen.New(tx).UpdateStaffRole(ctx, sqlcgen.UpdateStaffRoleParams{
		ID:   id,
		Role: role,
	}); err != nil {
		return fmt.Errorf("update staff role: %w", err)
	}
	return nil
}

// UpdateActive toggles a staff member's active flag.
func (s *StaffStore) UpdateActive(ctx context.Context, tx pgx.Tx, id uuid.UUID, active bool) error {
	if err := sqlcgen.New(tx).UpdateStaffActive(ctx, sqlcgen.UpdateStaffActiveParams{
		ID:       id,
		IsActive: active,
	}); err != nil {
		return fmt.Errorf("update staff active: %w", err)
	}
	return nil
}

// UpdatePassword updates a staff member's password hash.
func (s *StaffStore) UpdatePassword(ctx context.Context, tx pgx.Tx, id uuid.UUID, hash string) error {
	if err := sqlcgen.New(tx).UpdateStaffPassword(ctx, sqlcgen.UpdateStaffPasswordParams{
		ID:           id,
		PasswordHash: hash,
	}); err != nil {
		return fmt.Errorf("update staff password: %w", err)
	}
	return nil
}

func staffFromRow(r sqlcgen.Staff) *domain.Staff {
	return &domain.Staff{
		ID:           r.ID,
		Email:        r.Email,
		Name:         r.Name,
		PasswordHash: r.PasswordHash,
		Role:         domain.StaffRole(r.Role),
		IsActive:     r.IsActive,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}
}
