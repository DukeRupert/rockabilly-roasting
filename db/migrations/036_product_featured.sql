-- +goose Up
ALTER TABLE products
    ADD COLUMN is_featured boolean NOT NULL DEFAULT false;

-- Partial index: there are typically very few featured products, so an index
-- only on rows where the flag is set keeps the home-page lookup fast and cheap.
CREATE INDEX idx_products_is_featured ON products (is_featured) WHERE is_featured;

-- +goose Down
DROP INDEX IF EXISTS idx_products_is_featured;
ALTER TABLE products DROP COLUMN IF EXISTS is_featured;
