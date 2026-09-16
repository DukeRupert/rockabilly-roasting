package quickbooks

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
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

// stubTokenEndpoint intercepts the call to Intuit so the genuine refresh path
// can be exercised without a network. Injected through the client's own
// httpClient, which is the only thing exchangeRefreshToken takes.
type stubTokenEndpoint struct{ calls int }

func (s *stubTokenEndpoint) RoundTrip(req *http.Request) (*http.Response, error) {
	s.calls++
	body := `{"access_token":"fresh-access","refresh_token":"fresh-refresh","expires_in":3600,"x_refresh_token_expires_in":8726400}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Request:    req,
	}, nil
}

// The line the original Critical actually lived on: the return after a real
// token refresh. The first test covers the sibling branch — "another worker
// already refreshed" — and reverting this one left the suite green, which the
// previous commit wrongly claimed was pinned.
//
// This is also the branch production takes every hour once the token genuinely
// expires; the concurrent-worker case is the rarer one.
func TestValidTokenReturnsPlaintextRealmAfterAnActualRefresh(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	const realm = "9130354674505161"

	transport := &stubTokenEndpoint{}
	c := &QBClient{
		config:     ClientConfig{EncryptionKey: key, ClientID: "id", ClientSecret: "secret"},
		tenantID:   uuid.New(),
		pool:       refreshTestPool,
		httpClient: &http.Client{Transport: transport},
	}

	encRealm, err := c.Encrypt(realm)
	require.NoError(t, err)
	encToken, err := c.Encrypt("stale-access")
	require.NoError(t, err)

	expiring := &domain.QBCredentials{
		RealmID: encRealm, AccessToken: encToken, RefreshToken: encToken,
		AccessExpiresAt:  time.Now().Add(10 * time.Second),
		RefreshExpiresAt: time.Now().Add(90 * 24 * time.Hour),
	}
	// Still expiring on the re-read, so the lock path does NOT take the early
	// return and goes on to refresh for real.
	store := &refreshStubStore{expiring: expiring, fresh: expiring}
	c.credStore = store

	token, gotRealm, err := c.ValidToken(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, transport.calls, "must have actually refreshed, not taken the early return")

	assert.Equal(t, "fresh-access", token)
	assert.Equal(t, realm, gotRealm, "the realm must be plaintext on the path production takes hourly")
}

// The third caller-facing path: the token was still fresh, so ValidToken
// returns straight from readCredentials without touching the lock.
func TestValidTokenReturnsPlaintextRealmWhenTokenIsFresh(t *testing.T) {
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

	fresh := &domain.QBCredentials{
		RealmID: encRealm, AccessToken: encToken,
		AccessExpiresAt:  time.Now().Add(50 * time.Minute),
		RefreshExpiresAt: time.Now().Add(90 * 24 * time.Hour),
	}
	store := &refreshStubStore{expiring: fresh, fresh: fresh}
	c.credStore = store

	token, gotRealm, err := c.ValidToken(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, store.reads, "a fresh token must not reach the lock at all")

	assert.Equal(t, "access-token", token)
	assert.Equal(t, realm, gotRealm)
}
