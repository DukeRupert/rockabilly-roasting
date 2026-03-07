-- +goose Up

-- Store-level settings (singleton row — enforced by CHECK constraint).
CREATE TABLE store_settings (
    id         bool PRIMARY KEY DEFAULT true CHECK (id = true),
    tax_mode   text NOT NULL DEFAULT 'none'
                   CHECK (tax_mode IN ('stripe_tax', 'flat_rate', 'none')),
    tax_rate   numeric(6,4),    -- e.g. 0.0875 for 8.75%; null unless flat_rate
    tax_label  text,            -- e.g. "WA Sales Tax"; shown at checkout
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Seed with defaults (no tax).
INSERT INTO store_settings (tax_mode) VALUES ('none');

-- Product-level tax exemption.
ALTER TABLE products
    ADD COLUMN tax_exempt bool NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE products DROP COLUMN IF EXISTS tax_exempt;
DROP TABLE IF EXISTS store_settings;
