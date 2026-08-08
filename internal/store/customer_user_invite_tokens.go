package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// CustomerUserInviteTokenStore provides database access for the invite /
// password-setup tokens issued to additional logins on a wholesale account.
// It mirrors StaffInviteTokenStore: magic_link_tokens is FK-locked to
// customers, so a customer_users row needs its own token table.
type CustomerUserInviteTokenStore struct{}

// NewCustomerUserInviteTokenStore creates a new CustomerUserInviteTokenStore.
func NewCustomerUserInviteTokenStore() *CustomerUserInviteTokenStore {
	return &CustomerUserInviteTokenStore{}
}

// CustomerUserInviteToken represents a stored invite token.
type CustomerUserInviteToken struct {
	ID             uuid.UUID
	CustomerUserID uuid.UUID
	TokenHash      string
	ExpiresAt      time.Time
	UsedAt         *time.Time
	CreatedAt      time.Time
}

// Create inserts a new invite token.
func (s *CustomerUserInviteTokenStore) Create(ctx context.Context, tx pgx.Tx, customerUserID uuid.UUID, tokenHash string, expiresAt time.Time) (*CustomerUserInviteToken, error) {
	row, err := sqlcgen.New(tx).CreateCustomerUserInviteToken(ctx, sqlcgen.CreateCustomerUserInviteTokenParams{
		ID:             uuid.New(),
		CustomerUserID: customerUserID,
		TokenHash:      tokenHash,
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("create customer user invite token: %w", err)
	}
	return customerUserInviteTokenFromRow(row), nil
}

// Redeem atomically marks a token as used if it is valid and unused. Returns
// pgx.ErrNoRows if the token is expired, already used, or not found.
func (s *CustomerUserInviteTokenStore) Redeem(ctx context.Context, tx pgx.Tx, tokenHash string) (*CustomerUserInviteToken, error) {
	row, err := sqlcgen.New(tx).RedeemCustomerUserInviteToken(ctx, tokenHash)
	if err != nil {
		return nil, fmt.Errorf("redeem customer user invite token: %w", err)
	}
	return customerUserInviteTokenFromRow(row), nil
}

// Lookup returns a valid (unused, unexpired) token without consuming it. Use
// this to validate before rendering the set-password form; Redeem to consume it
// on submission.
func (s *CustomerUserInviteTokenStore) Lookup(ctx context.Context, tx pgx.Tx, tokenHash string) (*CustomerUserInviteToken, error) {
	row, err := sqlcgen.New(tx).GetValidCustomerUserInviteToken(ctx, tokenHash)
	if err != nil {
		return nil, fmt.Errorf("lookup customer user invite token: %w", err)
	}
	return customerUserInviteTokenFromRow(row), nil
}

// DeleteExpired removes all expired or used tokens.
func (s *CustomerUserInviteTokenStore) DeleteExpired(ctx context.Context, tx pgx.Tx) error {
	if err := sqlcgen.New(tx).DeleteExpiredCustomerUserInviteTokens(ctx); err != nil {
		return fmt.Errorf("delete expired customer user invite tokens: %w", err)
	}
	return nil
}

func customerUserInviteTokenFromRow(r sqlcgen.CustomerUserInviteToken) *CustomerUserInviteToken {
	t := &CustomerUserInviteToken{
		ID:             r.ID,
		CustomerUserID: r.CustomerUserID,
		TokenHash:      r.TokenHash,
		ExpiresAt:      r.ExpiresAt,
		CreatedAt:      r.CreatedAt,
	}
	if r.UsedAt.Valid {
		t.UsedAt = &r.UsedAt.Time
	}
	return t
}
