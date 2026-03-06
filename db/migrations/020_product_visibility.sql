-- +goose Up
ALTER TABLE products
    ADD COLUMN visibility text NOT NULL DEFAULT 'public'
        CHECK (visibility IN ('public', 'wholesale', 'restricted'));

CREATE TABLE product_group_visibility (
    product_id        uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    customer_group_id uuid NOT NULL REFERENCES customer_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (product_id, customer_group_id)
);

CREATE INDEX idx_products_visibility ON products (visibility);

-- +goose Down
DROP TABLE IF EXISTS product_group_visibility;
DROP INDEX IF EXISTS idx_products_visibility;
ALTER TABLE products DROP COLUMN IF EXISTS visibility;
