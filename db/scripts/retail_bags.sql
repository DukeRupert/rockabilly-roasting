-- Create the "Retail 12oz Bags" wholesale product.
--
-- Restores the Orderspace product structure (one product, flavor option) that the
-- os-migrate importer folded into each coffee's 12O variant (cmd/os-migrate/skumap.go:71).
-- Packaged retail bags carry their own price so packaging cost is not buried in the
-- bean price.
--
-- Creates the product in 'draft' so nothing reaches the wholesale order sheet until
-- it has been reviewed in admin (QuickOrderCatalog filters status='active').
--
-- Idempotency: not re-runnable. It will fail on the products_slug unique index if the
-- product already exists, which is the desired behaviour.

BEGIN;

-- ---------------------------------------------------------------------------
-- FILL IN: prices in CENTS. One row per flavor.
-- Leave a column NULL to skip that price list for that flavor.
--   base    -> fallback when a customer has no price list attached
--   ws2024  -> Wholesale 2024
--   tailwnd -> Tailwinds
--   ws2025  -> Wholesale 2025   (migrated OS customers are grandfathered here)
--   ws2026  -> Wholesale 2026   (default for new accounts)
-- Grind does not affect price: whole bean and drip both use the flavor's row.
-- ---------------------------------------------------------------------------
CREATE TEMP TABLE _rb_price (
    title   text PRIMARY KEY,
    code    text NOT NULL,
    base    int,
    ws2024  int,
    tailwnd int,
    ws2025  int,
    ws2026  int
) ON COMMIT DROP;

-- Taken from the Orderspace 0009 price screens (2026-07-27). Uniform across all
-- seven OS flavors -- no decaf premium, unlike the bean product where Cascadia
-- carries +$1.00. Rev It Up was never an OS retail bag; it inherits the same
-- ladder by extrapolation.
INSERT INTO _rb_price (title, code, base, ws2024, tailwnd, ws2025, ws2026) VALUES
    ('2-Stroke',   '2ST', 1250, 900, 1250, 1100, 1250),
    ('Bike Blend', 'BB',  1250, 900, 1250, 1100, 1250),
    ('Cascadia',   'CAS', 1250, 900, 1250, 1100, 1250),  -- OS: Cascadia Decaf
    ('Chop Top',   'CT',  1250, 900, 1250, 1100, 1250),
    ('Cloud 9',    'C9',  1250, 900, 1250, 1100, 1250),
    ('Ethiopia',   'ETH', 1250, 900, 1250, 1100, 1250),
    ('Guatemala',  'GUA', 1250, 900, 1250, 1100, 1250),  -- OS: Guatemalan Tikal
    ('Rev It Up',  'RIU', 1250, 900, 1250, 1100, 1250);  -- extrapolated, no OS price

-- Refuse to run against an unfilled template.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM _rb_price WHERE ws2025 IS NULL OR ws2026 IS NULL) THEN
        RAISE EXCEPTION 'prices not filled in: every flavor needs ws2025 and ws2026';
    END IF;
END $$;

-- ---------------------------------------------------------------------------
-- 1. Product. Wholesale-only: 'wholesale' visibility keeps it off the retail
--    storefront, which builds its filter from a zero-value viewer and therefore
--    matches visibility='public' alone (internal/store/catalog.go:336).
--    Not subscribable -- these are for resale, not subscription fulfillment.
--    tax_exempt matches every other coffee product.
-- ---------------------------------------------------------------------------
CREATE TEMP TABLE _rb_product ON COMMIT DROP AS
WITH ins AS (
    INSERT INTO products (slug, title, description, status, visibility,
                          subscribable, tax_exempt, is_featured, taxon_id)
    VALUES (
        'retail-12oz-bags',
        'Retail 12oz Bags',
        'Hand-packed 12oz bags, labeled and ready for the shelf. Same roast we ship in bulk -- priced for resale.',
        'draft',
        'wholesale',
        false,
        true,
        false,
        (SELECT taxon_id FROM products WHERE slug = 'cloud-9')
    )
    RETURNING id
)
SELECT id FROM ins;

-- ---------------------------------------------------------------------------
-- 2. Options. Existing products use lowercase option names and values
--    ('grind' = whole bean|drip|espresso|french press), so match that.
--    Retail bags ship whole bean or drip only.
-- ---------------------------------------------------------------------------
CREATE TEMP TABLE _rb_opt ON COMMIT DROP AS
WITH ins AS (
    INSERT INTO product_options (product_id, name, position)
    SELECT p.id, o.name, o.position
    FROM _rb_product p
    CROSS JOIN (VALUES ('flavor', 0), ('grind', 1)) AS o(name, position)
    RETURNING id, name
)
SELECT id, name FROM ins;

CREATE TEMP TABLE _rb_flavor ON COMMIT DROP AS
WITH ins AS (
    INSERT INTO product_option_values (product_option_id, value, position)
    SELECT o.id, f.title, row_number() OVER (ORDER BY f.title) - 1
    FROM _rb_opt o, _rb_price f
    WHERE o.name = 'flavor'
    RETURNING id, value
)
SELECT id, value FROM ins;

