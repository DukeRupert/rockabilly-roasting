-- +goose Up
CREATE TABLE discounts (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                text NOT NULL,
    description         text,
    type                text NOT NULL,
    value               int NOT NULL,
    minimum_order_cents int,
    starts_at           timestamptz,
    expires_at          timestamptz,
    active              boolean NOT NULL DEFAULT true,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE coupon_codes (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    discount_id  uuid NOT NULL REFERENCES discounts(id) ON DELETE CASCADE,
    code         text NOT NULL UNIQUE,
    customer_id  uuid REFERENCES customers(id) ON DELETE SET NULL,
    redeemed_at  timestamptz,
    redeemed_by  uuid REFERENCES customers(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_coupon_codes_discount_id ON coupon_codes(discount_id);

-- Now that the discounts and coupon_codes tables exist, add the FKs from carts.
ALTER TABLE carts
    ADD CONSTRAINT fk_carts_applied_discount_id
    FOREIGN KEY (applied_discount_id) REFERENCES discounts(id) ON DELETE SET NULL;

ALTER TABLE carts
    ADD CONSTRAINT fk_carts_applied_coupon_code_id
    FOREIGN KEY (applied_coupon_code_id) REFERENCES coupon_codes(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE carts DROP CONSTRAINT IF EXISTS fk_carts_applied_coupon_code_id;
ALTER TABLE carts DROP CONSTRAINT IF EXISTS fk_carts_applied_discount_id;
DROP TABLE IF EXISTS coupon_codes;
DROP TABLE IF EXISTS discounts;
