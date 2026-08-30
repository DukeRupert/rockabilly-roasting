-- +goose Up

-- Drop an index that serves no query.
--
-- 083 added service_time_entries_uncosted_idx claiming it kept "which of these
-- are uncosted" cheap. It does not: every rate_cents IS NULL read in the code
-- is an aggregate FILTER inside a SUM over a ticket's or a scope's entries,
-- which a partial index on ticket_id cannot serve, and the one per-render
-- existence check (AnyCostedTime) tests the complement — IS NOT NULL.
--
-- So it costs a write on every logged hour and is never read. Removing it is
-- cheaper than leaving a false justification in the schema for somebody to
-- reason from later.
DROP INDEX IF EXISTS service_time_entries_uncosted_idx;

-- +goose Down

CREATE INDEX service_time_entries_uncosted_idx
    ON service_time_entries (ticket_id) WHERE rate_cents IS NULL;
