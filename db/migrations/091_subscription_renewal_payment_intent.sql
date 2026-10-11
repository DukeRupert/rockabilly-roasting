-- +goose Up

-- The charge a renewal has made and not yet written an order for.
--
-- A renewal charges Stripe between two transactions: it reads, it charges,
-- then it writes the order and advances the period. Until the write commits,
-- the money has moved and nothing in the database says so. When the write
-- failed, the next attempt found the subscription still due and charged it
-- again.
--
-- The renewal now writes the intent id here, on every subscription the
-- charge covered, the moment Stripe answers — before the order. The order's
-- transaction clears it. A retry that finds it set finishes that charge
-- instead of making another, or refunds it if the subscription is no longer
-- to be renewed. A row with this set and no renewal claim held is a charge
-- without an order, which is what a sweep would look for.
--
-- A column beside renewal_claimed_at rather than a key in metadata, for the
-- same reason the claim is: it is read and cleared by statements over several
-- subscriptions at once, and cleared by intent id, not by subscription.
ALTER TABLE subscriptions ADD COLUMN renewal_payment_intent_id text;

-- A refund of that charge has begun. Set before the refund is sent and
-- cleared with the intent id once it is done, so a refund whose response was
-- lost is finished on retry rather than mistaken for a charge to write an
-- order on — Stripe still reports a refunded intent as succeeded.
ALTER TABLE subscriptions ADD COLUMN renewal_refunding boolean NOT NULL DEFAULT false;

-- Part of the renewal's idempotency key, raised once by every refund. Stripe
-- keeps a key for 24 hours and replays its first response, which for a
-- refunded charge says succeeded; without this, the charge that follows a
-- refund at the same amount would get the refunded intent back.
ALTER TABLE subscriptions ADD COLUMN renewal_key_generation integer NOT NULL DEFAULT 0;

-- +goose Down

ALTER TABLE subscriptions DROP COLUMN renewal_key_generation;
ALTER TABLE subscriptions DROP COLUMN renewal_refunding;
ALTER TABLE subscriptions DROP COLUMN renewal_payment_intent_id;
