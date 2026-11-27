-- +goose Up

-- Every coupon code was single-use: the first paid order spent it, for
-- everyone. A promotion code handed out to a whole list (WELCOME50) needs the
-- other shape: any number of customers, each once.
--
-- Set when the discount is created and not editable afterwards, like the code
-- itself. Switching a live discount between the two would strand the
-- redemptions recorded under the old rule.
ALTER TABLE discounts
    ADD COLUMN usage_limit text NOT NULL DEFAULT 'single_use'
        CONSTRAINT discounts_usage_limit_check
        CHECK (usage_limit IN ('single_use', 'once_per_customer'));

-- One row per paid order that used a once-per-customer discount. Single-use
-- codes keep recording their one redemption on coupon_codes itself.
--
-- Keyed on the discount rather than the code, so a customer cannot take one
-- offer twice by way of two codes for it (per-member referral codes will all
-- hang off one discount). order_id is unique so a capture that runs twice for
-- the same order finds its own row rather than a second one.
CREATE TABLE coupon_redemptions (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    discount_id    uuid NOT NULL REFERENCES discounts(id) ON DELETE CASCADE,
    coupon_code_id uuid NOT NULL REFERENCES coupon_codes(id) ON DELETE CASCADE,
    customer_id    uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    order_id       uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    redeemed_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT coupon_redemptions_discount_customer_key UNIQUE (discount_id, customer_id),
    CONSTRAINT coupon_redemptions_order_key UNIQUE (order_id)
);

CREATE INDEX idx_coupon_redemptions_coupon_code_id ON coupon_redemptions(coupon_code_id);

-- +goose Down
DROP TABLE IF EXISTS coupon_redemptions;
ALTER TABLE discounts DROP COLUMN IF EXISTS usage_limit;
