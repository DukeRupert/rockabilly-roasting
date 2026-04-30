-- +goose Up
-- Add merchant shipping origin and packaging tare weight to shipping_config.
-- Origin is captured for future Shippo / live-rates work; for the Pirate Ship
-- CSV round-trip (Pirate Ship has its own origin config), it's informational.
-- Tare weight is added to every export row to account for box + dunnage.

ALTER TABLE shipping_config
    ADD COLUMN origin_name      text NOT NULL DEFAULT '',
    ADD COLUMN origin_street1   text NOT NULL DEFAULT '',
    ADD COLUMN origin_street2   text NOT NULL DEFAULT '',
    ADD COLUMN origin_city      text NOT NULL DEFAULT '',
    ADD COLUMN origin_state     text NOT NULL DEFAULT '',
    ADD COLUMN origin_zip       text NOT NULL DEFAULT '',
    ADD COLUMN origin_country   text NOT NULL DEFAULT 'US',
    ADD COLUMN tare_weight_oz   numeric NOT NULL DEFAULT 0;

-- Seed Rockabilly's known origin so the admin form starts populated. Safe to
-- re-run: the UPDATE only fires when the columns are still at their defaults.
UPDATE shipping_config
   SET origin_name    = 'Rockabilly Roasting Co.',
       origin_street1 = '101 W Kennewick Ave',
       origin_city    = 'Kennewick',
       origin_state   = 'WA',
       origin_zip     = '99336',
       origin_country = 'US'
 WHERE origin_name = '' AND origin_street1 = '';

-- +goose Down
ALTER TABLE shipping_config
    DROP COLUMN IF EXISTS origin_name,
    DROP COLUMN IF EXISTS origin_street1,
    DROP COLUMN IF EXISTS origin_street2,
    DROP COLUMN IF EXISTS origin_city,
    DROP COLUMN IF EXISTS origin_state,
    DROP COLUMN IF EXISTS origin_zip,
    DROP COLUMN IF EXISTS origin_country,
    DROP COLUMN IF EXISTS tare_weight_oz;
