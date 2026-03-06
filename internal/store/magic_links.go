package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// MagicLinkStore provides database access for magic link tokens.
type MagicLinkStore struct{}

// NewMagicLinkStore creates a new MagicLinkStore.
func NewMagicLinkStore() *MagicLinkStore {
	return &MagicLinkStore{}
}

// MagicLinkToken represents a stored magic link token.
type MagicLinkToken struct {
	ID         uuid.UUID
	CustomerID uuid.UUID
	TokenHash  string
	ExpiresAt  time.Time
	UsedAt     *time.Time
	CreatedAt  time.Time
}

// Create inserts a new magic link token.
func (s *MagicLinkStore) Create(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, tokenHash string, expiresAt time.Time) (*MagicLinkToken, error) {
	row, err := sqlcgen.New(tx).CreateMagicLinkToken(ctx, sqlcgen.CreateMagicLinkTokenParams{
		ID:         uuid.New(),
		CustomerID: customerID,
		TokenHash:  tokenHash,
		ExpiresAt:  expiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("create magic link token: %w", err)
	}
	return magicLinkFromRow(row), nil
}

// Redeem atomically marks a token as used if it is valid and unused.
// Returns pgx.ErrNoRows if the token is expired, already used, or not found.
func (s *MagicLinkStore) Redeem(ctx context.Context, tx pgx.Tx, tokenHash string) (*MagicLinkToken, error) {
	row, err := sqlcgen.New(tx).RedeemMagicLinkToken(ctx, tokenHash)
	if err != nil {
		return nil, fmt.Errorf("redeem magic link token: %w", err)
	}
	return magicLinkFromRow(row), nil
}

// DeleteExpired removes all expired or used tokens.
func (s *MagicLinkStore) DeleteExpired(ctx context.Context, tx pgx.Tx) error {
	if err := sqlcgen.New(tx).DeleteExpiredMagicLinkTokens(ctx); err != nil {
		return fmt.Errorf("delete expired magic link tokens: %w", err)
	}
	return nil
}

func magicLinkFromRow(r sqlcgen.MagicLinkToken) *MagicLinkToken {
	t := &MagicLinkToken{
		ID:         r.ID,
		CustomerID: r.CustomerID,
		TokenHash:  r.TokenHash,
		ExpiresAt:  r.ExpiresAt,
		CreatedAt:  r.CreatedAt,
	}
	if r.UsedAt.Valid {
		t.UsedAt = &r.UsedAt.Time
	}
	return t
}
