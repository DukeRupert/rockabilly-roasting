-- +goose Up

-- Which kind of order a discount's code works on. Every code so far has been a
-- cart-checkout code, so that is the default. 'subscription_signup' codes work
-- only on the first order of a new subscription (WELCOME50, and the referral
-- offer to come); renewals are priced from the plan and never see a coupon.
-- Editable: nothing is recorded against it.
ALTER TABLE discounts
    ADD COLUMN applies_to text NOT NULL DEFAULT 'one_time'
        CONSTRAINT discounts_applies_to_check
        CHECK (applies_to IN ('one_time', 'subscription_signup'));

-- 'new_subscribers': customers who have never held a subscription, in any
-- status. WELCOME50 is for people joining, not for members coming back.
ALTER TABLE discounts DROP CONSTRAINT discounts_eligibility_check;
ALTER TABLE discounts ADD CONSTRAINT discounts_eligibility_check
    CHECK (eligibility IN ('anyone', 'subscribers', 'new_subscribers'));

-- +goose Down
-- Collapsing these would turn WELCOME50 into a 50%-off code anyone could use
-- at cart checkout. A rollback cannot keep the restriction, so it switches
-- those codes off first instead of widening them.
UPDATE discounts SET active = false, updated_at = now()
WHERE applies_to <> 'one_time' OR eligibility = 'new_subscribers';
ALTER TABLE discounts DROP CONSTRAINT discounts_eligibility_check;
UPDATE discounts SET eligibility = 'anyone' WHERE eligibility = 'new_subscribers';
ALTER TABLE discounts ADD CONSTRAINT discounts_eligibility_check
    CHECK (eligibility IN ('anyone', 'subscribers'));
ALTER TABLE discounts DROP COLUMN IF EXISTS applies_to;
