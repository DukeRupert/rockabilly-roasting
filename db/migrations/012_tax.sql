-- +goose Up
-- Tax fields (tax_exempt, tax_exempt_reason, stripe_tax_id) are already
-- included on the orders table in 006_orders.sql.
--
-- Add an index on stripe_tax_id for lookup by Stripe tax calculation reference.
CREATE INDEX idx_orders_stripe_tax_id ON orders(stripe_tax_id) WHERE stripe_tax_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_orders_stripe_tax_id;
