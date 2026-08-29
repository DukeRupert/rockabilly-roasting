-- +goose Up

-- Which QuickBooks items wholesale invoices bill against.
--
-- These were environment variables, and they were the wrong two to put there.
-- The rest of the QB_* set is credentials or deployment identity — a client
-- secret, an encryption key, a redirect URI — and belongs in the environment.
-- These are a business mapping: which income account the shop's coffee revenue
-- lands in. Only the bookkeeper can answer it, the answer is specific to the
-- company that happens to be connected, and getting it wrong misstates a P&L
-- rather than breaking anything visibly.
--
-- As environment variables they also created an ordering problem with no good
-- answer. The binary refused to boot without QB_SALES_ITEM_ID, but a valid ID
-- cannot be known until someone has connected and queried that company's
-- items — so standing an instance up meant guessing a value, connecting,
-- looking up the real ID, and redeploying. During that window invoices would
-- have billed against whatever the guess happened to name.
--
-- Held here rather than in a settings table of their own because they are two
-- scalars on a singleton row, exactly like tax_mode beside them. The
-- environment variables still work as a fallback so existing deployments keep
-- billing across this migration; the column wins when set.
ALTER TABLE store_settings
    ADD COLUMN qb_sales_item_id text NOT NULL DEFAULT '',
    ADD COLUMN qb_sales_item_name text NOT NULL DEFAULT '',
    ADD COLUMN qb_shipping_item_id text NOT NULL DEFAULT '',
    ADD COLUMN qb_shipping_item_name text NOT NULL DEFAULT '';

COMMENT ON COLUMN store_settings.qb_sales_item_name IS
    'Display name of the sales item, cached from QuickBooks so the settings page can name the current choice without a live API call. Advisory only — the ID is what bills.';

-- +goose Down

ALTER TABLE store_settings
    DROP COLUMN IF EXISTS qb_sales_item_id,
    DROP COLUMN IF EXISTS qb_sales_item_name,
    DROP COLUMN IF EXISTS qb_shipping_item_id,
    DROP COLUMN IF EXISTS qb_shipping_item_name;
