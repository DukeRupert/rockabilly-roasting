-- +goose Up
-- Postponed delivery runs, so a holiday can move the van instead of silently
-- promising a day the shop is closed.
--
-- The schedule added in migration 064 is purely a weekday rule: the van runs
-- Mondays and Thursdays, and NextDeliveryDate rolls forward to the next such
-- weekday. It has no notion of a calendar, so on Labor Day it will quote
-- Monday delivery at checkout, promise it in the confirmation email, and show
-- it on the dashboard cutoff strip, with no way for staff to say "that run
-- happens Tuesday this week".
--
-- A row here says exactly that: the run scheduled for original_date actually
-- happens on moved_to_date. It is a per-date exception, not a recurring holiday
-- calendar — staff mark the handful of days the shop observes each year, which
-- keeps the shop's actual practice (some holidays it runs, some it doesn't) out
-- of a hardcoded federal-holiday list that would be wrong for this business.
--
-- original_date is the primary key: a given run can only move once, and marking
-- the same day twice should replace the first answer rather than create an
-- ambiguity about which one the van follows.
CREATE TABLE delivery_postponements (
    original_date date PRIMARY KEY,
    moved_to_date date NOT NULL,
    -- Why the run moved. Shown to staff in admin settings; never customer-facing
    -- (customers see a date, and a moved holiday is announced through the
    -- Announcements composer if the shop wants to say more).
    note       text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),

    -- Forward-only. Moving a run earlier would mean the van leaves before some
    -- orders were told it would, and the cutoff arithmetic assumes a postponed
    -- run is never sooner than the day it was scheduled for.
    CONSTRAINT delivery_postponements_moves_forward
        CHECK (moved_to_date > original_date),

    -- Bounded so NextDeliveryDate's backwards scan can be a fixed window. A run
    -- postponed further than a fortnight is not a postponement — the shop has
    -- changed its schedule, and that belongs in local_delivery_weekdays.
    CONSTRAINT delivery_postponements_within_two_weeks
        CHECK (moved_to_date <= original_date + 14)
);

-- Dates are read as a whole set on every config load (there are only ever a
-- handful of rows — a working year has maybe six observed holidays), so no
-- index beyond the primary key is warranted.

-- Which run an order belongs to, as distinct from when that run goes out.
--
-- scheduled_delivery_date is the day the customer was promised, and once a run
-- is postponed it stops being able to identify the run: a Monday moved onto
-- Thursday leaves its orders sharing a date with Thursday's own, and nothing
-- can afterwards say which were which. Restoring the Monday then drags
-- Thursday's orders back to a Monday they were never on — which, if that
-- Monday is the holiday the shop was closed for, points the van's paperwork at
-- a shut day. That is the exact failure the postponement feature exists to
-- prevent.
--
-- This column is the identity: the scheduled weekday the order rides,
-- unaffected by any postponement. Postpone and restore match on it, so they
-- move exactly the orders that belong to the run being moved.
--
-- Backfilled from scheduled_delivery_date because no postponement exists yet —
-- every historical local-delivery order rode the day it was promised.
ALTER TABLE orders
    ADD COLUMN delivery_run_date date;

UPDATE orders
   SET delivery_run_date = scheduled_delivery_date
 WHERE scheduled_delivery_date IS NOT NULL;

-- Postpone and restore both filter on this column. Partial for the same reason
-- the scheduled_delivery_date index is: NULL for the large majority of orders.
CREATE INDEX idx_orders_delivery_run_date
    ON orders (delivery_run_date)
    WHERE delivery_run_date IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_orders_delivery_run_date;
ALTER TABLE orders DROP COLUMN IF EXISTS delivery_run_date;
DROP TABLE IF EXISTS delivery_postponements;
