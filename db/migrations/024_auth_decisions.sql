-- +goose Up

-- Safety check: ensure no meaningful guest rows exist before dropping.
-- If this fails, handle guest records before re-running.
DO $$
BEGIN
    IF (SELECT count(*) FROM customers WHERE is_guest = true) > 0 THEN
        RAISE EXCEPTION 'Found rows with is_guest = true. Handle these before dropping the column.';
    END IF;
END $$;

ALTER TABLE customers
    DROP COLUMN is_guest;

ALTER TABLE customers
    ADD COLUMN two_fa_enabled bool NOT NULL DEFAULT false,
    ADD COLUMN two_fa_method  text CHECK (two_fa_method IN ('magic_link', 'totp'));

CREATE TABLE magic_link_tokens (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    token_hash  text NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_magic_link_tokens_customer ON magic_link_tokens (customer_id);

-- +goose Down

DROP INDEX IF EXISTS idx_magic_link_tokens_customer;
DROP TABLE IF EXISTS magic_link_tokens;

ALTER TABLE customers
    DROP COLUMN IF EXISTS two_fa_method,
    DROP COLUMN IF EXISTS two_fa_enabled;

ALTER TABLE customers
    ADD COLUMN is_guest boolean NOT NULL DEFAULT false;
