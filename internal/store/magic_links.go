package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// Token purposes scope which flow may redeem a magic_link_tokens row. Magic-link
// sign-in and wholesale password setup share the default purpose (they always
// shared this table); white-label invites get their own so an invite link can
// never be redeemed as a password-setup token.
const (
	MagicLinkPurposeDefault          = "magic_link"
	MagicLinkPurposeWhiteLabelInvite = "white_label_invite"
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
	Purpose    string
	ExpiresAt  time.Time
	UsedAt     *time.Time
	CreatedAt  time.Time
}

// Create inserts a new magic link token with the given purpose.
func (s *MagicLinkStore) Create(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, tokenHash, purpose string, expiresAt time.Time) (*MagicLinkToken, error) {
	row, err := sqlcgen.New(tx).CreateMagicLinkToken(ctx, sqlcgen.CreateMagicLinkTokenParams{
		ID:         uuid.New(),
		CustomerID: customerID,
		TokenHash:  tokenHash,
		ExpiresAt:  expiresAt,
		Purpose:    purpose,
	})
	if err != nil {
		return nil, fmt.Errorf("create magic link token: %w", err)
	}
	return magicLinkFromRow(row), nil
}

// Redeem atomically marks a token of the given purpose as used if it is valid
// and unused. Returns pgx.ErrNoRows if the token is expired, already used, of a
// different purpose, or not found.
func (s *MagicLinkStore) Redeem(ctx context.Context, tx pgx.Tx, tokenHash, purpose string) (*MagicLinkToken, error) {
	row, err := sqlcgen.New(tx).RedeemMagicLinkToken(ctx, sqlcgen.RedeemMagicLinkTokenParams{
		TokenHash: tokenHash,
		Purpose:   purpose,
	})
	if err != nil {
		return nil, fmt.Errorf("redeem magic link token: %w", err)
	}
	return magicLinkFromRow(row), nil
}

// Lookup returns a valid (unused, unexpired) token of the given purpose without
// consuming it. Returns pgx.ErrNoRows if no such token exists. Use this to
// validate a token before showing a form; Redeem to consume it on submission.
func (s *MagicLinkStore) Lookup(ctx context.Context, tx pgx.Tx, tokenHash, purpose string) (*MagicLinkToken, error) {
	row, err := sqlcgen.New(tx).GetValidMagicLinkToken(ctx, sqlcgen.GetValidMagicLinkTokenParams{
		TokenHash: tokenHash,
		Purpose:   purpose,
	})
	if err != nil {
		return nil, fmt.Errorf("lookup magic link token: %w", err)
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
		Purpose:    r.Purpose,
		ExpiresAt:  r.ExpiresAt,
		CreatedAt:  r.CreatedAt,
	}
	if r.UsedAt.Valid {
		t.UsedAt = &r.UsedAt.Time
	}
	return t
}
