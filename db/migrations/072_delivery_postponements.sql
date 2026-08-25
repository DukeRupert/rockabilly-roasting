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

-- +goose Down
DROP TABLE IF EXISTS delivery_postponements;
