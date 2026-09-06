package store_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func TestQBAppConfigMissingRowIsNotAnError(t *testing.T) {
	// Every deployment starts here, and one still running on the environment
	// variables stays here — so "no row" has to be an ordinary answer the
	// provider can fall back from, not an error it has to unwrap.
	tx := testutil.NewTestTx(t, testPool)
	s := store.NewQBAppConfigStore()

	cfg, err := s.GetByTenantID(t.Context(), tx, uuid.New())
	require.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestQBAppConfigRoundTrip(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	s := store.NewQBAppConfigStore()
	tenantID := uuid.New()

	require.NoError(t, s.Upsert(t.Context(), tx, &domain.QBAppConfig{
		TenantID:        tenantID,
		ClientID:        "client-a",
		ClientSecret:    "ciphertext-a",
		WebhookVerifier: "ciphertext-verifier",
		Environment:     domain.QBEnvironmentSandbox,
	}))

	cfg, err := s.GetByTenantID(t.Context(), tx, tenantID)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "client-a", cfg.ClientID)
	assert.Equal(t, "ciphertext-a", cfg.ClientSecret)
	assert.Equal(t, "ciphertext-verifier", cfg.WebhookVerifier)
	assert.Equal(t, domain.QBEnvironmentSandbox, cfg.Environment)
}

func TestQBAppConfigUpsertReplacesInPlace(t *testing.T) {
	// One app per tenant. A second row would mean two answers to "which app
	// issued the tokens in qb_credentials".
	tx := testutil.NewTestTx(t, testPool)
	s := store.NewQBAppConfigStore()
	tenantID := uuid.New()

	require.NoError(t, s.Upsert(t.Context(), tx, &domain.QBAppConfig{
		TenantID: tenantID, ClientID: "client-a", ClientSecret: "a", WebhookVerifier: "a",
		Environment: domain.QBEnvironmentSandbox,
	}))
	require.NoError(t, s.Upsert(t.Context(), tx, &domain.QBAppConfig{
		TenantID: tenantID, ClientID: "client-b", ClientSecret: "b", WebhookVerifier: "b",
		Environment: domain.QBEnvironmentProduction,
	}))

	cfg, err := s.GetByTenantID(t.Context(), tx, tenantID)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "client-b", cfg.ClientID)
	assert.Equal(t, domain.QBEnvironmentProduction, cfg.Environment)

	var count int
	require.NoError(t, tx.QueryRow(t.Context(),
		`SELECT count(*) FROM qb_app_config WHERE tenant_id = $1`, tenantID).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestQBAppConfigRejectsAnUnknownEnvironment(t *testing.T) {
	// The database is the last line: "prod" is not "production", and the
	// difference decides whether a real company's books get written.
	tx := testutil.NewTestTx(t, testPool)
	s := store.NewQBAppConfigStore()

	err := s.Upsert(t.Context(), tx, &domain.QBAppConfig{
		TenantID: uuid.New(), ClientID: "client", ClientSecret: "a", WebhookVerifier: "a",
		Environment: "prod",
	})
	assert.Error(t, err)
}

func TestQBAppConfigDelete(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	s := store.NewQBAppConfigStore()
	tenantID := uuid.New()

	require.NoError(t, s.Upsert(t.Context(), tx, &domain.QBAppConfig{
		TenantID: tenantID, ClientID: "client", ClientSecret: "a", WebhookVerifier: "a",
		Environment: domain.QBEnvironmentSandbox,
	}))
	require.NoError(t, s.Delete(t.Context(), tx, tenantID))

	cfg, err := s.GetByTenantID(t.Context(), tx, tenantID)
	require.NoError(t, err)
	assert.Nil(t, cfg)
}
