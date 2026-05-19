-- +goose Up
-- Soft-delete column for variants. When set, the variant is hidden from
-- storefront/wholesale listings and add-to-cart, but order history, invoices,
-- and existing subscriptions on the variant remain functional.
ALTER TABLE variants
    ADD COLUMN archived_at timestamptz;

-- Partial index for the common storefront query (active variants per product).
CREATE INDEX idx_variants_product_active
    ON variants(product_id)
    WHERE archived_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_variants_product_active;
ALTER TABLE variants DROP COLUMN IF EXISTS archived_at;
