package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/platform/quickbooks"
)

// QBCredentialStore provides database access for QuickBooks OAuth credentials.
type QBCredentialStore struct{}

// NewQBCredentialStore creates a new QBCredentialStore.
func NewQBCredentialStore() *QBCredentialStore {
	return &QBCredentialStore{}
}

// GetByTenantID returns the QB credentials for a tenant.
func (s *QBCredentialStore) GetByTenantID(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (*quickbooks.Credentials, error) {
	var c quickbooks.Credentials
	err := tx.QueryRow(ctx,
		`SELECT id, tenant_id, realm_id, access_token, refresh_token,
		        access_expires_at, refresh_expires_at, created_at, updated_at
		 FROM qb_credentials
		 WHERE tenant_id = $1`, tenantID,
	).Scan(
		&c.ID, &c.TenantID, &c.RealmID,
		&c.AccessToken, &c.RefreshToken,
		&c.AccessExpiresAt, &c.RefreshExpiresAt,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get qb credentials for tenant %s: %w", tenantID, err)
	}
	return &c, nil
}

// Upsert inserts or updates QB credentials for a tenant.
func (s *QBCredentialStore) Upsert(ctx context.Context, tx pgx.Tx, creds *quickbooks.Credentials) error {
	now := time.Now()
	if creds.ID == uuid.Nil {
		creds.ID = uuid.New()
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO qb_credentials (id, tenant_id, realm_id, access_token, refresh_token,
		                              access_expires_at, refresh_expires_at, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT ON CONSTRAINT uq_qb_tenant
		 DO UPDATE SET realm_id = EXCLUDED.realm_id,
		               access_token = EXCLUDED.access_token,
		               refresh_token = EXCLUDED.refresh_token,
		               access_expires_at = EXCLUDED.access_expires_at,
		               refresh_expires_at = EXCLUDED.refresh_expires_at,
		               updated_at = EXCLUDED.updated_at`,
		creds.ID, creds.TenantID, creds.RealmID,
		creds.AccessToken, creds.RefreshToken,
		creds.AccessExpiresAt, creds.RefreshExpiresAt,
		now, now,
	)
	if err != nil {
		return fmt.Errorf("upsert qb credentials: %w", err)
	}
	return nil
}

// Delete removes QB credentials for a tenant (disconnect).
func (s *QBCredentialStore) Delete(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
	_, err := tx.Exec(ctx, `DELETE FROM qb_credentials WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return fmt.Errorf("delete qb credentials: %w", err)
	}
	return nil
}