CREATE TEMP TABLE _rb_grind ON COMMIT DROP AS
WITH ins AS (
    INSERT INTO product_option_values (product_option_id, value, position)
    SELECT o.id, g.value, g.position
    FROM _rb_opt o
    CROSS JOIN (VALUES ('whole bean', 0), ('drip', 1)) AS g(value, position)
    WHERE o.name = 'grind'
    RETURNING id, value
)
SELECT id, value FROM ins;

-- ---------------------------------------------------------------------------
-- 3. Variants: 8 flavors x 2 grinds = 16.
--    retail_available=false  -> never orderable on the retail storefront
--    wholesale_available=true -> appears on the wholesale order sheet
--    340g = 12oz, matching the existing 12O variants.
--    Cloud 9 whole bean is the default (highest historical volume: 747 units).
-- ---------------------------------------------------------------------------
CREATE TEMP TABLE _rb_variant ON COMMIT DROP AS
WITH grid AS (
    SELECT f.title, f.code, g.value AS grind,
           CASE g.value WHEN 'whole bean' THEN 'WB' ELSE 'DRI' END AS grind_code,
           row_number() OVER (ORDER BY f.title, g.value DESC) - 1 AS pos
    FROM _rb_price f
    CROSS JOIN (VALUES ('whole bean'), ('drip')) AS g(value)
), ins AS (
    INSERT INTO variants (product_id, sku, position, is_default, weight_grams,
                          retail_available, wholesale_available, metadata)
    SELECT p.id,
           'RB-' || grid.code || '-' || grid.grind_code,
           grid.pos,
           (grid.code = 'C9' AND grid.grind_code = 'WB'),
           340,
           false,
           true,
           -- Suggested resale price carried over from Orderspace. Nothing reads
           -- this today; stored so the number survives the migration.
           '{"msrp_cents": 1800}'::jsonb
    FROM _rb_product p, grid
    RETURNING id, sku
)
SELECT i.id, i.sku,
       split_part(i.sku, '-', 2) AS code,
       split_part(i.sku, '-', 3) AS grind_code
FROM ins i;

-- Link each variant to its flavor and grind option values.
INSERT INTO variant_option_values (variant_id, product_option_value_id)
SELECT v.id, f.id
FROM _rb_variant v
JOIN _rb_price pr ON pr.code = v.code
JOIN _rb_flavor f ON f.value = pr.title;

INSERT INTO variant_option_values (variant_id, product_option_value_id)
SELECT v.id, g.id
FROM _rb_variant v
JOIN _rb_grind g
  ON g.value = CASE v.grind_code WHEN 'WB' THEN 'whole bean' ELSE 'drip' END;

-- ---------------------------------------------------------------------------
-- 4. Price sets + prices. Every variant needs a price_set; a missing price
--    silently renders $0.00 on the order sheet (wholesale_portal.templ:247),
--    so the base column acts as the backstop.
-- ---------------------------------------------------------------------------
INSERT INTO price_sets (variant_id)
SELECT id FROM _rb_variant;

INSERT INTO prices (price_set_id, amount, currency_code, price_list_id)
SELECT ps.id, amt.amount, 'usd', pl.id
FROM _rb_variant v
JOIN price_sets ps ON ps.variant_id = v.id
JOIN _rb_price pr  ON pr.code = v.code
CROSS JOIN LATERAL (VALUES
    (pr.base,    NULL::text),
    (pr.ws2024,  'Wholesale 2024'),
    (pr.tailwnd, 'Tailwinds'),
    (pr.ws2025,  'Wholesale 2025'),
    (pr.ws2026,  'Wholesale 2026')
) AS amt(amount, list_name)
LEFT JOIN price_lists pl ON pl.name = amt.list_name
WHERE amt.amount IS NOT NULL
  AND (amt.list_name IS NULL OR pl.id IS NOT NULL);

-- ---------------------------------------------------------------------------
-- 5. Unrelated data fix: 'Cloud 9 ' carries a trailing space.
-- ---------------------------------------------------------------------------
UPDATE products SET title = btrim(title), updated_at = now()
WHERE title <> btrim(title);

-- ---------------------------------------------------------------------------
-- Verification -- read these before COMMIT.
-- ---------------------------------------------------------------------------
SELECT v.sku,
       max(p.amount) FILTER (WHERE p.price_list_id IS NULL)                AS base,
       max(p.amount) FILTER (WHERE pl.name = 'Wholesale 2024')             AS ws2024,
       max(p.amount) FILTER (WHERE pl.name = 'Tailwinds')                  AS tailwinds,
       max(p.amount) FILTER (WHERE pl.name = 'Wholesale 2025')             AS ws2025,
       max(p.amount) FILTER (WHERE pl.name = 'Wholesale 2026')             AS ws2026,
       (SELECT count(*) FROM variant_option_values vov WHERE vov.variant_id = v.id)
                                                                           AS opt_links
FROM _rb_variant v
JOIN price_sets ps ON ps.variant_id = v.id
LEFT JOIN prices p ON p.price_set_id = ps.id
LEFT JOIN price_lists pl ON pl.id = p.price_list_id
GROUP BY v.id, v.sku ORDER BY v.sku;

-- Expect: 16 rows, opt_links = 2 on every row, no NULL in ws2025 / ws2026.

COMMIT;

-- After reviewing the draft product in admin, publish with:
--   UPDATE products SET status = 'active', updated_at = now()
--   WHERE slug = 'retail-12oz-bags';
