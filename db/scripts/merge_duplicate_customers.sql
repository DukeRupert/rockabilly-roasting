-- Merge case-differing duplicate customer accounts.
--
-- These pairs exist because customer email lookup was an exact match
-- (`WHERE email = $1`) with no normalization, so signup could not see an
-- existing row that differed only by capitalization. Must be run BEFORE
-- migration 061, which adds a UNIQUE index on lower(email) and will fail
-- while any duplicate remains.
--
-- Strategy: reparent every child row from loser -> winner, then delete the
-- loser. The winner is the row that is already lowercase AND carries the
-- credentials (password / verified / most recent activity), so no customer
-- loses their ability to sign in.
--
-- Run inside one transaction and READ THE VERIFICATION OUTPUT before COMMIT.

BEGIN;

CREATE TEMP TABLE _merge (winner uuid, loser uuid, note text) ON COMMIT DROP;

INSERT INTO _merge (winner, loser, note) VALUES
    -- Dianne Wallace. Both are guest checkouts (no password, unverified), one
    -- order each. Winner is simply the already-lowercase row.
    ('f62791d1-3d58-4a88-9493-2aa3267523f5',
     '2f31d30d-88c2-4081-842f-8ec4053137c8',
     'dswallace1@comcast.net'),

    -- Ruby Navarro. Winner has the password, verified email, and the most
    -- recent order (Jul 23 vs Jul 9). Loser holds 4 orders and a second
    -- Stripe customer, both preserved below.
    ('22b9e1c4-8101-4a79-86d0-c1c71a1d2491',
     'b8b52a4c-138f-4556-9367-df2b7133bf27',
     'rubyyyn28@yahoo.com');

-- Guard: refuse to merge rows that are not actually the same address.
DO $$
DECLARE bad int;
BEGIN
    SELECT count(*) INTO bad
    FROM _merge m
    JOIN customers w ON w.id = m.winner
    JOIN customers l ON l.id = m.loser
    WHERE lower(btrim(w.email)) <> lower(btrim(l.email));
    IF bad > 0 THEN
        RAISE EXCEPTION 'refusing to merge % pair(s) whose emails differ by more than case', bad;
    END IF;
END $$;

-- ---------------------------------------------------------------------------
-- Preserve what the loser row carried that the winner does not.
-- The loser's Stripe customer still exists in Stripe with its own payment
-- methods and history; dropping the ID silently would strand it.
-- ---------------------------------------------------------------------------
UPDATE customers w
SET metadata = w.metadata || jsonb_build_object(
        'merged_from', jsonb_build_object(
            'customer_id',        l.id,
            'email',              l.email,
            'stripe_customer_id', l.stripe_customer_id,
            'created_at',         l.created_at
        )
    ),
    updated_at = now()
FROM _merge m
JOIN customers l ON l.id = m.loser
WHERE w.id = m.winner;

-- Adopt the loser's phone only if the winner has none.
UPDATE customers w
SET phone = l.phone, updated_at = now()
FROM _merge m
JOIN customers l ON l.id = m.loser
WHERE w.id = m.winner
  AND (w.phone IS NULL OR btrim(w.phone) = '')
  AND l.phone IS NOT NULL AND btrim(l.phone) <> '';

-- ---------------------------------------------------------------------------
-- Reparent child rows. Simple FKs first.
-- ---------------------------------------------------------------------------
UPDATE orders             o  SET customer_id = m.winner FROM _merge m WHERE o.customer_id  = m.loser;
UPDATE subscriptions      s  SET customer_id = m.winner FROM _merge m WHERE s.customer_id  = m.loser;
UPDATE addresses          a  SET customer_id = m.winner FROM _merge m WHERE a.customer_id  = m.loser;
UPDATE carts              ct SET customer_id = m.winner FROM _merge m WHERE ct.customer_id = m.loser;
UPDATE coupon_codes       cc SET customer_id = m.winner FROM _merge m WHERE cc.customer_id = m.loser;
UPDATE coupon_codes       cc SET redeemed_by = m.winner FROM _merge m WHERE cc.redeemed_by = m.loser;
UPDATE email_verifications ev SET customer_id = m.winner FROM _merge m WHERE ev.customer_id = m.loser;

-- Tokens minted for the loser address are dropped rather than moved: they were
-- emailed to an address the customer is no longer identified by, and any live
-- one would be a valid credential for the surviving account.
DELETE FROM magic_link_tokens t USING _merge m WHERE t.customer_id = m.loser;

-- ---------------------------------------------------------------------------
-- Composite-PK tables: move only rows the winner does not already have,
-- then drop the remainder so the delete below does not trip.
-- ---------------------------------------------------------------------------
UPDATE customer_group_memberships g SET customer_id = m.winner
FROM _merge m WHERE g.customer_id = m.loser
  AND NOT EXISTS (SELECT 1 FROM customer_group_memberships x
                  WHERE x.customer_id = m.winner AND x.customer_group_id = g.customer_group_id);
DELETE FROM customer_group_memberships g USING _merge m WHERE g.customer_id = m.loser;

UPDATE product_customer_visibility v SET customer_id = m.winner
FROM _merge m WHERE v.customer_id = m.loser
  AND NOT EXISTS (SELECT 1 FROM product_customer_visibility x
                  WHERE x.customer_id = m.winner AND x.product_id = v.product_id);
DELETE FROM product_customer_visibility v USING _merge m WHERE v.customer_id = m.loser;

-- Sessions are not FK-bound to customers (actor_type/actor_id), so clear them
-- explicitly. Any session on the loser row must not survive the merge.
DELETE FROM sessions s USING _merge m
WHERE s.actor_type = 'customer' AND s.actor_id = m.loser;

-- ---------------------------------------------------------------------------
-- Audit, then remove the loser.
-- ---------------------------------------------------------------------------
INSERT INTO audit_log (actor_type, actor_name, action, resource_type, resource_id, metadata)
SELECT 'system', 'customer_merge', 'customer.merged', 'customer', m.winner,
       jsonb_build_object(
           'merged_customer_id', l.id,
           'merged_email',       l.email,
           'reason',             'case-differing duplicate; see NormalizeEmail fix'
       )
FROM _merge m JOIN customers l ON l.id = m.loser;

DELETE FROM customers c USING _merge m WHERE c.id = m.loser;

-- ---------------------------------------------------------------------------
-- Verification -- read before COMMIT.
-- ---------------------------------------------------------------------------
SELECT c.email,
       (SELECT count(*) FROM orders o        WHERE o.customer_id = c.id) AS orders,
       (SELECT count(*) FROM subscriptions s WHERE s.customer_id = c.id) AS subs,
       (SELECT count(*) FROM addresses a     WHERE a.customer_id = c.id) AS addrs,
       c.password_hash IS NOT NULL AS has_pw,
       c.metadata->'merged_from'->>'email' AS absorbed
FROM customers c
WHERE c.id IN (SELECT winner FROM _merge)
ORDER BY c.email;

-- Expect: dswallace1@comcast.net -> 2 orders, 2 addrs
--         rubyyyn28@yahoo.com    -> 7 orders, 2 subs, 4 addrs, has_pw = t

SELECT lower(btrim(email)) AS norm, count(*)
FROM customers GROUP BY 1 HAVING count(*) > 1;
-- Expect: 0 rows. Migration 061 cannot run until this is empty.

COMMIT;
