-- +goose Up

-- Email lookup is an exact match (`WHERE email = $1`), so an address stored with
-- any capitalization is unreachable by a customer who types it in lowercase --
-- and, before domain.NormalizeEmail was applied at the app boundaries, signup
-- could not see an existing row that differed only by case and happily created a
-- duplicate beside it.
--
-- Backfill every stored address to the canonical form the application now
-- produces, then enforce the invariant in the database so the two can never
-- drift apart again.
--
-- PREREQUISITE: any customers whose addresses collide once lowercased must be
-- merged first (see scripts/merge_duplicate_customers.sql). The guard below
-- fails loudly rather than letting CREATE UNIQUE INDEX emit a bare constraint
-- violation with no explanation of what to do about it.

-- +goose StatementBegin
DO $$
DECLARE dupes text;
BEGIN
    SELECT string_agg(norm, ', ') INTO dupes
    FROM (
        SELECT lower(btrim(email)) AS norm
        FROM customers GROUP BY 1 HAVING count(*) > 1
    ) d;

    IF dupes IS NOT NULL THEN
        RAISE EXCEPTION
            'cannot normalize customer emails: % would collide. Merge these accounts first (see merge_duplicate_customers.sql), then re-run.', dupes;
    END IF;
END $$;
-- +goose StatementEnd

UPDATE customers
SET email = lower(btrim(email)), updated_at = now()
WHERE email <> lower(btrim(email));

UPDATE staff
SET email = lower(btrim(email)), updated_at = now()
WHERE email <> lower(btrim(email));

-- Enforce case-insensitive uniqueness. The pre-existing customers_email_key /
-- staff email unique constraints stay: they are implied by these but harmless,
-- and dropping them would be a wider change than this migration needs.
CREATE UNIQUE INDEX idx_customers_email_lower ON customers (lower(email));
CREATE UNIQUE INDEX idx_staff_email_lower     ON staff (lower(email));

-- +goose Down

-- Only the indexes come back off. The original capitalization of each address is
-- not recoverable -- it was not recorded anywhere before being overwritten --
-- and lowercase addresses are valid for every provider, so there is nothing to
-- restore beyond dropping the constraint.
DROP INDEX IF EXISTS idx_customers_email_lower;
DROP INDEX IF EXISTS idx_staff_email_lower;
