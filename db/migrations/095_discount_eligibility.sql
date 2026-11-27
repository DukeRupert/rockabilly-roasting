-- +goose Up

-- Who a discount is for. 'subscribers' limits it to signed-in customers with
-- an active subscription (GIFT20: a members' holiday offer on one-time
-- orders). Editable: unlike usage_limit, nothing is recorded against it.
ALTER TABLE discounts
    ADD COLUMN eligibility text NOT NULL DEFAULT 'anyone'
        CONSTRAINT discounts_eligibility_check
        CHECK (eligibility IN ('anyone', 'subscribers'));

-- +goose Down
-- Dropping the column would make every members-only code (GIFT20) work for
-- anyone. A rollback cannot keep the restriction, so it switches those codes
-- off instead of widening them. Re-migrating brings the column back as
-- 'anyone': set each code's restriction again before turning it back on.
UPDATE discounts SET active = false, updated_at = now() WHERE eligibility <> 'anyone';
ALTER TABLE discounts DROP COLUMN IF EXISTS eligibility;
