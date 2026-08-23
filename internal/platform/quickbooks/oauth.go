package quickbooks

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
)

const (
	oauthStateCookieName   = "qb_oauth_state"
	oauthStateCookiePath   = "/admin/settings/integrations/quickbooks"
	oauthStateCookieMaxAge = 600 // 10 minutes
)

// ErrInvalidState is returned when the OAuth callback's state parameter does
// not match the signed cookie (CSRF check failed).
var ErrInvalidState = errors.New("qb oauth: invalid state")

// ErrMissingCallbackParams is returned when the OAuth callback is missing
// required query parameters (code, realmId).
var ErrMissingCallbackParams = errors.New("qb oauth: missing code or realmId")

// OAuthManager orchestrates the QuickBooks OAuth2 authorization flow:
// signing state cookies, exchanging codes for tokens, encrypting tokens, and
// persisting credentials. It is constructed once per process for a given
// tenant; the web layer calls it from the admin settings handler.
type OAuthManager struct {
	config     ClientConfig
	encrypter  *QBClient
	credStore  CredentialStore
	tenantID   uuid.UUID
	hmacKey    []byte
	httpClient *http.Client
	secure     bool
}

// NewOAuthManager creates a new OAuth manager bound to the given tenant.
// encrypter is the concrete QBClient — its AES key is used to encrypt tokens
// before they are persisted. hmacKey signs the state cookie. secure controls
// the cookie's Secure flag; pass false only for local development.
func NewOAuthManager(
	config ClientConfig,
	encrypter *QBClient,
	credStore CredentialStore,
	tenantID uuid.UUID,
	hmacKey []byte,
	httpClient *http.Client,
	secure bool,
) *OAuthManager {
	return &OAuthManager{
		config:     config,
		encrypter:  encrypter,
		credStore:  credStore,
		tenantID:   tenantID,
		hmacKey:    hmacKey,
		httpClient: httpClient,
		secure:     secure,
	}
}

// TenantID returns the tenant this manager is bound to.
func (m *OAuthManager) TenantID() uuid.UUID { return m.tenantID }

// StartAuth generates a new state, sets a signed cookie for CSRF protection,
// and returns the authorization URL the user should be redirected to.
func (m *OAuthManager) StartAuth(w http.ResponseWriter) (string, error) {
	state, err := generateOAuthState()
	if err != nil {
		return "", fmt.Errorf("generate oauth state: %w", err)
	}
	setOAuthStateCookie(w, state, m.hmacKey, m.secure)
	return AuthorizationURL(m.config.ClientID, m.config.RedirectURI, state), nil
}

// ExchangeCallback validates the callback's state cookie, exchanges the
// authorization code for tokens, encrypts them, and returns credentials ready
// for persistence. It performs the external HTTP call to Intuit and must
// therefore run outside any database transaction.
//
// The caller is responsible for persisting the returned credentials by calling
// SaveCredentials inside a tx and recording an audit entry.
func (m *OAuthManager) ExchangeCallback(ctx context.Context, r *http.Request) (*domain.QBCredentials, error) {
	state := r.URL.Query().Get("state")
	if !validateOAuthStateCookie(r, state, m.hmacKey) {
		return nil, ErrInvalidState
	}

	code := r.URL.Query().Get("code")
	realmID := r.URL.Query().Get("realmId")
	if code == "" || realmID == "" {
		return nil, ErrMissingCallbackParams
	}

	tokenResp, err := ExchangeCode(ctx, m.httpClient, m.config.ClientID, m.config.ClientSecret, code, m.config.RedirectURI)
	if err != nil {
		return nil, fmt.Errorf("qb oauth: token exchange: %w", err)
	}

	encAccess, err := m.encrypter.Encrypt(tokenResp.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("qb oauth: encrypt access token: %w", err)
	}
	encRefresh, err := m.encrypter.Encrypt(tokenResp.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("qb oauth: encrypt refresh token: %w", err)
	}

	now := time.Now()
	return &domain.QBCredentials{
		TenantID:         m.tenantID,
		RealmID:          realmID,
		AccessToken:      encAccess,
		RefreshToken:     encRefresh,
		AccessExpiresAt:  now.Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
		RefreshExpiresAt: now.Add(time.Duration(tokenResp.RefreshExpiresIn) * time.Second),
	}, nil
}

