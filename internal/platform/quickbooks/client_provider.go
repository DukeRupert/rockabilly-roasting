package quickbooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
)

// AppConfig is which Intuit app this deployment connects QuickBooks through,
// in plaintext. domain.QBAppConfig is the same thing on its way to and from
// the database, where the two secrets are ciphertext.
type AppConfig struct {
	ClientID        string
	ClientSecret    string
	WebhookVerifier string
	Environment     string

	// FromDatabase distinguishes a configuration a staffer saved from one the
	// environment supplied. The settings page says which is in force, because
	// "the form is empty but QuickBooks works" is otherwise inexplicable, and
	// because a saved row silently overriding a variable somebody just edited
	// on the box is exactly the confusion this field exists to prevent.
	FromDatabase bool
}

// Complete reports whether the configuration has everything needed to talk to
// Intuit. A partial configuration is treated as no configuration: half an app
// cannot connect, and pretending otherwise moves the failure to a worse place.
func (c AppConfig) Complete() bool {
	return c.ClientID != "" && c.ClientSecret != "" && c.WebhookVerifier != ""
}

// Sandbox reports whether this configuration points at Intuit's sandbox.
//
// The comparison is against the sandbox value, not the production one, so
// anything unrecognised means production. That is the same way the environment
// variable behaved and it is deliberate: a typo must never quietly aim a shop
// at a sandbox company that does not hold its books.
func (c AppConfig) Sandbox() bool {
	return c.Environment == domain.QBEnvironmentSandbox
}

