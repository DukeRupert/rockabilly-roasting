-- +goose Up

-- Token storage for QuickBooks Online OAuth2 credentials
CREATE TABLE qb_credentials (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    realm_id           text NOT NULL,
    access_token       text NOT NULL,
    refresh_token      text NOT NULL,
    access_expires_at  timestamptz NOT NULL,
    refresh_expires_at timestamptz NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_qb_tenant UNIQUE (tenant_id)
);

-- Customer sync tracking
ALTER TABLE customers
    ADD COLUMN qb_customer_id text,
    ADD COLUMN qb_synced_at   timestamptz;

CREATE INDEX idx_customers_qb_id ON customers (qb_customer_id)
    WHERE qb_customer_id IS NOT NULL;

-- Order invoice tracking
ALTER TABLE orders
    ADD COLUMN qb_invoice_id  text,
    ADD COLUMN qb_invoice_no  text,
    ADD COLUMN qb_synced_at   timestamptz;

CREATE INDEX idx_orders_qb_invoice ON orders (qb_invoice_id)
    WHERE qb_invoice_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_orders_qb_invoice;
ALTER TABLE orders
    DROP COLUMN IF EXISTS qb_invoice_id,
    DROP COLUMN IF EXISTS qb_invoice_no,
    DROP COLUMN IF EXISTS qb_synced_at;

DROP INDEX IF EXISTS idx_customers_qb_id;
ALTER TABLE customers
    DROP COLUMN IF EXISTS qb_customer_id,
    DROP COLUMN IF EXISTS qb_synced_at;

DROP TABLE IF EXISTS qb_credentials;
