-- +goose Up

-- The QB credential store keys every read/write on tenant_id and upserts via
-- ON CONFLICT ON CONSTRAINT uq_qb_tenant, but migration 031 shipped the table
-- without either — the OAuth connect flow could never persist tokens.
--
-- Any pre-existing rows are unreadable dead weight: every store query
-- referenced the missing tenant_id column and errored, so nothing in the app
-- ever used them (they could only be manual inserts holding stale tokens).
-- Clearing the table lets the column be NOT NULL without a backfill guess
-- about which tenant ID a deployment uses; QuickBooks must be (re)connected
-- through the admin OAuth flow either way.
DELETE FROM qb_credentials;

ALTER TABLE qb_credentials ADD COLUMN tenant_id uuid NOT NULL;

ALTER TABLE qb_credentials ADD CONSTRAINT uq_qb_tenant UNIQUE (tenant_id);

-- +goose Down

ALTER TABLE qb_credentials DROP CONSTRAINT IF EXISTS uq_qb_tenant;

ALTER TABLE qb_credentials DROP COLUMN IF EXISTS tenant_id;
