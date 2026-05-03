-- +goose Up
ALTER TABLE customers
    ADD COLUMN price_list_id uuid REFERENCES price_lists(id) ON DELETE SET NULL;
CREATE INDEX idx_customers_price_list_id ON customers (price_list_id)
    WHERE price_list_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_customers_price_list_id;
ALTER TABLE customers DROP COLUMN IF EXISTS price_list_id;
