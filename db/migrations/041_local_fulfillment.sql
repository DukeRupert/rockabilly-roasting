-- +goose Up
-- Local fulfillment options.
--
-- Customers in a local zip currently always get "free local delivery" (a label
-- baked into the checkout). This split lets the merchant offer one or both of:
--   - free local delivery (route runs on configured days)
--   - free pickup at the shop (customer picks up when notified ready)
--
-- Defaults preserve current behaviour — delivery on, pickup off — so the
-- migration is a no-op until the merchant flips pickup on in admin settings.

ALTER TABLE shipping_config
    ADD COLUMN local_delivery_enabled    boolean NOT NULL DEFAULT true,
    ADD COLUMN local_pickup_enabled      boolean NOT NULL DEFAULT false,
    ADD COLUMN local_pickup_instructions text    NOT NULL DEFAULT '',
    ADD COLUMN local_delivery_days       text    NOT NULL DEFAULT 'Mondays and Thursdays';

-- Customer's saved preference for future local-eligible orders. NULL means
-- "ask each time at checkout". Constrained to the two local methods because
-- "shipped" isn't a preference — it's the fallback when no local zip applies.
ALTER TABLE customers
    ADD COLUMN preferred_local_fulfillment text
        CHECK (preferred_local_fulfillment IN ('local_delivery', 'pickup'));

-- +goose Down
ALTER TABLE customers
    DROP COLUMN IF EXISTS preferred_local_fulfillment;

ALTER TABLE shipping_config
    DROP COLUMN IF EXISTS local_delivery_enabled,
    DROP COLUMN IF EXISTS local_pickup_enabled,
    DROP COLUMN IF EXISTS local_pickup_instructions,
    DROP COLUMN IF EXISTS local_delivery_days;
