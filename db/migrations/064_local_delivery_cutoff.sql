-- +goose Up
-- Structured local-delivery schedule, so the van's route is data rather than prose.
--
-- Migration 041 shipped local_delivery_days as free text ("Mondays and
-- Thursdays") purely to print in checkout copy and the out-for-delivery email.
-- Nothing could read it, so nothing could answer the only question a customer
-- actually asks: *which* Monday — this one, or next week's? These two columns
-- make the schedule computable.
--
-- local_delivery_weekdays uses Go's time.Weekday numbering (Sunday = 0), not
-- ISO-8601's (Monday = 1), because every consumer is Go and a translation layer
-- at the store boundary is one more place to invert a day. The default {1,4} is
-- Monday and Thursday — the client's current route, and the same schedule the
-- old free-text default described.
--
-- The cutoff is minutes-past-midnight rather than a `time` column so it stays a
-- plain int through sqlc and the whole comparison is integer arithmetic against
-- a wall-clock offset. 540 = 09:00. Half-hour cutoffs work without a schema
-- change if the client ever moves it.
--
-- Both are evaluated in MERCHANT_TIMEZONE (America/Los_Angeles — the local zone
-- is the Tri-Cities, WA). There is deliberately no timezone column here: a
-- single-merchant platform has one clock, and a second source of truth for it
-- would silently disagree with the renewal anchor and the reminder schedule.
ALTER TABLE shipping_config
    ADD COLUMN local_delivery_weekdays       smallint[] NOT NULL DEFAULT '{1,4}',
    ADD COLUMN local_delivery_cutoff_minutes integer    NOT NULL DEFAULT 540;

ALTER TABLE shipping_config
    ADD CONSTRAINT shipping_config_cutoff_minutes_range
        CHECK (local_delivery_cutoff_minutes BETWEEN 0 AND 1439);

-- local_delivery_days is left in place but is no longer read by application
-- code — the display label is now derived from local_delivery_weekdays so the
-- prose can never drift from the schedule the cutoff math uses. It is kept for
-- one release so a merchant who typed custom phrasing can have it recovered;
-- drop it once production is confirmed to be on the derived label.

-- The promised delivery date, frozen onto the order at placement time.
--
-- Stored rather than recomputed on read: it is a promise made in the
-- confirmation email, and editing the schedule in admin settings next month
-- must not retroactively rewrite what a customer was already told. A `date`
-- (not timestamptz) because the commitment is "Thursday", not an instant —
-- the van leaves when it leaves.
--
-- NULL for every non-local-delivery order, and for local-delivery orders placed
-- before this migration. Nothing backfills: those orders have already been
-- delivered or already had their date communicated by hand.
--
-- Distinct from the pre-existing requested_delivery_date, which is an imported
-- Orderspace/WooCommerce field recording what the *customer asked for* on a
-- legacy order. This column is what the shop *committed to*. Collapsing them
-- would make imported history indistinguishable from live promises.
ALTER TABLE orders
    ADD COLUMN scheduled_delivery_date date;

-- The fulfillment queue and load list both filter local delivery by date; this
-- keeps that ordered scan off a seq scan as order volume grows. Partial, since
-- the column is NULL for the large majority of orders.
CREATE INDEX idx_orders_scheduled_delivery_date
    ON orders (scheduled_delivery_date)
    WHERE scheduled_delivery_date IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_orders_scheduled_delivery_date;
ALTER TABLE orders DROP COLUMN IF EXISTS scheduled_delivery_date;

ALTER TABLE shipping_config
    DROP CONSTRAINT IF EXISTS shipping_config_cutoff_minutes_range;
ALTER TABLE shipping_config
    DROP COLUMN IF EXISTS local_delivery_weekdays,
    DROP COLUMN IF EXISTS local_delivery_cutoff_minutes;
