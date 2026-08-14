-- +goose Up
-- Volume price tiers: quantity breaks on wholesale price lists.
--
-- No new columns. Migration 005 created prices.min_quantity and
-- prices.max_quantity and nothing has ever written them — every query in
-- db/queries/pricing.sql filters `min_quantity IS NULL`. This migration turns
-- those dormant columns into a working feature by constraining what may be
-- stored in them, so the shape the application relies on is guaranteed by the
-- database rather than by convention.
--
-- A ladder is a set of rows sharing a (price_set_id, currency_code,
-- price_list_id) triple: one base rung with min_quantity NULL, plus one row per
-- volume break. Pricing is all-units — reaching a break reprices the whole
-- line, not just the units above the threshold.

-- Tiers are open-ended: each rung runs until the next one starts, so a rung
-- needs a floor and nothing else. Storing an upper bound as well would admit
-- two failure modes that have no correct answer at read time — a gap (11 units
-- priced by no rung) and an overlap (11 units priced by two). Forbidding the
-- column outright makes both unrepresentable, which is what lets the resolver
-- promise that every quantity has exactly one price.
--
-- The column is kept rather than dropped: it is part of the price_sets shape
-- inherited from migration 005, dropping it would churn every sqlc-generated
-- struct that reads a price row, and a CHECK expresses the intent more
-- precisely than absence would.
ALTER TABLE prices
    ADD CONSTRAINT prices_max_quantity_unused
        CHECK (max_quantity IS NULL);

-- min_quantity NULL means the base rung. A stored 1 would mean the same thing
-- by a different spelling, and two spellings for one concept is how a ladder
-- ends up with two base rungs that disagree. Breaks therefore start at 2.
ALTER TABLE prices
    ADD CONSTRAINT prices_min_quantity_is_a_break
        CHECK (min_quantity IS NULL OR min_quantity >= 2);

-- One price per threshold. Without this, authoring a tier twice at the same
-- quantity leaves the ladder's price at that quantity dependent on row order —
-- which is to say, arbitrary. Partial because it constrains only tier rows:
-- base prices (min_quantity NULL) and base-list prices (price_list_id NULL)
-- are governed by the existing delete-then-insert paths and must stay outside
-- this index, since NULLs would not collide anyway.
--
-- Tiers are deliberately confined to price lists. Base prices stay single-rung,
-- which is the firewall keeping the retail storefront, the Svelte checkout,
-- subscriptions, and renewal pricing on exactly the behavior they have today —
-- every one of those paths reads base prices, so a base price that cannot
-- become a ladder is a base price that cannot change under them.
--
-- This index also serves ladder reads, which look up by the same leading
-- columns. It is the only index the feature needs.
CREATE UNIQUE INDEX idx_prices_tier
    ON prices (price_set_id, currency_code, price_list_id, min_quantity)
    WHERE price_list_id IS NOT NULL AND min_quantity IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_prices_tier;
ALTER TABLE prices DROP CONSTRAINT IF EXISTS prices_min_quantity_is_a_break;
ALTER TABLE prices DROP CONSTRAINT IF EXISTS prices_max_quantity_unused;
