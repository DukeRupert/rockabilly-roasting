package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/platform/quickbooks"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// handleAdminSettings renders the Settings page with integration status.
func (d *Deps) handleAdminSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	qbStatus := admin.QBConnectionStatus{}
	qbEnabled := d.QBClient != nil

	if qbEnabled {
		// Check if QB is connected by looking for credentials
		_ = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			creds, err := d.QBCredentialStore.GetByTenantID(ctx, tx, tenantID())
			if err != nil {
				return nil // not connected
			}
			qbStatus.Connected = true
			qbStatus.RealmID = creds.RealmID
			qbStatus.RefreshExpiresAt = &creds.RefreshExpiresAt
			return nil
		})
	}

	name, role := staffNameRole(r)
	props := admin.SettingsProps{
		QB:        qbStatus,
		QBEnabled: qbEnabled,
		Flash:     r.URL.Query().Get("flash"),
		StaffName: name,
		StaffRole: role,
	}

	if IsHTMX(r) {
		admin.SettingsContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.Settings(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminQBConnect initiates the OAuth2 flow to connect QuickBooks.
func (d *Deps) handleAdminQBConnect(w http.ResponseWriter, r *http.Request) {
	if d.QBClient == nil {
		http.Error(w, "QuickBooks not configured", http.StatusBadRequest)
		return
	}

	qbCfg := d.QBConfig
	state, err := generateOAuthState()
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Store state in a signed cookie (CSRF protection)
	setOAuthStateCookie(w, state, d.QBOAuthHMACKey)

	authURL := quickbooks.AuthorizationURL(qbCfg.ClientID, qbCfg.RedirectURI, state)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleAdminQBCallback handles the OAuth2 callback from QuickBooks.
func (d *Deps) handleAdminQBCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if d.QBClient == nil {
		http.Error(w, "QuickBooks not configured", http.StatusBadRequest)
		return
	}

	// Validate state (CSRF)
	state := r.URL.Query().Get("state")
	if !validateOAuthStateCookie(r, state, d.QBOAuthHMACKey) {
		slog.Error("qb oauth: invalid state parameter")
		http.Redirect(w, r, "/admin/settings?flash=QuickBooks+connection+failed+(invalid+state)", http.StatusSeeOther)
		return
	}

	code := r.URL.Query().Get("code")
	realmID := r.URL.Query().Get("realmId")

	if code == "" || realmID == "" {
		errorDesc := r.URL.Query().Get("error")
		slog.Error("qb oauth: missing code or realmId", "error", errorDesc)
		http.Redirect(w, r, "/admin/settings?flash=QuickBooks+connection+failed", http.StatusSeeOther)
		return
	}

	qbCfg := d.QBConfig

	// Exchange code for tokens
	tokenResp, err := quickbooks.ExchangeCode(ctx, d.QBHTTPClient, qbCfg.ClientID, qbCfg.ClientSecret, code, qbCfg.RedirectURI)
	if err != nil {
		slog.Error("qb oauth: token exchange failed", "error", err)
		http.Redirect(w, r, "/admin/settings?flash=QuickBooks+connection+failed+(token+exchange)", http.StatusSeeOther)
		return
	}

	// Encrypt tokens
	qbClient := d.QBClient.(*quickbooks.QBClient)
	encAccess, err := qbClient.Encrypt(tokenResp.AccessToken)
	if err != nil {
		slog.Error("qb oauth: encrypt access token", "error", err)
		http.Redirect(w, r, "/admin/settings?flash=QuickBooks+connection+failed", http.StatusSeeOther)
		return
	}
	encRefresh, err := qbClient.Encrypt(tokenResp.RefreshToken)
	if err != nil {
		slog.Error("qb oauth: encrypt refresh token", "error", err)
		http.Redirect(w, r, "/admin/settings?flash=QuickBooks+connection+failed", http.StatusSeeOther)
		return
	}

	now := time.Now()
	creds := &quickbooks.Credentials{
		TenantID:         tenantID(),
		RealmID:          realmID,
		AccessToken:      encAccess,
		RefreshToken:     encRefresh,
		AccessExpiresAt:  now.Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
		RefreshExpiresAt: now.Add(time.Duration(tokenResp.RefreshExpiresIn) * time.Second),
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.QBCredentialStore.Upsert(ctx, tx, creds)
	})
	if err != nil {
		slog.Error("qb oauth: save credentials", "error", err)
		http.Redirect(w, r, "/admin/settings?flash=QuickBooks+connection+failed+(database+error)", http.StatusSeeOther)
		return
	}

	slog.Info("qb: connected", "realm_id", realmID)
	http.Redirect(w, r, "/admin/settings?flash=QuickBooks+connected+successfully", http.StatusSeeOther)
}

// handleAdminQBDisconnect removes the QuickBooks connection.
func (d *Deps) handleAdminQBDisconnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.QBCredentialStore.Delete(ctx, tx, tenantID())
	})
	if err != nil {
		slog.Error("qb: disconnect failed", "error", err)
		http.Redirect(w, r, "/admin/settings?flash=Failed+to+disconnect+QuickBooks", http.StatusSeeOther)
		return
	}

	slog.Info("qb: disconnected")
	http.Redirect(w, r, "/admin/settings?flash=QuickBooks+disconnected", http.StatusSeeOther)
}

// --- OAuth state helpers ---

const oauthStateCookieName = "qb_oauth_state"

// generateOAuthState creates a random state parameter for CSRF protection.
func generateOAuthState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate oauth state: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// setOAuthStateCookie sets a signed cookie with the OAuth state.
func setOAuthStateCookie(w http.ResponseWriter, state string, hmacKey []byte) {
	sig := signState(state, hmacKey)
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    state + "." + sig,
		Path:     "/admin/settings/integrations/quickbooks",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600, // 10 minutes
	})
}

// validateOAuthStateCookie validates the state parameter against the signed cookie.
func validateOAuthStateCookie(r *http.Request, state string, hmacKey []byte) bool {
	cookie, err := r.Cookie(oauthStateCookieName)
	if err != nil {
		return false
	}

	// Cookie value is "state.signature"
	expected := state + "." + signState(state, hmacKey)
	return hmac.Equal([]byte(cookie.Value), []byte(expected))
}

func signState(state string, key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(state))
	return hex.EncodeToString(mac.Sum(nil))
}

// tenantID returns the tenant ID for the current deployment.
// In a multi-tenant setup this would come from context/subdomain.
func tenantID() uuid.UUID {
	return uuid.MustParse("00000000-0000-0000-0000-000000000001")
}
