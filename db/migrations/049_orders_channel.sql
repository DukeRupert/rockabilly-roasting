-- +goose Up
-- channel records the sales channel an order was placed through, frozen at
-- placement time. It is NOT derived live from the customer's account_type so an
-- order stays in its original channel even if the customer is later converted,
-- suspended, or deleted. Retail is the default; wholesale is set explicitly in
-- PlaceWholesaleOrder.
ALTER TABLE orders
    ADD COLUMN channel text NOT NULL DEFAULT 'retail'
        CHECK (channel IN ('retail', 'wholesale'));

-- Backfill: classify existing orders from their customer's account_type. Orders
-- with no customer (guest/draft) keep the 'retail' default.
UPDATE orders o
SET channel = 'wholesale'
FROM customers c
WHERE o.customer_id = c.id
  AND c.account_type = 'wholesale';

CREATE INDEX idx_orders_channel ON orders (channel);

-- +goose Down
DROP INDEX IF EXISTS idx_orders_channel;
ALTER TABLE orders DROP COLUMN IF EXISTS channel;
