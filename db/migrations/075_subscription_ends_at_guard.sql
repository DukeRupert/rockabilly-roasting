-- +goose Up
-- +goose StatementBegin

-- Forbid the state that stopped three subscriptions billing without anyone
-- noticing: a status the renewal scheduler treats as live, alongside an
-- ends_at that makes it unselectable.
--
-- ListSubscriptionsDueForRenewal reads
--   status IN ('active','past_due') AND next_order_at <= now()
--     AND (ends_at IS NULL OR ends_at > now())
-- Status is a badge and next_order_at is a date on the page; ends_at is
-- neither, so the combination reads as healthy while silently earning nothing.
-- Jon Law, Stephanie Maier and Kelly Burnett sat like that for one to three
-- months each. Nothing failed, nothing retried, nothing logged.
--
-- The check is on status rather than on "ends_at is in the past", because a
-- CHECK constraint may only call IMMUTABLE functions and now() is not one.
-- That makes this stricter than the bug strictly requires: it also refuses an
-- active subscription whose ends_at is still in the future.
--
-- That strictness is deliberate. A future ends_at was the original fixed-term
-- feature (migration 032, for the WooCommerce importer's fixed-length plans),
-- and that feature is half-built: ends_at excludes a subscription from renewal
-- the moment it passes, but nothing anywhere transitions the row to 'expired'
-- when it does. A fixed-term subscription therefore does not end — it becomes
-- this exact bug on a timer. Re-enabling fixed terms needs a sweep that moves
-- status to 'expired' when ends_at passes; until that exists, refusing the
-- combination is the honest position.
--
-- paused, cancelled and expired keep ends_at freely: none of them is selected
-- for renewal, so the column is a record rather than a trap. Eight rows in
-- production rely on that. ResumeSubscription clears ends_at before flipping a
-- paused subscription back to active, in that order, because this constraint is
-- evaluated per statement rather than at commit.
ALTER TABLE subscriptions
    ADD CONSTRAINT subscriptions_live_status_has_no_end_date
    CHECK (ends_at IS NULL OR status NOT IN ('active', 'past_due'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE subscriptions
    DROP CONSTRAINT subscriptions_live_status_has_no_end_date;
-- +goose StatementEnd
