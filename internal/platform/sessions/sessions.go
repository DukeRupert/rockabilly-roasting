package sessions

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
)

// Session lifetime constants.
const (
	CustomerRememberMeDuration = 30 * 24 * time.Hour // 30 days
	CustomerSessionDuration    = 24 * time.Hour       // 24 hours
	GuestSessionDuration       = 72 * time.Hour       // 72 hours
	StaffSessionDuration       = 8 * time.Hour        // 8 hours
)

// Manager manages user sessions.
type Manager struct {
	store Store
}

// Store defines the interface for session persistence.
type Store interface {
	Create(ctx context.Context, tx pgx.Tx, actorType domain.SessionActorType, actorID uuid.UUID, expiresAt time.Time, ipAddress *string, userAgent *string) (*domain.Session, string, error)
	GetByToken(ctx context.Context, tx pgx.Tx, rawToken string) (*domain.Session, error)
	Revoke(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) error
	RevokeAllForActor(ctx context.Context, tx pgx.Tx, actorType domain.SessionActorType, actorID uuid.UUID) error
	UpdateLastSeen(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) error
	PruneExpired(ctx context.Context, tx pgx.Tx) (int64, error)
}

// NewManager creates a new session manager.
func NewManager(store Store) *Manager {
	return &Manager{store: store}
}

// GetStore returns the underlying store for direct access.
func (m *Manager) GetStore() Store {
	return m.store
}
