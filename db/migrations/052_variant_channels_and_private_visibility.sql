-- +goose Up

-- Variant channel availability: which sales channels a variant may be ordered in.
-- Both default true so every existing variant stays orderable in retail AND
-- wholesale (no behaviour change on deploy). Setting wholesale_available=false on,
-- e.g., a 1lb variant hides it from the wholesale catalog and refuses it at
-- wholesale add-to-cart, while it stays on the retail storefront.
ALTER TABLE variants
    ADD COLUMN retail_available    boolean NOT NULL DEFAULT true,
    ADD COLUMN wholesale_available boolean NOT NULL DEFAULT true;

-- 'private' visibility: a product orderable only by the specific customers granted
-- access in product_customer_visibility. This is per-customer white-labelling —
-- access scoping only; the product keeps its normal title/images/price.
ALTER TABLE products DROP CONSTRAINT products_visibility_check;
ALTER TABLE products ADD CONSTRAINT products_visibility_check
    CHECK (visibility IN ('public', 'wholesale', 'restricted', 'private'));

CREATE TABLE product_customer_visibility (
    product_id  uuid NOT NULL REFERENCES products(id)  ON DELETE CASCADE,
    customer_id uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    PRIMARY KEY (product_id, customer_id)
);

CREATE INDEX idx_pcv_customer_id ON product_customer_visibility (customer_id);

-- +goose Down
DROP TABLE IF EXISTS product_customer_visibility;

ALTER TABLE products DROP CONSTRAINT products_visibility_check;
ALTER TABLE products ADD CONSTRAINT products_visibility_check
    CHECK (visibility IN ('public', 'wholesale', 'restricted'));

ALTER TABLE variants
    DROP COLUMN IF EXISTS retail_available,
    DROP COLUMN IF EXISTS wholesale_available;
