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

// statusTestKey is a real AES-256 key, because Status now decrypts the realm
// it reports and a fixture holding plaintext would be testing a shape the
// writer never produces.
var statusTestKey = []byte("0123456789abcdef0123456789abcdef")

func statusManager(store CredentialStore) *OAuthManager {
	return NewOAuthManager(ClientConfig{EncryptionKey: statusTestKey}, nil, store, uuid.New(), []byte("k"), nil, false)
}

// encryptedRealm is how a realm reaches the database: encrypted with the same
// key Status reads it back with.
func encryptedRealm(t *testing.T, realm string) string {
	t.Helper()
	out, err := encryptWithKey(statusTestKey, realm)
	require.NoError(t, err)
	return out
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
		RealmID:          encryptedRealm(t, "9130354674505161"),
		RefreshExpiresAt: expires,
	}}).Status(ctx, nil)
	require.NoError(t, err)
	assert.True(t, connected.Connected)
	assert.Equal(t, "9130354674505161", connected.RealmID)
	require.NotNil(t, connected.RefreshExpiresAt)
	assert.Equal(t, expires, *connected.RefreshExpiresAt)
}

// A realm this server cannot decrypt means the encryption key changed. That is
// a different question from whether a connection exists, and the settings page
// omits the field when it is empty — so the connection is still reported, the
// realm is not invented, and the token paths raise the key problem where it
// actually blocks work.
func TestStatus_UnreadableRealmStillReportsConnected(t *testing.T) {
	ctx := context.Background()

	other, err := encryptWithKey([]byte("ffffffffffffffffffffffffffffffff"), "9130354674505161")
	require.NoError(t, err)

	status, err := statusManager(stubCredStore{creds: &domain.QBCredentials{
		RealmID:          other,
		RefreshExpiresAt: time.Now().Add(24 * time.Hour),
	}}).Status(ctx, nil)
	require.NoError(t, err)
	assert.True(t, status.Connected, "a connection exists whether or not this server can read its realm")
	assert.Empty(t, status.RealmID, "never show a realm that could not be decrypted")
}
