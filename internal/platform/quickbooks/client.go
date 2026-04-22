package quickbooks

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
)

const (
	sandboxBaseURL    = "https://sandbox-quickbooks.api.intuit.com"
	productionBaseURL = "https://quickbooks.api.intuit.com"

	tokenRefreshBuffer = 5 * time.Minute
)

// CredentialStore is the interface for persisting QB OAuth tokens.
type CredentialStore interface {
	GetByTenantID(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (*domain.QBCredentials, error)
	Upsert(ctx context.Context, tx pgx.Tx, creds *domain.QBCredentials) error
	Delete(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error
}

// ClientConfig holds the configuration for the QB client.
type ClientConfig struct {
	ClientID      string
	ClientSecret  string
	EncryptionKey []byte // AES-256, 32 bytes
	Environment   string // "sandbox" or "production"
	RedirectURI   string
}

// QBClient is the concrete implementation of the Client interface.
type QBClient struct {
	config     ClientConfig
	tenantID   uuid.UUID
	credStore  CredentialStore
	pool       *pgxpool.Pool
	httpClient *http.Client
	baseURL    string
}

// NewQBClient creates a new QuickBooks client.
func NewQBClient(config ClientConfig, tenantID uuid.UUID, credStore CredentialStore, pool *pgxpool.Pool) *QBClient {
	baseURL := productionBaseURL
	if config.Environment == "sandbox" {
		baseURL = sandboxBaseURL
	}
	return &QBClient{
		config:   config,
		tenantID: tenantID,
		credStore: credStore,
		pool:     pool,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: baseURL,
	}
}

// ValidToken returns a valid access token, refreshing if needed.
// Acquires a DB-level advisory lock to prevent concurrent refresh races.
func (c *QBClient) ValidToken(ctx context.Context) (string, string, error) {
	var creds *domain.QBCredentials
	var err error

	// Read current credentials
	err = func() error {
		tx, txErr := c.pool.Begin(ctx)
		if txErr != nil {
			return txErr
		}
		defer tx.Rollback(ctx) //nolint:errcheck

		creds, txErr = c.credStore.GetByTenantID(ctx, tx, c.tenantID)
		if txErr != nil {
			return fmt.Errorf("get QB credentials: %w", txErr)
		}
		return tx.Commit(ctx)
	}()
	if err != nil {
		return "", "", err
	}

	// If token is still fresh, decrypt and return
	if time.Until(creds.AccessExpiresAt) >= tokenRefreshBuffer {
		token, decErr := c.decrypt(creds.AccessToken)
		if decErr != nil {
			return "", "", fmt.Errorf("decrypt access token: %w", decErr)
		}
		return token, creds.RealmID, nil
	}

	// Need to refresh — use advisory lock to prevent races
	creds, err = c.refreshTokenWithLock(ctx, creds)
	if err != nil {
		return "", "", err
	}

	token, err := c.decrypt(creds.AccessToken)
	if err != nil {
		return "", "", fmt.Errorf("decrypt access token: %w", err)
	}
	return token, creds.RealmID, nil
}

// refreshTokenWithLock refreshes the access token while holding an advisory lock.
func (c *QBClient) refreshTokenWithLock(ctx context.Context, creds *domain.QBCredentials) (*domain.QBCredentials, error) {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin refresh tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Advisory lock keyed on tenant ID to prevent concurrent refreshes
	lockKey := int64(creds.TenantID.ID())
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey); err != nil {
		return nil, fmt.Errorf("advisory lock: %w", err)
	}

	// Re-fetch after acquiring lock — another worker may have already refreshed
	creds, err = c.credStore.GetByTenantID(ctx, tx, c.tenantID)
	if err != nil {
		return nil, err
	}

	if time.Until(creds.AccessExpiresAt) >= tokenRefreshBuffer {
		// Already refreshed by another worker
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return creds, nil
	}

	// Check refresh token hasn't expired
	if time.Now().After(creds.RefreshExpiresAt) {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, ErrTokenExpired
	}

	// Decrypt refresh token
	refreshToken, err := c.decrypt(creds.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("decrypt refresh token: %w", err)
	}

	// Call QBO token endpoint
	tokenResp, err := exchangeRefreshToken(ctx, c.httpClient, c.config.ClientID, c.config.ClientSecret, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("qb token refresh: %w", err)
	}

	// Encrypt new tokens
	encAccess, err := c.Encrypt(tokenResp.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("encrypt access token: %w", err)
	}
	encRefresh, err := c.Encrypt(tokenResp.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("encrypt refresh token: %w", err)
	}

	now := time.Now()
	creds.AccessToken = encAccess
	creds.RefreshToken = encRefresh
	creds.AccessExpiresAt = now.Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	creds.RefreshExpiresAt = now.Add(time.Duration(tokenResp.RefreshExpiresIn) * time.Second)
	creds.UpdatedAt = now

	if err := c.credStore.Upsert(ctx, tx, creds); err != nil {
		return nil, fmt.Errorf("save refreshed credentials: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit refresh: %w", err)
	}

	slog.Info("qb: token refreshed", "tenant_id", c.tenantID)
	return creds, nil
}

// doAPI makes an authenticated API request to QBO.
func (c *QBClient) doAPI(ctx context.Context, method, path string, body any) ([]byte, error) {
	token, realmID, err := c.ValidToken(ctx)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/v3/company/%s%s", c.baseURL, realmID, path)

	var reqBody io.Reader
	if body != nil {
		b, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal request: %w", marshalErr)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qb api call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, classifyError(resp.StatusCode, respBody)
	}

	return respBody, nil
}

// classifyError converts an HTTP error response into the appropriate error type.
func classifyError(statusCode int, body []byte) error {
	msg := string(body)
	apiErr := &APIError{
		StatusCode: statusCode,
		Message:    http.StatusText(statusCode),
		Detail:     msg,
	}
	switch statusCode {
	case 400:
		return fmt.Errorf("%w: %s", ErrBadRequest, apiErr.Error())
	case 401:
		return fmt.Errorf("%w: %s", ErrTokenExpired, apiErr.Error())
	case 429:
		return fmt.Errorf("%w: %s", ErrRateLimited, apiErr.Error())
	default:
		return apiErr
	}
}

// Encrypt encrypts plaintext using AES-256-GCM.
func (c *QBClient) Encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(c.config.EncryptionKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// decrypt decrypts a base64-encoded AES-256-GCM ciphertext.
func (c *QBClient) decrypt(encoded string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(c.config.EncryptionKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
