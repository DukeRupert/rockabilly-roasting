-- +goose Up

-- Customer: payment terms and billing method for wholesale invoicing
ALTER TABLE customers ADD COLUMN payment_terms_days int;
ALTER TABLE customers ADD COLUMN billing_method text NOT NULL DEFAULT 'manual';

-- Order: shipping method and requested delivery date for wholesale operations
ALTER TABLE orders ADD COLUMN shipping_method text;
ALTER TABLE orders ADD COLUMN requested_delivery_date timestamptz;

-- Subscription: promote stripe_payment_method_id from metadata to proper column
ALTER TABLE subscriptions ADD COLUMN stripe_payment_method_id text;

-- +goose Down
ALTER TABLE subscriptions DROP COLUMN stripe_payment_method_id;
ALTER TABLE orders DROP COLUMN requested_delivery_date;
ALTER TABLE orders DROP COLUMN shipping_method;
ALTER TABLE customers DROP COLUMN billing_method;
ALTER TABLE customers DROP COLUMN payment_terms_days;
