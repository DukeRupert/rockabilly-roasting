package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// SessionStore provides database access for sessions, reset tokens, and email verifications.
type SessionStore struct{}

// NewSessionStore creates a new SessionStore.
func NewSessionStore() *SessionStore {
	return &SessionStore{}
}

// hashToken returns the SHA-256 hex digest of a raw token.
func hashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// --- Sessions ---

// Create inserts a new session and returns it along with the raw token.
// The raw token is returned to the caller (for setting a cookie) but only the
// hash is stored in the database.
func (s *SessionStore) Create(
	ctx context.Context,
	tx pgx.Tx,
	actorType domain.SessionActorType,
	actorID uuid.UUID,
	expiresAt time.Time,
	ipAddress *string,
	userAgent *string,
) (*domain.Session, string, error) {
	rawToken := uuid.New().String()
	tokenHash := hashToken(rawToken)

	row, err := sqlcgen.New(tx).CreateSession(ctx, sqlcgen.CreateSessionParams{
		ID:        uuid.New(),
		ActorType: string(actorType),
		ActorID:   actorID,
		TokenHash: tokenHash,
		IpAddress: ipAddress,
		UserAgent: userAgent,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return nil, "", fmt.Errorf("insert session: %w", err)
	}

	return sessionFromRow(row), rawToken, nil
}

// GetByToken looks up a session by raw token. Returns the session if it exists
// and has not expired.
func (s *SessionStore) GetByToken(ctx context.Context, tx pgx.Tx, rawToken string) (*domain.Session, error) {
	row, err := sqlcgen.New(tx).GetSessionByTokenHash(ctx, hashToken(rawToken))
	if err != nil {
		return nil, fmt.Errorf("get session by token: %w", err)
	}
	return sessionFromRow(row), nil
}

// UpdateLastSeen bumps the last_seen_at timestamp for a session.
func (s *SessionStore) UpdateLastSeen(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) error {
	if err := sqlcgen.New(tx).UpdateSessionLastSeen(ctx, sessionID); err != nil {
		return fmt.Errorf("update session last_seen: %w", err)
	}
	return nil
}

// Revoke deletes a single session.
func (s *SessionStore) Revoke(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) error {
	if err := sqlcgen.New(tx).RevokeSession(ctx, sessionID); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

// RevokeAllForActor deletes all sessions for a given actor (e.g., on password change).
func (s *SessionStore) RevokeAllForActor(ctx context.Context, tx pgx.Tx, actorType domain.SessionActorType, actorID uuid.UUID) error {
	err := sqlcgen.New(tx).RevokeAllSessionsForActor(ctx, sqlcgen.RevokeAllSessionsForActorParams{
		ActorType: string(actorType),
		ActorID:   actorID,
	})
	if err != nil {
		return fmt.Errorf("revoke all sessions for actor: %w", err)
	}
	return nil
}

// PruneExpired removes all expired sessions.
func (s *SessionStore) PruneExpired(ctx context.Context, tx pgx.Tx) (int64, error) {
	n, err := sqlcgen.New(tx).PruneExpiredSessions(ctx)
	if err != nil {
		return 0, fmt.Errorf("prune expired sessions: %w", err)
	}
	return n, nil
}

// --- Reset Tokens ---

// CreateResetToken inserts a reset token and returns the raw token.
func (s *SessionStore) CreateResetToken(
	ctx context.Context,
	tx pgx.Tx,
	actorType domain.SessionActorType,
	actorID uuid.UUID,
	expiresAt time.Time,
) (*domain.ResetToken, string, error) {
	rawToken := uuid.New().String()
	tokenHash := hashToken(rawToken)

	row, err := sqlcgen.New(tx).CreateResetToken(ctx, sqlcgen.CreateResetTokenParams{
		ID:        uuid.New(),
		ActorType: string(actorType),
		ActorID:   actorID,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return nil, "", fmt.Errorf("insert reset token: %w", err)
	}

	return resetTokenFromRow(row), rawToken, nil
}

// GetResetTokenByToken looks up a reset token by raw token. Returns only
// unexpired, unused tokens.
func (s *SessionStore) GetResetTokenByToken(ctx context.Context, tx pgx.Tx, rawToken string) (*domain.ResetToken, error) {
	row, err := sqlcgen.New(tx).GetResetTokenByTokenHash(ctx, hashToken(rawToken))
	if err != nil {
		return nil, fmt.Errorf("get reset token: %w", err)
	}
	return resetTokenFromRow(row), nil
}

// MarkResetTokenUsed marks a reset token as used.
func (s *SessionStore) MarkResetTokenUsed(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).MarkResetTokenUsed(ctx, id); err != nil {
		return fmt.Errorf("mark reset token used: %w", err)
	}
	return nil
}

// --- Email Verifications ---

// CreateEmailVerification inserts an email verification token and returns the raw token.
func (s *SessionStore) CreateEmailVerification(
	ctx context.Context,
	tx pgx.Tx,
	customerID uuid.UUID,
	expiresAt time.Time,
) (*domain.EmailVerification, string, error) {
	rawToken := uuid.New().String()
	tokenHash := hashToken(rawToken)

	row, err := sqlcgen.New(tx).CreateEmailVerification(ctx, sqlcgen.CreateEmailVerificationParams{
		ID:         uuid.New(),
		CustomerID: customerID,
		TokenHash:  tokenHash,
		ExpiresAt:  expiresAt,
	})
	if err != nil {
		return nil, "", fmt.Errorf("insert email verification: %w", err)
	}

	return emailVerificationFromRow(row), rawToken, nil
}

// GetEmailVerificationByToken looks up an email verification by raw token.
func (s *SessionStore) GetEmailVerificationByToken(ctx context.Context, tx pgx.Tx, rawToken string) (*domain.EmailVerification, error) {
	row, err := sqlcgen.New(tx).GetEmailVerificationByTokenHash(ctx, hashToken(rawToken))
	if err != nil {
		return nil, fmt.Errorf("get email verification: %w", err)
	}
	return emailVerificationFromRow(row), nil
}

// MarkEmailVerified marks an email verification as verified.
func (s *SessionStore) MarkEmailVerified(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).MarkEmailVerified(ctx, id); err != nil {
		return fmt.Errorf("mark email verified: %w", err)
	}
	return nil
}

// --- Row converters ---

func sessionFromRow(r sqlcgen.Session) *domain.Session {
	return &domain.Session{
		ID:         r.ID,
		ActorType:  domain.SessionActorType(r.ActorType),
		ActorID:    r.ActorID,
		TokenHash:  r.TokenHash,
		IPAddress:  r.IpAddress,
		UserAgent:  r.UserAgent,
		LastSeenAt: r.LastSeenAt,
		ExpiresAt:  r.ExpiresAt,
		CreatedAt:  r.CreatedAt,
	}
}

func resetTokenFromRow(r sqlcgen.ResetToken) *domain.ResetToken {
	var usedAt *time.Time
	if r.UsedAt.Valid {
		usedAt = &r.UsedAt.Time
	}
	return &domain.ResetToken{
		ID:        r.ID,
		ActorType: domain.SessionActorType(r.ActorType),
		ActorID:   r.ActorID,
		TokenHash: r.TokenHash,
		ExpiresAt: r.ExpiresAt,
		UsedAt:    usedAt,
		CreatedAt: r.CreatedAt,
	}
}

func emailVerificationFromRow(r sqlcgen.EmailVerification) *domain.EmailVerification {
	var verifiedAt *time.Time
	if r.VerifiedAt.Valid {
		verifiedAt = &r.VerifiedAt.Time
	}
	return &domain.EmailVerification{
		ID:         r.ID,
		CustomerID: r.CustomerID,
		TokenHash:  r.TokenHash,
		ExpiresAt:  r.ExpiresAt,
		VerifiedAt: verifiedAt,
		CreatedAt:  r.CreatedAt,
	}
}
