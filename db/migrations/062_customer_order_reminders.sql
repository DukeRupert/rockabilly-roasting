-- +goose Up
-- order_reminders_enabled controls whether a customer receives the weekly
-- "place your order before the cutoff" reminder. This replaces the standalone
-- rr service's customer_notifications.email_notify_days flag, which lived in a
-- separate SQLite database keyed by Orderspace customer id.
--
-- Defaults to true: the old service also treated a missing preference row as
-- opted in (COALESCE(..., true)), so every account carries over subscribed.
ALTER TABLE customers
    ADD COLUMN order_reminders_enabled boolean NOT NULL DEFAULT true;

-- The reminder scan is "approved wholesale accounts that ordered recently".
-- The customer side of that filter is narrow enough to index directly; the
-- order-recency half rides the existing idx_orders_channel / placed_at paths.
CREATE INDEX idx_customers_order_reminders
    ON customers (account_type, wholesale_status)
    WHERE order_reminders_enabled;

-- +goose Down
DROP INDEX IF EXISTS idx_customers_order_reminders;
ALTER TABLE customers DROP COLUMN IF EXISTS order_reminders_enabled;