// SaveCredentials upserts credentials produced by ExchangeCallback. Must run
// inside a transaction so the caller can record an audit entry atomically.
func (m *OAuthManager) SaveCredentials(ctx context.Context, tx pgx.Tx, creds *domain.QBCredentials) error {
	if err := m.credStore.Upsert(ctx, tx, creds); err != nil {
		return fmt.Errorf("qb oauth: save credentials: %w", err)
	}
	return nil
}

// RefreshTokenForRevoke reads and decrypts the stored refresh token so the
// caller can revoke it with Intuit before deleting the local credential. It
// uses the provided (read-only) tx. Returns "" with no error when no
// credential is stored — there is nothing to revoke.
func (m *OAuthManager) RefreshTokenForRevoke(ctx context.Context, tx pgx.Tx) (string, error) {
	creds, err := m.credStore.GetByTenantID(ctx, tx, m.tenantID)
	if err != nil {
		// Not connected (or no rows) — nothing to revoke. Caller proceeds to
		// the local delete, which is a no-op.
		return "", nil //nolint:nilerr
	}
	token, err := m.encrypter.Decrypt(creds.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("qb oauth: decrypt refresh token: %w", err)
	}
	return token, nil
}

// Revoke revokes the given refresh token with Intuit, terminating the grant
// server-side. It performs an external HTTP call and must run OUTSIDE any
// database transaction. Callers should treat this as best-effort: a revoke
// failure must not block deleting the local credential.
func (m *OAuthManager) Revoke(ctx context.Context, refreshToken string) error {
	return RevokeToken(ctx, m.httpClient, m.config.ClientID, m.config.ClientSecret, refreshToken)
}

// Disconnect removes the stored credentials for this tenant. This is the local
// half of disconnection only — call Revoke first (outside the tx) to terminate
// the grant on Intuit's side.
func (m *OAuthManager) Disconnect(ctx context.Context, tx pgx.Tx) error {
	if err := m.credStore.Delete(ctx, tx, m.tenantID); err != nil {
		return fmt.Errorf("qb oauth: disconnect: %w", err)
	}
	return nil
}

// ConnectionStatus describes whether QuickBooks is connected for this tenant.
type ConnectionStatus struct {
	Connected        bool
	RealmID          string
	RefreshExpiresAt *time.Time
}

// Status returns the current connection status, reading from the credential
// store. No stored credentials means Connected=false, which is a fact rather
// than an error.
//
// Anything else is returned. It used to be swallowed into the same
// Connected=false, which made "the database is unreachable" and "nobody has
// connected QuickBooks" the same answer — and the settings page then told staff
// to go and reconnect a connection that was fine.
func (m *OAuthManager) Status(ctx context.Context, tx pgx.Tx) (ConnectionStatus, error) {
	creds, err := m.credStore.GetByTenantID(ctx, tx, m.tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ConnectionStatus{}, nil
	}
	if err != nil {
		return ConnectionStatus{}, fmt.Errorf("qb status: %w", err)
	}
	return ConnectionStatus{
		Connected:        true,
		RealmID:          creds.RealmID,
		RefreshExpiresAt: &creds.RefreshExpiresAt,
	}, nil
}

// --- state cookie helpers (unexported, package-internal) ---

func generateOAuthState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func setOAuthStateCookie(w http.ResponseWriter, state string, hmacKey []byte, secure bool) {
	sig := signOAuthState(state, hmacKey)
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    state + "." + sig,
		Path:     oauthStateCookiePath,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   oauthStateCookieMaxAge,
	})
}

func validateOAuthStateCookie(r *http.Request, state string, hmacKey []byte) bool {
	cookie, err := r.Cookie(oauthStateCookieName)
	if err != nil {
		return false
	}
	expected := state + "." + signOAuthState(state, hmacKey)
	return hmac.Equal([]byte(cookie.Value), []byte(expected))
}

func signOAuthState(state string, key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(state))
	return hex.EncodeToString(mac.Sum(nil))
}
