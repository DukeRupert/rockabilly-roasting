-- Wholesale 2024 price list seed (idempotent; safe to re-run).
--
-- Recreates the OrderSpace legacy "2024 Wholesale Price" list in Hiri so that
-- customers still on it (MOCHA Express) keep their 2024 pricing through the
-- migration. Moving them up to 2025/2026 is deliberately left as a later
-- business decision, not baked into the migration.
--
-- Priced flat by size, same convention as the Wholesale 2025/2026 lists: the
-- Hiri 12O (12oz) variant takes the OS *1LB* wholesale price (Hiri retired the
-- 1lb bag and carried its price to 12oz), NOT the OS retail-12oz price. From the
-- OS 2024 list:
--
--   12O <- OS 1LB   $10.50
--   1LB (RIU only)  $10.50
--   3LB             $31.50
--   5LB             $52.50
--
-- Prerequisite for importing MOCHA Express (Batch 3). The importer looks this
-- list up by the name 'Wholesale 2024' and refuses to import a customer mapped
-- to it until it exists.

-- 1. The price list itself.
INSERT INTO price_lists (name, type, status)
SELECT 'Wholesale 2024', 'override', 'active'
WHERE NOT EXISTS (SELECT 1 FROM price_lists WHERE name = 'Wholesale 2024');

-- 2. One 2024 price per variant, by size. Skips variants already priced, so
--    re-running only fills gaps.
WITH wl AS (SELECT id FROM price_lists WHERE name = 'Wholesale 2024')
INSERT INTO prices (price_set_id, amount, currency_code, price_list_id)
SELECT ps.id,
       CASE split_part(v.sku, '-', 2)
           WHEN '12O' THEN 1050
           WHEN '1LB' THEN 1050
           WHEN '3LB' THEN 3150
           WHEN '5LB' THEN 5250
       END,
       'USD',
       (SELECT id FROM wl)
FROM variants v
JOIN price_sets ps ON ps.variant_id = v.id
WHERE split_part(v.sku, '-', 2) IN ('12O', '1LB', '3LB', '5LB')
  AND NOT EXISTS (
      SELECT 1 FROM prices p
      WHERE p.price_set_id = ps.id
        AND p.price_list_id = (SELECT id FROM wl)
  );