// fingerprint identifies a configuration for cache invalidation. It hashes
// rather than stores the secrets so a cached fingerprint is not a second copy
// of the client secret sitting in memory.
func (c AppConfig) fingerprint() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		c.ClientID, c.ClientSecret, c.WebhookVerifier, c.Environment,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// AppConfigStore is the persistence interface for the app configuration.
type AppConfigStore interface {
	GetByTenantID(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (*domain.QBAppConfig, error)
	Upsert(ctx context.Context, tx pgx.Tx, cfg *domain.QBAppConfig) error
	Delete(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error
}

// AppConfigInput is a submitted app configuration on its way in from the admin
// form.
type AppConfigInput struct {
	ClientID string
	// ClientSecret and WebhookVerifier are blank to mean "leave what is
	// stored". The form never renders a stored secret back — a page that
	// prints a client secret into HTML has put it in browser caches, proxy
	// logs and over the staffer's shoulder — so blank has to mean unchanged,
	// or every edit to the client ID would demand retyping both secrets.
	ClientSecret    string
	WebhookVerifier string
	Environment     string
}

// Form field names for AppConfigInput, so the provider and the form that feeds
// it cannot drift apart on what a field is called. AppConfigError reports
// failures against these, and the admin marks the matching input.
const (
	FieldClientID        = "qb_client_id"
	FieldClientSecret    = "qb_client_secret"
	FieldWebhookVerifier = "qb_webhook_verifier"
	FieldEnvironment     = "qb_environment"
)

// AppConfigError reports which submitted fields are wrong and what to say
// about each.
//
// A map rather than the first problem found: the realistic failure is a first
// save with both secret fields left blank, and reporting one, then the other,
// is two round trips through a form whose secrets cannot be re-rendered — so
// the staffer would retype the good one each time. Same reasoning as
// parseShippingForm in the web layer.
type AppConfigError struct {
	// Fields maps a form field name to the sentence shown beneath it.
	Fields map[string]string
}

func (e *AppConfigError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for _, field := range []string{FieldClientID, FieldClientSecret, FieldWebhookVerifier, FieldEnvironment} {
		if msg, ok := e.Fields[field]; ok {
			parts = append(parts, field+": "+msg)
		}
	}
	return "quickbooks: invalid app configuration (" + strings.Join(parts, "; ") + ")"
}

// Is makes errors.Is(err, ErrInvalidAppConfig) hold for every AppConfigError,
// so a caller that only needs to know "the form was wrong" does not have to
// know this type exists.
func (e *AppConfigError) Is(target error) bool { return target == ErrInvalidAppConfig }

// ProviderConfig is the wiring a Provider needs.
type ProviderConfig struct {
	Pool        *pgxpool.Pool
	AppConfigs  AppConfigStore
	Credentials CredentialStore
	TenantID    uuid.UUID

	// EncryptionKey is AES-256 (32 bytes). It protects the OAuth tokens and
	// the two secrets in the app configuration alike.
	EncryptionKey []byte

	// RedirectURI is derived from the deployment's BASE_URL — see
	// DefaultRedirectURI. It is deployment identity rather than app identity,
	// which is why it is not part of AppConfig.
	RedirectURI string

	// EnvFallback is the configuration the environment variables supply, used
	// when no row is stored. It keeps deployments that predate the settings
	// form working across the migration that introduced it.
	EnvFallback AppConfig

	// SalesItemID / ShippingItemID are the environment's invoice-item
	// fallbacks (see db/migrations/079). The admin's stored choice wins over
	// these; they are passed through to every client this provider builds.
	SalesItemID    string
	ShippingItemID string

	HTTPClient    *http.Client
	SecureCookies bool
}

// Provider resolves which Intuit app is in force and hands back a client
// talking to it. It implements Client, so callers that only want to make API
// calls — every River worker, most handlers — take a Client as they always did
// and never learn that the configuration can change underneath them.
//
// It exists because QuickBooks configuration stopped being a boot-time fact.
// Before, main.go read QB_CLIENT_ID and either built a client or left a nil
// that switched the whole module off, which made connecting QuickBooks a
// deploy and made "is QuickBooks on" a question answered once, at startup, by
// the environment. Now the answer lives in the database, can change while the
// server runs, and is asked at the moment it matters.
//
// Every call resolves the configuration afresh from a singleton-row read. That
// is a fraction of a millisecond against an Intuit round trip measured in
// hundreds, and it is what lets a credential saved in the admin take effect on
// the next job rather than the next restart. The built client is cached behind
// the configuration's fingerprint so the resolution does not throw away the
// term cache on every call.
type Provider struct {
	cfg ProviderConfig

	mu          sync.Mutex
	fingerprint string
	client      *QBClient
	oauth       *OAuthManager
}

// NewProvider creates a Provider. It never fails and never talks to the
// database: a Provider is always constructible, including on a deployment with
// no QuickBooks configuration at all, which is what lets main.go wire the
// workers and routes unconditionally.
func NewProvider(cfg ProviderConfig) *Provider {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &Provider{cfg: cfg}
}

// TenantID returns the tenant this provider is bound to.
func (p *Provider) TenantID() uuid.UUID { return p.cfg.TenantID }

// RedirectURI returns the OAuth callback this deployment will send Intuit to.
// The settings page shows it, because it has to be registered on Intuit's side
// character for character and it is derived rather than typed.
func (p *Provider) RedirectURI() string { return p.cfg.RedirectURI }

// AppConfig returns the configuration in force: the stored row when there is
// one, otherwise the environment's. The second return is false when neither
// supplies a complete configuration.
func (p *Provider) AppConfig(ctx context.Context) (AppConfig, bool, error) {
	tx, err := p.cfg.Pool.Begin(ctx)
	if err != nil {
		return AppConfig{}, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	cfg, ok, err := p.appConfigTx(ctx, tx)
	if err != nil {
		return AppConfig{}, false, err
	}
	return cfg, ok, tx.Commit(ctx)
}

// appConfigTx is AppConfig inside a caller's transaction.
func (p *Provider) appConfigTx(ctx context.Context, tx pgx.Tx) (AppConfig, bool, error) {
	stored, err := p.cfg.AppConfigs.GetByTenantID(ctx, tx, p.cfg.TenantID)
	if err != nil {
		return AppConfig{}, false, err
	}
	if stored != nil {
		cfg, decErr := p.decryptStored(stored)
		if decErr != nil {
			// Deliberately not a fall-through to the environment. A row that
			// will not decrypt means the encryption key changed, and the
			// environment may well describe a different Intuit app — silently
			// connecting to that one instead is how a shop ends up writing
			// into the wrong company's books.
			return AppConfig{}, false, decErr
		}
		return cfg, cfg.Complete(), nil
	}
	env := p.cfg.EnvFallback
	return env, env.Complete(), nil
}

func (p *Provider) decryptStored(stored *domain.QBAppConfig) (AppConfig, error) {
	if len(p.cfg.EncryptionKey) == 0 {
		// Named rather than left to fail inside AES as "invalid key size 0".
		// The fix is a variable on the box, and the message has to say which.
		return AppConfig{}, fmt.Errorf("%w: set QB_TOKEN_ENCRYPTION_KEY or APP_SECRET", ErrAppConfigUnreadable)
	}
	secret, err := decryptWithKey(p.cfg.EncryptionKey, stored.ClientSecret)
	if err != nil {
		return AppConfig{}, fmt.Errorf("%w: client secret: %v", ErrAppConfigUnreadable, err)
	}
	verifier, err := decryptWithKey(p.cfg.EncryptionKey, stored.WebhookVerifier)
	if err != nil {
		return AppConfig{}, fmt.Errorf("%w: webhook verifier: %v", ErrAppConfigUnreadable, err)
	}
	return AppConfig{
		ClientID:        stored.ClientID,
		ClientSecret:    secret,
		WebhookVerifier: verifier,
		Environment:     stored.Environment,
		FromDatabase:    true,
	}, nil
}

// Configured reports whether a complete configuration is in force. A read
// failure is reported as an error rather than as "not configured": those are
// different facts, and answering "not configured" to "could not tell" is what
// sends a staffer to re-enter credentials that were fine.
func (p *Provider) Configured(ctx context.Context) (bool, error) {
	_, ok, err := p.AppConfig(ctx)
	return ok, err
}

// ConfiguredTx is Configured inside a caller's transaction. Handlers that are
// already in one use it rather than Configured, so deciding whether to enqueue
// a QuickBooks job does not take a second connection out of the pool while the
// first is still held.
func (p *Provider) ConfiguredTx(ctx context.Context, tx pgx.Tx) (bool, error) {
	_, ok, err := p.appConfigTx(ctx, tx)
	return ok, err
}

// WebhookVerifier returns the token Intuit's webhook signatures are checked
// against. Empty when QuickBooks is not configured — VerifySignature rejects
// every signature under an empty token, so an unconfigured deployment fails
// closed.
func (p *Provider) WebhookVerifier(ctx context.Context) (string, error) {
	cfg, ok, err := p.AppConfig(ctx)
	if err != nil || !ok {
		return "", err
	}
	return cfg.WebhookVerifier, nil
}

// OAuth returns the OAuth manager for the configuration in force, or
// ErrNotConfigured when there is none.
func (p *Provider) OAuth(ctx context.Context) (*OAuthManager, error) {
	// Checked here and not in resolve: the redirect URI is the one piece of
	// configuration only the authorization flow needs. An API call works
	// perfectly well without it, and failing those too would turn a missing
	// BASE_URL into "QuickBooks is broken" rather than "you cannot start a new
	// connection from this deployment".
	if p.cfg.RedirectURI == "" {
		return nil, fmt.Errorf("%w: BASE_URL or QB_REDIRECT_URI must be set to connect QuickBooks", ErrInvalidAppConfig)
	}
	if _, err := p.resolve(ctx); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.oauth, nil
}

// Status reports the stored connection inside a caller's transaction. It reads
// the credentials only, so it answers on a deployment whose app configuration
// is missing or unreadable — which is the case where a staffer most needs the
// settings page to still render.
func (p *Provider) Status(ctx context.Context, tx pgx.Tx) (ConnectionStatus, error) {
	return connectionStatus(ctx, tx, p.cfg.Credentials, p.cfg.TenantID)
}

// Client returns the API client for the configuration in force, or
// ErrNotConfigured when there is none. Most callers do not need this: Provider
// implements Client itself.
func (p *Provider) Client(ctx context.Context) (Client, error) {
	return p.resolve(ctx)
}

// resolve returns the client for the configuration in force, rebuilding it
// only when the configuration has changed since the last call.
func (p *Provider) resolve(ctx context.Context) (*QBClient, error) {
	cfg, ok, err := p.AppConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotConfigured
	}

	fingerprint := cfg.fingerprint()

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil && p.fingerprint == fingerprint {
		return p.client, nil
	}

	clientCfg := ClientConfig{
		ClientID:       cfg.ClientID,
		ClientSecret:   cfg.ClientSecret,
		EncryptionKey:  p.cfg.EncryptionKey,
		Environment:    cfg.Environment,
		RedirectURI:    p.cfg.RedirectURI,
		SalesItemID:    p.cfg.SalesItemID,
		ShippingItemID: p.cfg.ShippingItemID,
	}
	client := NewQBClient(clientCfg, p.cfg.TenantID, p.cfg.Credentials, p.cfg.Pool)
	p.client = client
	p.oauth = NewOAuthManager(
		clientCfg, client, p.cfg.Credentials, p.cfg.TenantID,
		p.cfg.EncryptionKey, // reuse the encryption key for HMAC signing
		p.cfg.HTTPClient, p.cfg.SecureCookies,
	)
	p.fingerprint = fingerprint
	return client, nil
}

// SaveAppConfig validates and stores an app configuration, encrypting both
// secrets. Blank secrets keep whatever is stored.
//
// It refuses while a connection is live: the OAuth tokens in qb_credentials
// were issued by the app being replaced and stop meaning anything the moment
// it changes, so silently accepting the edit would leave a connection that
// looks healthy on the settings page and fails on the next API call.
func (p *Provider) SaveAppConfig(ctx context.Context, tx pgx.Tx, in AppConfigInput) error {
	stored, err := p.cfg.AppConfigs.GetByTenantID(ctx, tx, p.cfg.TenantID)
	if err != nil {
		return err
	}

	connected, err := p.connected(ctx, tx)
	if err != nil {
		return err
	}
	if connected {
		return ErrConnected
	}

	var storedSecret, storedVerifier string
	if stored != nil {
		storedSecret, storedVerifier = stored.ClientSecret, stored.WebhookVerifier
	}

	fieldErrors := map[string]string{}

	clientID := strings.TrimSpace(in.ClientID)
	if clientID == "" {
		fieldErrors[FieldClientID] = "Enter the client ID from your Intuit app."
	}
	environment := strings.TrimSpace(in.Environment)
	if environment != domain.QBEnvironmentSandbox && environment != domain.QBEnvironmentProduction {
		fieldErrors[FieldEnvironment] = "Choose sandbox or production."
	}

	secret, err := p.secretForSave(strings.TrimSpace(in.ClientSecret), storedSecret)
	if errors.Is(err, errNoSecret) {
		fieldErrors[FieldClientSecret] = "Enter the client secret. Nothing is stored yet, so it cannot be left blank."
	} else if err != nil {
		return fmt.Errorf("encrypt qb client secret: %w", err)
	}
	verifier, err := p.secretForSave(strings.TrimSpace(in.WebhookVerifier), storedVerifier)
	if errors.Is(err, errNoSecret) {
		fieldErrors[FieldWebhookVerifier] = "Enter the webhook verifier token. Nothing is stored yet, so it cannot be left blank."
	} else if err != nil {
		return fmt.Errorf("encrypt qb webhook verifier: %w", err)
	}

	if len(fieldErrors) > 0 {
		return &AppConfigError{Fields: fieldErrors}
	}

	return p.cfg.AppConfigs.Upsert(ctx, tx, &domain.QBAppConfig{
		TenantID:        p.cfg.TenantID,
		ClientID:        clientID,
		ClientSecret:    secret,
		WebhookVerifier: verifier,
		Environment:     environment,
	})
}

// errNoSecret marks the first-save case with a secret field left blank, so the
// caller can name which field rather than reporting an encryption failure.
var errNoSecret = errors.New("no secret submitted and none stored")

// secretForSave returns the ciphertext to store: a freshly encrypted new value
// when one was submitted, otherwise the ciphertext already on the row.
func (p *Provider) secretForSave(submitted, existingCiphertext string) (string, error) {
	if submitted == "" {
		if existingCiphertext == "" {
			return "", errNoSecret
		}
		return existingCiphertext, nil
	}
	return encryptWithKey(p.cfg.EncryptionKey, submitted)
}

// ClearAppConfig removes the stored configuration, returning the deployment to
// whatever the environment supplies — usually nothing, which switches
// QuickBooks off. Refused while connected, for the same reason as a change.
func (p *Provider) ClearAppConfig(ctx context.Context, tx pgx.Tx) error {
	connected, err := p.connected(ctx, tx)
	if err != nil {
		return err
	}
	if connected {
		return ErrConnected
	}
	return p.cfg.AppConfigs.Delete(ctx, tx, p.cfg.TenantID)
}

// connected reports whether OAuth tokens are stored for this tenant.
func (p *Provider) connected(ctx context.Context, tx pgx.Tx) (bool, error) {
	_, err := p.cfg.Credentials.GetByTenantID(ctx, tx, p.cfg.TenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
