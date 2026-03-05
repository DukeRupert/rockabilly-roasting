-- +goose Up

-- Add subscribable flag to products (coffee yes, tshirts no).
ALTER TABLE products ADD COLUMN subscribable boolean NOT NULL DEFAULT false;

-- Add discount_pct to subscription_plans (subscriber savings, e.g. 15).
ALTER TABLE subscription_plans ADD COLUMN discount_pct int NOT NULL DEFAULT 0;

-- Move variant_id and price_set_id from plans to subscriptions.
ALTER TABLE subscriptions ADD COLUMN variant_id uuid REFERENCES variants(id) ON DELETE RESTRICT;

-- Backfill variant_id on existing subscriptions from their plan.
UPDATE subscriptions s
SET variant_id = sp.variant_id
FROM subscription_plans sp
WHERE s.plan_id = sp.id;

-- Now make variant_id NOT NULL.
ALTER TABLE subscriptions ALTER COLUMN variant_id SET NOT NULL;

-- Drop variant_id and price_set_id from plans.
ALTER TABLE subscription_plans DROP COLUMN variant_id;
ALTER TABLE subscription_plans DROP COLUMN price_set_id;

-- +goose Down

-- Re-add columns to plans (nullable for rollback).
ALTER TABLE subscription_plans ADD COLUMN variant_id uuid REFERENCES variants(id) ON DELETE RESTRICT;
ALTER TABLE subscription_plans ADD COLUMN price_set_id uuid REFERENCES price_sets(id) ON DELETE RESTRICT;

ALTER TABLE subscriptions DROP COLUMN variant_id;
ALTER TABLE subscription_plans DROP COLUMN discount_pct;
ALTER TABLE products DROP COLUMN subscribable;
