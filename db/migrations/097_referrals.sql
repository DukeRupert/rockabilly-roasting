-- +goose Up

-- The referral offer: one discount staff mark as the one members share. Each
-- member gets their own code on it (coupon_codes.customer_id = the member who
-- owns it), so the code says who referred the friend.
ALTER TABLE discounts ADD COLUMN is_referral boolean NOT NULL DEFAULT false;

-- At most one referral offer at a time: a member's code has to mean one thing.
CREATE UNIQUE INDEX discounts_one_referral_offer ON discounts (is_referral) WHERE is_referral;

-- One code per member per discount, and the lookup for it. Unique so two page
-- loads racing to mint a member's code cannot both succeed.
CREATE UNIQUE INDEX idx_coupon_codes_discount_customer ON coupon_codes (discount_id, customer_id)
    WHERE customer_id IS NOT NULL;

-- A free bag a member earned: one row per paid order placed with their code.
-- Written at capture, next to the redemption; voided if that order is
-- cancelled before the bag goes out. Staff work the 'owed' rows from the
-- admin and mark each one sent.
CREATE TABLE referral_rewards (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    referrer_customer_id  uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    referred_customer_id  uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    coupon_code_id        uuid NOT NULL REFERENCES coupon_codes(id) ON DELETE CASCADE,
    order_id              uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    status                text NOT NULL DEFAULT 'owed'
        CONSTRAINT referral_rewards_status_check
        CHECK (status IN ('owed', 'sent', 'void')),
    sent_at               timestamptz,
    -- Who marked it sent: an actor id, unreferenced like audit_log.actor_id,
    -- so a removed staff member's history stays as it was.
    sent_by               uuid,
    created_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT referral_rewards_order_key UNIQUE (order_id)
);

CREATE INDEX idx_referral_rewards_status ON referral_rewards (status, created_at);
CREATE INDEX idx_referral_rewards_referrer ON referral_rewards (referrer_customer_id);

-- +goose Down
DROP TABLE IF EXISTS referral_rewards;
DROP INDEX IF EXISTS idx_coupon_codes_discount_customer;
DROP INDEX IF EXISTS discounts_one_referral_offer;
-- Members' codes stay as ordinary codes on the discount once is_referral is
-- gone, and nothing would stop a member using their own. Switch the offer off
-- rather than leave that open; set it up again before turning it back on.
UPDATE discounts SET active = false, updated_at = now() WHERE is_referral;
ALTER TABLE discounts DROP COLUMN IF EXISTS is_referral;
