-- +goose Up
CREATE TABLE cart_items (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    cart_id     uuid NOT NULL REFERENCES carts(id) ON DELETE CASCADE,
    variant_id  uuid NOT NULL REFERENCES variants(id),
    quantity    int  NOT NULL CHECK (quantity > 0),
    unit_price  int  NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_cart_variant UNIQUE (cart_id, variant_id)
);

CREATE INDEX idx_cart_items_cart ON cart_items (cart_id);

-- +goose Down
DROP TABLE IF EXISTS cart_items;
