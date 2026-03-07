-- +goose Up

-- Track which order redeemed a coupon code for admin lookup.
ALTER TABLE coupon_codes
    ADD COLUMN redeemed_by_order_id uuid REFERENCES orders(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE coupon_codes DROP COLUMN IF EXISTS redeemed_by_order_id;
