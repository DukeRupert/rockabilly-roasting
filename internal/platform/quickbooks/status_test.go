package quickbooks

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// stubCredStore returns whatever it is told to, so Status can be exercised
// without a database.
type stubCredStore struct {
	creds *domain.QBCredentials
	err   error
}

func (s stubCredStore) GetByTenantID(context.Context, pgx.Tx, uuid.UUID) (*domain.QBCredentials, error) {
	return s.creds, s.err
}
func (s stubCredStore) Upsert(context.Context, pgx.Tx, *domain.QBCredentials) error { return nil }
func (s stubCredStore) Delete(context.Context, pgx.Tx, uuid.UUID) error             { return nil }

func statusManager(store CredentialStore) *OAuthManager {
	return NewOAuthManager(ClientConfig{}, nil, store, uuid.New(), []byte("k"), nil, false)
}

// Status used to swallow every store error into Connected=false, which made "the
// database is unreachable" and "nobody has connected QuickBooks" the same
// answer — and the settings page then told staff to reconnect a connection that
// was fine. Only the no-credentials case may be reported as not-connected.
func TestStatus_DistinguishesNoCredentialsFromAFailedRead(t *testing.T) {
	ctx := context.Background()

	// No row: a fact, not a failure. The store wraps, so the sentinel arrives
	// wrapped — which is exactly how it reaches Status in production.
	missing, err := statusManager(stubCredStore{err: fmt.Errorf("get qb credentials: %w", pgx.ErrNoRows)}).Status(ctx, nil)
	require.NoError(t, err)
	assert.False(t, missing.Connected)

	// Anything else is a failed read and must say so.
	readFailed := errors.New("conn busy")
	_, err = statusManager(stubCredStore{err: fmt.Errorf("get qb credentials: %w", readFailed)}).Status(ctx, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, readFailed)

	// Credentials present: connected, with the realm and expiry carried through.
	expires := time.Now().Add(90 * 24 * time.Hour)
	connected, err := statusManager(stubCredStore{creds: &domain.QBCredentials{
		RealmID:          "9130354674505161",
		RefreshExpiresAt: expires,
	}}).Status(ctx, nil)
	require.NoError(t, err)
	assert.True(t, connected.Connected)
	assert.Equal(t, "9130354674505161", connected.RealmID)
	require.NotNil(t, connected.RefreshExpiresAt)
	assert.Equal(t, expires, *connected.RefreshExpiresAt)
}
