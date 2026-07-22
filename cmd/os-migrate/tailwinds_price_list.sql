-- Tailwinds price list seed (idempotent; safe to re-run).
--
-- Recreates the OrderSpace "Tailwinds" concessions price list in Hiri so that
-- Tailwind Concessions keeps its pricing through the migration. Tailwinds is a
-- MARKUP list (concessions pay more than base), priced flat by size — grind and
-- decaf make no difference. Hiri prices follow the same size convention as the
-- Wholesale 2025/2026 lists: the Hiri 12O (12oz) variant takes the OS *1LB*
-- wholesale price (Hiri retired the 1lb bag and carried its price to 12oz), NOT
-- the OS retail-12oz price. From the OS Tailwinds list:
--
--   12O <- OS 1LB   $11.00
--   1LB (RIU only)  $11.00   -- OS Tailwinds 1lb wholesale price
--   3LB             $33.00
--   5LB             $55.00
--
-- Prerequisite for importing Tailwind Concessions (Batch 3). The importer
-- (cmd/os-migrate) looks this list up by the name 'Tailwinds' and refuses to
-- import a Tailwinds-priced customer until it exists.

-- 1. The price list itself.
INSERT INTO price_lists (name, type, status)
SELECT 'Tailwinds', 'override', 'active'
WHERE NOT EXISTS (SELECT 1 FROM price_lists WHERE name = 'Tailwinds');

-- 2. One Tailwinds price per variant, by size. Skips variants already priced,
--    so re-running only fills gaps (e.g. after new SKUs are added).
WITH tw AS (SELECT id FROM price_lists WHERE name = 'Tailwinds')
INSERT INTO prices (price_set_id, amount, currency_code, price_list_id)
SELECT ps.id,
       CASE split_part(v.sku, '-', 2)
           WHEN '12O' THEN 1100
           WHEN '1LB' THEN 1100
           WHEN '3LB' THEN 3300
           WHEN '5LB' THEN 5500
       END,
       'USD',
       (SELECT id FROM tw)
FROM variants v
JOIN price_sets ps ON ps.variant_id = v.id
WHERE split_part(v.sku, '-', 2) IN ('12O', '1LB', '3LB', '5LB')
  AND NOT EXISTS (
      SELECT 1 FROM prices p
      WHERE p.price_set_id = ps.id
        AND p.price_list_id = (SELECT id FROM tw)
  );
