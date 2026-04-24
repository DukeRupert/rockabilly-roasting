-- +goose Up
-- Add a local-zip allowlist to shipping_config. Orders shipping to one of
-- these zips ship free regardless of subtotal; everyone else pays
-- flat_rate_cents unless the subtotal meets free_shipping_threshold.

ALTER TABLE shipping_config
    ADD COLUMN local_zip_codes text[] NOT NULL DEFAULT '{}';

-- Seed the singleton row if the table is empty. Rockabilly Roasting defaults:
-- $6 flat, free over $50, local delivery free for the Tri-Cities metro
-- (Kennewick, Richland, Pasco, West Richland, Benton City, Burbank, Prosser).
INSERT INTO shipping_config (flat_rate_cents, free_shipping_threshold, currency, local_zip_codes)
SELECT 600, 5000, 'usd', ARRAY[
    '99301', '99302',           -- Pasco
    '99320',                     -- Benton City
    '99323',                     -- Burbank
    '99336', '99337', '99338',  -- Kennewick
    '99350',                     -- Prosser
    '99352', '99354',           -- Richland
    '99353'                      -- West Richland
]
WHERE NOT EXISTS (SELECT 1 FROM shipping_config);

-- If a row already existed (e.g. from a previous deploy), populate the zip
-- list without clobbering flat_rate_cents or free_shipping_threshold.
UPDATE shipping_config
   SET local_zip_codes = ARRAY[
           '99301', '99302',
           '99320',
           '99323',
           '99336', '99337', '99338',
           '99350',
           '99352', '99354',
           '99353'
       ]
 WHERE local_zip_codes = '{}';

-- +goose Down
ALTER TABLE shipping_config DROP COLUMN IF EXISTS local_zip_codes;
