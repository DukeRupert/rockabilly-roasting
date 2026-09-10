-- +goose Up
-- Collapse the two spellings of "this order goes out by carrier" into one.
--
-- shipping_method has always had two values meaning the same thing: 'shipped',
-- and NULL. NULL is not corruption — resolveLocalMethod (web/checkout.go) returns
-- nil for any address outside the local zone, and its own comment calls that
-- "standard shipped downstream". Migration 033 added the column nullable with no
-- default and no backfill, so every ordinary retail mail-out has been written
-- NULL ever since: 267 rows at the time of writing, still accruing ~50/month.
--
-- Two spellings mean every read site decides for itself, and they disagreed.
-- CalculateForMethod and canBuyLabel treat NULL as shipped; GroupRowsByShippingMethod
-- files it under its own "No shipping method" heading (which loses the bulk
-- buy-labels action for the bulk of retail volume); and hasShippingDetails ignored
-- it entirely, which is how the convert-to-local control shipped in v1.118.0
-- invisible to most of the orders it was built for. That last one was a caught bug;
-- the point of this migration is that it was a whole class, not an incident.
--
-- After this, NULL is unrepresentable and the Go type is a value rather than a
-- pointer, so the compiler refuses the next version of that mistake.

UPDATE orders SET shipping_method = 'shipped' WHERE shipping_method IS NULL;

ALTER TABLE orders
    ALTER COLUMN shipping_method SET DEFAULT 'shipped',
    ALTER COLUMN shipping_method SET NOT NULL;

-- NOT NULL alone is a half-measure: the column default only applies when the
-- column is omitted from an INSERT, and every writer here goes through sqlc,
-- which always names it. An unset method therefore arrives as '' rather than
-- falling back to the default — a third spelling of the same bug this migration
-- exists to remove. The constraint makes '' unrepresentable too, and catches a
-- typo'd method while it is at it.
ALTER TABLE orders
    ADD CONSTRAINT orders_shipping_method_check
        CHECK (shipping_method IN ('shipped', 'local_delivery', 'pickup'));

-- On the backfill value: 227 of the 267 ship to out-of-zone addresses, where
-- 'shipped' is the only thing they could have been. The remaining 40 are in-zone
-- (Tri-Cities), placed 2026-04-26..2026-06-15, mostly subscription renewals, 37
-- already delivered — and what they physically were cannot be recovered. Every
-- discriminator tried was inconclusive: they hold no shipment rows, but neither do
-- the 148 known 'shipped' orders from the same window; they carry no scheduled or
-- run date, but neither do the 135 known local_delivery orders from that window,
-- because those columns came later.
--
-- They are stamped 'shipped' because that is what every read site has always
-- believed NULL meant, so no system behaviour changes, past or present. Calling
-- them local_delivery would retroactively enter them into local-delivery reporting
-- they were never part of. Listed here so the choice stays auditable and
-- reversible, since after this runs they are indistinguishable:
--   SUB-1777163273479 SUB-1777169979167 SUB-1777169979628 SUB-1777170708648 
--   SUB-1777173090179 ORD-A2DF24C5E3 ORD-7DE42C7EB5 SUB-1777300875568 
--   ORD-D7CD6E39E3 SUB-1777320651299 ORD-311A13111E SUB-1777407677383 
--   SUB-1777413076393 ORD-41ADB88A39 ORD-8F9ED19E60 ORD-8BFB622FC9 
--   SUB-1777511565249 ORD-B2E85DCCC3 ORD-2EC5C5C1A0 ORD-72446D76C2 
--   SUB-1777532429582 SUB-1777551990515 SUB-1777563078576 SUB-1777577402930 
--   SUB-1777583763540 ORD-1152776571 ORD-94D515C899 ORD-41C55EF80C 
--   ORD-2137E3185F ORD-7F19898C7D ORD-F88281F311 ORD-D57BFEAD7E ORD-E043B406F8 
--   ORD-FB79898310 ORD-50829DA5FD ORD-9CAEB539F2 ORD-E33FB7FB08 ORD-C9D4D37705 
--   ORD-DE6B665BE9 ORD-00C25FB919

-- +goose Down
-- Lossy by nature: which rows were NULL is not recoverable once backfilled (see
-- the list above for the only cohort where 'shipped' was a judgement call rather
-- than a certainty). Down only restores the column's nullability.
ALTER TABLE orders
    DROP CONSTRAINT IF EXISTS orders_shipping_method_check;

ALTER TABLE orders
    ALTER COLUMN shipping_method DROP NOT NULL,
    ALTER COLUMN shipping_method DROP DEFAULT;
