-- +goose Up
ALTER TABLE orders ADD COLUMN stripe_payment_intent_id text;
CREATE INDEX idx_orders_stripe_payment_intent_id ON orders (stripe_payment_intent_id) WHERE stripe_payment_intent_id IS NOT NULL;

ALTER TABLE customers ADD COLUMN stripe_customer_id text;
CREATE UNIQUE INDEX idx_customers_stripe_customer_id ON customers (stripe_customer_id) WHERE stripe_customer_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_customers_stripe_customer_id;
ALTER TABLE customers DROP COLUMN IF EXISTS stripe_customer_id;

DROP INDEX IF EXISTS idx_orders_stripe_payment_intent_id;
ALTER TABLE orders DROP COLUMN IF EXISTS stripe_payment_intent_id;
