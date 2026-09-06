package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
)

// QBAppConfigStore provides database access for the Intuit app a QuickBooks
// connection is made through.
type QBAppConfigStore struct{}

// NewQBAppConfigStore creates a new QBAppConfigStore.
func NewQBAppConfigStore() *QBAppConfigStore {
	return &QBAppConfigStore{}
}

// GetByTenantID returns the stored app config for a tenant, or nil when the
// tenant has none.
//
// Nil rather than pgx.ErrNoRows because "no row" is the ordinary state here,
// not an exception: a deployment that has never opened the settings form, or
// one still running on the environment variables, has no row and the caller
// falls back rather than fails.
func (s *QBAppConfigStore) GetByTenantID(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (*domain.QBAppConfig, error) {
	var c domain.QBAppConfig
	err := tx.QueryRow(ctx,
		`SELECT tenant_id, client_id, client_secret, webhook_verifier,
		        environment, created_at, updated_at
		 FROM qb_app_config
		 WHERE tenant_id = $1`, tenantID,
	).Scan(
		&c.TenantID, &c.ClientID, &c.ClientSecret, &c.WebhookVerifier,
		&c.Environment, &c.CreatedAt, &c.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get qb app config for tenant %s: %w", tenantID, err)
	}
	return &c, nil
}

// Upsert inserts or replaces the app config for a tenant.
func (s *QBAppConfigStore) Upsert(ctx context.Context, tx pgx.Tx, cfg *domain.QBAppConfig) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO qb_app_config (tenant_id, client_id, client_secret, webhook_verifier, environment)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (tenant_id)
		 DO UPDATE SET client_id        = EXCLUDED.client_id,
		               client_secret    = EXCLUDED.client_secret,
		               webhook_verifier = EXCLUDED.webhook_verifier,
		               environment      = EXCLUDED.environment,
		               updated_at       = now()`,
		cfg.TenantID, cfg.ClientID, cfg.ClientSecret, cfg.WebhookVerifier, cfg.Environment,
	)
	if err != nil {
		return fmt.Errorf("upsert qb app config for tenant %s: %w", cfg.TenantID, err)
	}
	return nil
}

// Delete removes a tenant's app config, returning the deployment to whatever
// the environment supplies (usually nothing, which switches QuickBooks off).
func (s *QBAppConfigStore) Delete(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
	_, err := tx.Exec(ctx, `DELETE FROM qb_app_config WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return fmt.Errorf("delete qb app config for tenant %s: %w", tenantID, err)
	}
	return nil
}
