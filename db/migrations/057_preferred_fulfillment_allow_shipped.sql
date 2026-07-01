-- +goose Up
-- Let a local-eligible customer opt to have their orders shipped instead of
-- delivered/picked up. "shipped" was previously excluded from the preference
-- (migration 041) because it was only ever the fallback for non-local zips —
-- but a local customer may prefer their subscription mailed, so it's now a
-- valid saved choice. The constraint below is the auto-generated name Postgres
-- gave the inline column CHECK in migration 041.
ALTER TABLE customers
    DROP CONSTRAINT customers_preferred_local_fulfillment_check,
    ADD CONSTRAINT customers_preferred_local_fulfillment_check
        CHECK (preferred_local_fulfillment IN ('local_delivery', 'pickup', 'shipped'));

-- +goose Down
-- Reverting narrows the domain, so any 'shipped' preferences must be cleared
-- first (back to "ask each time") or the constraint add would fail.
UPDATE customers
    SET preferred_local_fulfillment = NULL
    WHERE preferred_local_fulfillment = 'shipped';

ALTER TABLE customers
    DROP CONSTRAINT customers_preferred_local_fulfillment_check,
    ADD CONSTRAINT customers_preferred_local_fulfillment_check
        CHECK (preferred_local_fulfillment IN ('local_delivery', 'pickup'));
