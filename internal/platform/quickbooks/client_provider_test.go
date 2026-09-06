package quickbooks

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// The fakes below ignore the transaction they are handed, which is what lets
// these tests pass a nil pgx.Tx and exercise the resolution and save rules
// without a database. Everything a database would decide here — the row's
// existence and its contents — is exactly what the fakes control.

type fakeAppConfigStore struct {
	row *domain.QBAppConfig
	err error
}

func (f *fakeAppConfigStore) GetByTenantID(context.Context, pgx.Tx, uuid.UUID) (*domain.QBAppConfig, error) {
	return f.row, f.err
}

func (f *fakeAppConfigStore) Upsert(_ context.Context, _ pgx.Tx, cfg *domain.QBAppConfig) error {
	stored := *cfg
	f.row = &stored
	return nil
}

func (f *fakeAppConfigStore) Delete(context.Context, pgx.Tx, uuid.UUID) error {
	f.row = nil
	return nil
}

type fakeCredStore struct {
	creds *domain.QBCredentials
}

func (f *fakeCredStore) GetByTenantID(context.Context, pgx.Tx, uuid.UUID) (*domain.QBCredentials, error) {
	if f.creds == nil {
		return nil, pgx.ErrNoRows
	}
	return f.creds, nil
}

func (f *fakeCredStore) Upsert(context.Context, pgx.Tx, *domain.QBCredentials) error { return nil }
func (f *fakeCredStore) Delete(context.Context, pgx.Tx, uuid.UUID) error             { return nil }

func testKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}

func newTestProvider(apps *fakeAppConfigStore, creds *fakeCredStore, env AppConfig) *Provider {
	return NewProvider(ProviderConfig{
		AppConfigs:    apps,
		Credentials:   creds,
		TenantID:      uuid.New(),
		EncryptionKey: testKey(),
		RedirectURI:   "https://shop.example.com" + CallbackPath,
		EnvFallback:   env,
	})
}

func envConfig() AppConfig {
	return AppConfig{
		ClientID:        "env-client",
		ClientSecret:    "env-secret",
		WebhookVerifier: "env-verifier",
		Environment:     domain.QBEnvironmentProduction,
	}
}

func TestAppConfigFallsBackToTheEnvironment(t *testing.T) {
	p := newTestProvider(&fakeAppConfigStore{}, &fakeCredStore{}, envConfig())

	cfg, ok, err := p.appConfigTx(context.Background(), nil)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "env-client", cfg.ClientID)
	assert.False(t, cfg.FromDatabase)
}

func TestAppConfigUnconfiguredWhenNeitherSourceIsComplete(t *testing.T) {
	// A half-set environment is not a configuration. Treating it as one moves
	// the failure from here to the middle of an OAuth handshake.
	p := newTestProvider(&fakeAppConfigStore{}, &fakeCredStore{}, AppConfig{ClientID: "env-client"})

	_, ok, err := p.appConfigTx(context.Background(), nil)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestStoredConfigurationWinsOverTheEnvironment(t *testing.T) {
	apps := &fakeAppConfigStore{}
	p := newTestProvider(apps, &fakeCredStore{}, envConfig())

	require.NoError(t, p.SaveAppConfig(context.Background(), nil, AppConfigInput{
		ClientID:        "saved-client",
		ClientSecret:    "saved-secret",
		WebhookVerifier: "saved-verifier",
		Environment:     domain.QBEnvironmentSandbox,
	}))

	cfg, ok, err := p.appConfigTx(context.Background(), nil)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "saved-client", cfg.ClientID)
	assert.Equal(t, "saved-secret", cfg.ClientSecret)
	assert.Equal(t, "saved-verifier", cfg.WebhookVerifier)
	assert.True(t, cfg.FromDatabase)
	assert.True(t, cfg.Sandbox())

	// Neither secret is stored in the clear.
	assert.NotEqual(t, "saved-secret", apps.row.ClientSecret)
	assert.NotEqual(t, "saved-verifier", apps.row.WebhookVerifier)
}

func TestBlankSecretsKeepTheStoredOnes(t *testing.T) {
	apps := &fakeAppConfigStore{}
	p := newTestProvider(apps, &fakeCredStore{}, AppConfig{})
	ctx := context.Background()

	require.NoError(t, p.SaveAppConfig(ctx, nil, AppConfigInput{
		ClientID:        "client-a",
		ClientSecret:    "secret-a",
		WebhookVerifier: "verifier-a",
		Environment:     domain.QBEnvironmentSandbox,
	}))

	// Correcting the client ID must not demand credentials the staffer may not
	// have in front of them — the form never renders them back.
	require.NoError(t, p.SaveAppConfig(ctx, nil, AppConfigInput{
		ClientID:    "client-b",
		Environment: domain.QBEnvironmentSandbox,
	}))

	cfg, ok, err := p.appConfigTx(ctx, nil)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "client-b", cfg.ClientID)
	assert.Equal(t, "secret-a", cfg.ClientSecret)
	assert.Equal(t, "verifier-a", cfg.WebhookVerifier)
}

func TestFirstSaveRequiresBothSecrets(t *testing.T) {
	p := newTestProvider(&fakeAppConfigStore{}, &fakeCredStore{}, AppConfig{})

	err := p.SaveAppConfig(context.Background(), nil, AppConfigInput{
		ClientID:        "client",
		WebhookVerifier: "verifier",
		Environment:     domain.QBEnvironmentSandbox,
	})
	assert.ErrorIs(t, err, ErrInvalidAppConfig)

	var invalid *AppConfigError
	require.ErrorAs(t, err, &invalid)
	assert.Contains(t, invalid.Fields, FieldClientSecret)
	assert.NotContains(t, invalid.Fields, FieldWebhookVerifier, "the field that was filled in must not be marked")
}

