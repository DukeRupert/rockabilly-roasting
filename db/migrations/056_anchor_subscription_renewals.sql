-- +goose Up

-- Subscriptions historically renewed at each subscriber's signup time-of-day, so
-- renewal orders trickled in across the whole day. Going forward they anchor to
-- 02:00 merchant-local (America/Los_Angeles) — see app.anchorRenewalTime, wired
-- via RENEWAL_ANCHOR_HOUR. This one-time backfill snaps every still-live
-- subscription's next_order_at forward to the next 02:00 local at or after its
-- current value, so the existing base joins the morning batch immediately rather
-- than converging over up to 90 days of natural renewals.
--
-- Forward-only: no subscription is ever moved earlier, so none is charged before
-- its period truly elapses (at most ~1 day later, once). If you run a different
-- MERCHANT_TIMEZONE / RENEWAL_ANCHOR_HOUR than the defaults below, adjust the
-- zone and hour here to match before applying.
UPDATE subscriptions
SET next_order_at = CASE
        WHEN (date_trunc('day', next_order_at AT TIME ZONE 'America/Los_Angeles')
                  + interval '2 hours') AT TIME ZONE 'America/Los_Angeles' >= next_order_at
        THEN (date_trunc('day', next_order_at AT TIME ZONE 'America/Los_Angeles')
                  + interval '2 hours') AT TIME ZONE 'America/Los_Angeles'
        ELSE (date_trunc('day', next_order_at AT TIME ZONE 'America/Los_Angeles')
                  + interval '1 day' + interval '2 hours') AT TIME ZONE 'America/Los_Angeles'
    END
WHERE status IN ('active', 'past_due');

-- +goose Down

-- Irreversible data backfill — the original signup-time next_order_at values are
-- not retained. Nothing to undo; each subscription re-anchors on its next
-- renewal regardless of this migration.
SELECT 1;
