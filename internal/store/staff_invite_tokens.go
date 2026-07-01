package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// StaffInviteTokenStore provides database access for staff invite / password-setup
// tokens. It mirrors MagicLinkStore but binds to a staff row rather than a
// customer (magic_link_tokens is NOT NULL FK to customers, so staff need their
// own table).
type StaffInviteTokenStore struct{}

// NewStaffInviteTokenStore creates a new StaffInviteTokenStore.
func NewStaffInviteTokenStore() *StaffInviteTokenStore {
	return &StaffInviteTokenStore{}
}

// StaffInviteToken represents a stored staff invite token.
type StaffInviteToken struct {
	ID        uuid.UUID
	StaffID   uuid.UUID
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

// Create inserts a new staff invite token.
func (s *StaffInviteTokenStore) Create(ctx context.Context, tx pgx.Tx, staffID uuid.UUID, tokenHash string, expiresAt time.Time) (*StaffInviteToken, error) {
	row, err := sqlcgen.New(tx).CreateStaffInviteToken(ctx, sqlcgen.CreateStaffInviteTokenParams{
		ID:        uuid.New(),
		StaffID:   staffID,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("create staff invite token: %w", err)
	}
	return staffInviteTokenFromRow(row), nil
}

// Redeem atomically marks a token as used if it is valid and unused. Returns
// pgx.ErrNoRows if the token is expired, already used, or not found.
func (s *StaffInviteTokenStore) Redeem(ctx context.Context, tx pgx.Tx, tokenHash string) (*StaffInviteToken, error) {
	row, err := sqlcgen.New(tx).RedeemStaffInviteToken(ctx, tokenHash)
	if err != nil {
		return nil, fmt.Errorf("redeem staff invite token: %w", err)
	}
	return staffInviteTokenFromRow(row), nil
}

// Lookup returns a valid (unused, unexpired) token without consuming it. Returns
// pgx.ErrNoRows if no such token exists. Use this to validate a token before
// showing the set-password form; Redeem to consume it on submission.
func (s *StaffInviteTokenStore) Lookup(ctx context.Context, tx pgx.Tx, tokenHash string) (*StaffInviteToken, error) {
	row, err := sqlcgen.New(tx).GetValidStaffInviteToken(ctx, tokenHash)
	if err != nil {
		return nil, fmt.Errorf("lookup staff invite token: %w", err)
	}
	return staffInviteTokenFromRow(row), nil
}

// DeleteExpired removes all expired or used tokens.
func (s *StaffInviteTokenStore) DeleteExpired(ctx context.Context, tx pgx.Tx) error {
	if err := sqlcgen.New(tx).DeleteExpiredStaffInviteTokens(ctx); err != nil {
		return fmt.Errorf("delete expired staff invite tokens: %w", err)
	}
	return nil
}

func staffInviteTokenFromRow(r sqlcgen.StaffInviteToken) *StaffInviteToken {
	t := &StaffInviteToken{
		ID:        r.ID,
		StaffID:   r.StaffID,
		TokenHash: r.TokenHash,
		ExpiresAt: r.ExpiresAt,
		CreatedAt: r.CreatedAt,
	}
	if r.UsedAt.Valid {
		t.UsedAt = &r.UsedAt.Time
	}
	return t
}
