-- +goose Up

-- Store-wide default price list for wholesale accounts. When a wholesale customer
-- has no price list explicitly assigned (customers.price_list_id IS NULL), prices
-- resolve against this default instead of base prices. NULL means "no default —
-- fall back to base prices" (the prior behavior). ON DELETE SET NULL so removing
-- a price list quietly clears the default rather than blocking the delete.
ALTER TABLE store_settings
    ADD COLUMN default_wholesale_price_list_id uuid
        REFERENCES price_lists(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE store_settings DROP COLUMN IF EXISTS default_wholesale_price_list_id;
