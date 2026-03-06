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
