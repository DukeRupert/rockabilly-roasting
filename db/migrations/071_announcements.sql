-- +goose Up

-- Announcements are staff-composed one-off notices sent to a whole audience —
-- "Labor Day pushes Monday's shipment to Tuesday", a price change, a closure.
-- They generalize the wholesale-only one-off notice (which could only reach the
-- weekly reminder list) to retail, wholesale, or both.
--
-- The row exists so a send can be *scheduled* and, until it fires, cancelled.
-- Cancellation flips status here rather than deleting a River job: the job is
-- the dispatcher, and it re-reads this row before fanning anything out, so one
-- UPDATE is the whole cancel path and there is no window where the queue and
-- the admin disagree about whether mail is going out.
CREATE TABLE announcements (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subject      text NOT NULL,
    -- Plain text. Blank lines separate paragraphs; bare URLs are linkified at
    -- render time. Never HTML — staff input must not reach the mail body as
    -- markup, which is exactly what the old rr /send-adhoc endpoint allowed.
    body         text NOT NULL,
    audience     text NOT NULL CHECK (audience IN ('all', 'retail', 'wholesale')),
    status       text NOT NULL DEFAULT 'scheduled'
                 CHECK (status IN ('scheduled', 'sending', 'sent', 'cancelled')),
    -- Always set, even for "send now" (which stores now()). One column means the
    -- dispatcher, the list ordering, and the cancel window all read the same
    -- field instead of branching on a nullable one.
    scheduled_at timestamptz NOT NULL,
    sent_at      timestamptz,
    -- Filled in by the dispatcher from the audience it actually resolved, not
    -- from what the compose screen predicted — the two can differ if an account
    -- was suspended or opted out in between.
    recipient_count integer,
    -- Denormalized so the list still says who sent it after the staff member
    -- leaves and their row is removed.
    created_by      uuid REFERENCES staff(id) ON DELETE SET NULL,
    created_by_name text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- The admin list is "newest first", and the dispatcher only ever looks one row
-- up by id, so this single index covers the page.
CREATE INDEX idx_announcements_scheduled_at ON announcements (scheduled_at DESC);

-- Announcements get their own opt-out, separate from the weekly wholesale
-- reminder (customers.order_reminders_enabled, migration 062).
--
-- They are different subscriptions and collapsing them would be wrong in both
-- directions: a wholesale buyer who mutes the weekly "time to order" nudge
-- still needs to hear that the holiday moved their delivery, and a retail
-- customer has no weekly reminder to opt out of but must still get a working
-- unsubscribe link on anything that isn't strictly transactional.
--
-- Defaults true, matching the reminder flag: subscribed until the recipient
-- says otherwise, via the link in the mail itself.
ALTER TABLE customers ADD COLUMN announcements_enabled boolean NOT NULL DEFAULT true;

-- Same flag for an invited teammate, so an opt-out silences the address that
-- clicked and nobody else on the account.
ALTER TABLE customer_users ADD COLUMN announcements_enabled boolean NOT NULL DEFAULT true;

-- +goose Down

ALTER TABLE customer_users DROP COLUMN announcements_enabled;
ALTER TABLE customers DROP COLUMN announcements_enabled;
DROP INDEX IF EXISTS idx_announcements_scheduled_at;
DROP TABLE IF EXISTS announcements;
