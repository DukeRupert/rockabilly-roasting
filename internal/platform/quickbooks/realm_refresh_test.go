package quickbooks

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/testutil"
)

var refreshTestPool *pgxpool.Pool

func TestMain(m *testing.M) {
	pool, cleanup := testutil.SetupTestDB()
	refreshTestPool = pool
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// refreshStubStore reports an expiring token on the first read and a fresh one
// on the second — the "another worker already refreshed while we waited for the
// advisory lock" case. That path re-reads the row directly from the store,
// bypassing readCredentials, which is how the ciphertext realm escaped into
// request URLs the first time this encryption was written.
type refreshStubStore struct {
	expiring *domain.QBCredentials
	fresh    *domain.QBCredentials
	reads    int
}

func (s *refreshStubStore) GetByTenantID(context.Context, pgx.Tx, uuid.UUID) (*domain.QBCredentials, error) {
	s.reads++
	if s.reads == 1 {
		return s.expiring, nil
	}
	return s.fresh, nil
}
func (s *refreshStubStore) Upsert(context.Context, pgx.Tx, *domain.QBCredentials) error { return nil }
func (s *refreshStubStore) Delete(context.Context, pgx.Tx, uuid.UUID) error             { return nil }

// ValidToken must return the DECRYPTED realm on every path, not just the one
// where the token was still fresh.
//
// sendAPI interpolates whatever comes back into
// https://…/v3/company/<realm>/<path>. A ciphertext realm is base64, which
// contains "/" and "+", so the URL is both wrong and mangled — and QBO access
// tokens last an hour, so this would have broken invoice creation, sends,
// payment sync and the reconcile sweep once an hour, looking exactly like an
// Intuit outage.
func TestValidTokenReturnsPlaintextRealmAfterRefresh(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	const realm = "9130354674505161"

	c := &QBClient{
		config:   ClientConfig{EncryptionKey: key},
		tenantID: uuid.New(),
		pool:     refreshTestPool,
	}

	encRealm, err := c.Encrypt(realm)
	require.NoError(t, err)
	encToken, err := c.Encrypt("access-token")
	require.NoError(t, err)

	store := &refreshStubStore{
		// Inside the refresh buffer, so ValidToken goes for the lock.
		expiring: &domain.QBCredentials{
			RealmID: encRealm, AccessToken: encToken,
			AccessExpiresAt:  time.Now().Add(30 * time.Second),
			RefreshExpiresAt: time.Now().Add(90 * 24 * time.Hour),
		},
		// Another worker got there first: comfortably fresh, so the lock path
		// returns early without calling Intuit.
		fresh: &domain.QBCredentials{
			RealmID: encRealm, AccessToken: encToken,
			AccessExpiresAt:  time.Now().Add(50 * time.Minute),
			RefreshExpiresAt: time.Now().Add(90 * 24 * time.Hour),
		},
	}
	c.credStore = store

	token, gotRealm, err := c.ValidToken(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, store.reads, "must have gone through the lock's re-read")

	assert.Equal(t, "access-token", token)
	assert.Equal(t, realm, gotRealm, "the realm reaching the request URL must be plaintext")
	assert.NotContains(t, gotRealm, "/", "base64 ciphertext would mangle the request path")
}
