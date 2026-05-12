-- +goose Up
-- Origin contact info. Required by USPS via Shippo — transactions fail with
-- "Seller info missing email or phone" without these. Pirate Ship had its
-- own origin config so these fields didn't exist before live label buying.
ALTER TABLE shipping_config
    ADD COLUMN origin_email text NOT NULL DEFAULT '',
    ADD COLUMN origin_phone text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE shipping_config
    DROP COLUMN IF EXISTS origin_email,
    DROP COLUMN IF EXISTS origin_phone;
