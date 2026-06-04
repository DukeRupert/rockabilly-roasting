-- +goose Up

-- The prices.customer_group_id column backed the group-pricing feature removed
-- in v1.54.0. No rows use it and no query reads or writes it any more — drop it
-- (and its index). The base/price-list price queries no longer filter on it.
DROP INDEX IF EXISTS idx_prices_customer_group_id;
ALTER TABLE prices DROP COLUMN IF EXISTS customer_group_id;

-- +goose Down
ALTER TABLE prices
    ADD COLUMN customer_group_id uuid REFERENCES customer_groups(id) ON DELETE CASCADE;
CREATE INDEX idx_prices_customer_group_id ON prices(customer_group_id);
