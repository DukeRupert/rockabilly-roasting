-- +goose Up
ALTER TABLE customers
    ADD COLUMN account_type      text NOT NULL DEFAULT 'retail'
        CHECK (account_type IN ('retail', 'wholesale')),
    ADD COLUMN wholesale_status  text
        CHECK (wholesale_status IN ('pending', 'approved', 'suspended')),
    ADD COLUMN company_name      text,
    ADD COLUMN website           text,
    ADD COLUMN wholesale_notes   text,
    ADD COLUMN approved_at       timestamptz,
    ADD COLUMN approved_by       uuid REFERENCES staff(id);

CREATE INDEX idx_customers_account_type ON customers (account_type);
CREATE INDEX idx_customers_wholesale_status ON customers (wholesale_status) WHERE account_type = 'wholesale';

-- +goose Down
DROP INDEX IF EXISTS idx_customers_wholesale_status;
DROP INDEX IF EXISTS idx_customers_account_type;
ALTER TABLE customers
    DROP COLUMN IF EXISTS approved_by,
    DROP COLUMN IF EXISTS approved_at,
    DROP COLUMN IF EXISTS wholesale_notes,
    DROP COLUMN IF EXISTS website,
    DROP COLUMN IF EXISTS company_name,
    DROP COLUMN IF EXISTS wholesale_status,
    DROP COLUMN IF EXISTS account_type;
