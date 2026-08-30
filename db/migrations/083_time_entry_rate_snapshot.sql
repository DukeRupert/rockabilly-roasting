-- +goose Up

-- Snapshot the hourly cost onto each time entry, so changing the shop's rate
-- stops rewriting what past work cost.
--
-- Until now the cost reports multiplied minutes by whatever rate was set at the
-- moment somebody opened the page. Raising the rate in March silently made last
-- August more expensive, which is the opposite of what a cost record is for:
-- the hour was bought at the rate of the day, and that is a fact about the past.
--
-- The rate is stamped when the entry is written and never touched again by a
-- settings change. It stays editable per entry — a rate set late, or set wrong,
-- has to be fixable — but only deliberately, one row at a time.
ALTER TABLE service_time_entries
    ADD COLUMN rate_cents integer CHECK (rate_cents >= 0);

COMMENT ON COLUMN service_time_entries.rate_cents IS
    'Hourly cost this entry was booked at, in cents, captured when it was written. NULL = uncosted (logged before any rate existed); the reports count its minutes and none of its money.';

-- +goose StatementBegin
-- Backfill at the rate the reports are *currently* using, so this migration
-- changes no number anybody can see. Labour takes the labour rate and travel
-- takes the travel rate with the same fallback to labour the application
-- applies; a shop with no rate set gets NULL everywhere, which is exactly what
-- its reports already show.
--
-- After this the columns diverge on purpose: store_settings holds what the next
-- hour will cost, and these hold what past hours did.
UPDATE service_time_entries e
SET rate_cents = CASE e.kind
        WHEN 'travel' THEN COALESCE(s.service_travel_rate_cents, s.service_labor_rate_cents)
        ELSE s.service_labor_rate_cents
    END
FROM store_settings s
WHERE s.id = true
  AND s.service_labor_rate_cents IS NOT NULL;
-- +goose StatementEnd

-- No index on rate_cents. Every read of it is an aggregate FILTER inside a SUM
-- over one ticket's or one scope's entries, which a partial index cannot serve,
-- and the one existence check (AnyCostedTime) tests the complement. An index
-- here would cost a write on every logged hour and never be read.

-- +goose Down

DROP INDEX IF EXISTS service_time_entries_uncosted_idx;
ALTER TABLE service_time_entries DROP COLUMN IF EXISTS rate_cents;
