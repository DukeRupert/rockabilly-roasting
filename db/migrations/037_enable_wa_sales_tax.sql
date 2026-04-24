-- +goose Up
-- Enable flat-rate WA Sales Tax for B2C.
-- 8.8% = 6.5% WA state + 2.3% local (Kennewick).
-- Wholesale orders remain exempt (hardcoded in app layer).
UPDATE store_settings
   SET tax_mode   = 'flat_rate',
       tax_rate   = 0.0880,
       tax_label  = 'WA Sales Tax',
       updated_at = now()
 WHERE id = true;

-- Default the existing catalog to tax-exempt. Every product today is bagged
-- coffee, which falls under WA's food-for-home-consumption exemption.
-- Staff must flip tax_exempt = false on any future taxable SKUs (merch,
-- brewing equipment, ready-to-drink).
UPDATE products SET tax_exempt = true;

-- +goose Down
UPDATE store_settings
   SET tax_mode   = 'none',
       tax_rate   = NULL,
       tax_label  = NULL,
       updated_at = now()
 WHERE id = true;

UPDATE products SET tax_exempt = false;
