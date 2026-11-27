-- +goose Up

-- Who a discount is for. 'subscribers' limits it to signed-in customers with a
-- active subscription (GIFT20: an active members' holiday
-- offer on one-time orders). Editable: unlike usage_limit, nothing is
-- recorded against it.
ALTER TABLE discounts
    ADD COLUMN eligibility text NOT NULL DEFAULT 'anyone'
        CONSTRAINT discounts_eligibility_check
        CHECK (eligibility IN ('anyone', 'subscribers'));

-- +goose Down
ALTER TABLE discounts DROP COLUMN IF EXISTS eligibility;