func TestValidationReportsEveryBadFieldAtOnce(t *testing.T) {
	// One round trip, not four. The secrets cannot be re-rendered, so every
	// rejected save costs the staffer both of them again — reporting one
	// problem per submit would make that a repeated cost.
	p := newTestProvider(&fakeAppConfigStore{}, &fakeCredStore{}, AppConfig{})

	err := p.SaveAppConfig(context.Background(), nil, AppConfigInput{Environment: "prod"})

	var invalid *AppConfigError
	require.ErrorAs(t, err, &invalid)
	assert.ElementsMatch(t,
		[]string{FieldClientID, FieldClientSecret, FieldWebhookVerifier, FieldEnvironment},
		keysOf(invalid.Fields))
	for field, msg := range invalid.Fields {
		assert.NotEmpty(t, msg, "%s needs a sentence a staffer can act on", field)
	}
}

// The field names are the form's input names — the admin marks inputs by
// looking itself up in this map, so a rename on either side has to be a rename
// on both.
func TestValidationFieldNamesMatchTheFormInputs(t *testing.T) {
	assert.Equal(t, "qb_client_id", FieldClientID)
	assert.Equal(t, "qb_client_secret", FieldClientSecret)
	assert.Equal(t, "qb_webhook_verifier", FieldWebhookVerifier)
	assert.Equal(t, "qb_environment", FieldEnvironment)
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestSaveRejectsAnUnknownEnvironment(t *testing.T) {
	p := newTestProvider(&fakeAppConfigStore{}, &fakeCredStore{}, AppConfig{})

	err := p.SaveAppConfig(context.Background(), nil, AppConfigInput{
		ClientID:        "client",
		ClientSecret:    "secret",
		WebhookVerifier: "verifier",
		Environment:     "prod",
	})
	assert.ErrorIs(t, err, ErrInvalidAppConfig)

	var invalid *AppConfigError
	require.ErrorAs(t, err, &invalid)
	assert.Contains(t, invalid.Fields, FieldEnvironment)
}

func TestSaveAndClearAreRefusedWhileConnected(t *testing.T) {
	// The stored tokens were issued by the app being replaced. Accepting the
	// edit would leave a connection that reads as healthy and fails on the
	// next API call.
	creds := &fakeCredStore{creds: &domain.QBCredentials{RealmID: "123"}}
	p := newTestProvider(&fakeAppConfigStore{}, creds, AppConfig{})

	err := p.SaveAppConfig(context.Background(), nil, AppConfigInput{
		ClientID:        "client",
		ClientSecret:    "secret",
		WebhookVerifier: "verifier",
		Environment:     domain.QBEnvironmentSandbox,
	})
	assert.ErrorIs(t, err, ErrConnected)

	assert.ErrorIs(t, p.ClearAppConfig(context.Background(), nil), ErrConnected)
}

func TestClearReturnsToTheEnvironment(t *testing.T) {
	apps := &fakeAppConfigStore{}
	p := newTestProvider(apps, &fakeCredStore{}, envConfig())
	ctx := context.Background()

	require.NoError(t, p.SaveAppConfig(ctx, nil, AppConfigInput{
		ClientID:        "saved-client",
		ClientSecret:    "saved-secret",
		WebhookVerifier: "saved-verifier",
		Environment:     domain.QBEnvironmentSandbox,
	}))
	require.NoError(t, p.ClearAppConfig(ctx, nil))

	cfg, ok, err := p.appConfigTx(ctx, nil)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "env-client", cfg.ClientID)
}

func TestARowThatWillNotDecryptIsNotSilentlyReplacedByTheEnvironment(t *testing.T) {
	// The environment may describe a different Intuit app. Falling through to
	// it is how a shop ends up writing into the wrong company's books.
	apps := &fakeAppConfigStore{row: &domain.QBAppConfig{
		ClientID:        "saved-client",
		ClientSecret:    "not-ciphertext",
		WebhookVerifier: "not-ciphertext",
		Environment:     domain.QBEnvironmentSandbox,
	}}
	p := newTestProvider(apps, &fakeCredStore{}, envConfig())

	_, _, err := p.appConfigTx(context.Background(), nil)
	assert.ErrorIs(t, err, ErrAppConfigUnreadable)
}

func TestAStoredRowWithNoEncryptionKeyNamesTheVariable(t *testing.T) {
	apps := &fakeAppConfigStore{row: &domain.QBAppConfig{ClientID: "saved-client"}}
	p := NewProvider(ProviderConfig{
		AppConfigs:  apps,
		Credentials: &fakeCredStore{},
		TenantID:    uuid.New(),
	})

	_, _, err := p.appConfigTx(context.Background(), nil)
	assert.ErrorIs(t, err, ErrAppConfigUnreadable)
	assert.Contains(t, err.Error(), "APP_SECRET")
}

func TestConfigurationChangeChangesTheFingerprint(t *testing.T) {
	// The fingerprint is what makes a credential saved in the admin take
	// effect on the next call rather than the next restart.
	a := AppConfig{ClientID: "c", ClientSecret: "s", WebhookVerifier: "v", Environment: domain.QBEnvironmentSandbox}
	b := a
	b.Environment = domain.QBEnvironmentProduction

	assert.Equal(t, a.fingerprint(), a.fingerprint())
	assert.NotEqual(t, a.fingerprint(), b.fingerprint())
	// The secrets are hashed, not carried.
	assert.NotContains(t, a.fingerprint(), "s")
}

func TestNotConfiguredIsPermanent(t *testing.T) {
	// A job that hits it must fail once with a message naming the setting, not
	// retry for hours against a configuration only a human can supply.
	assert.False(t, IsRetryable(ErrNotConfigured))
}
