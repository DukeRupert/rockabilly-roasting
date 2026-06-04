-- name: GetBasePrice :one
SELECT p.id, p.price_set_id, p.amount, p.currency_code,
       p.min_quantity, p.max_quantity,
       p.price_list_id, p.starts_at, p.ends_at
FROM price_sets ps
JOIN prices p ON p.price_set_id = ps.id
WHERE ps.variant_id = $1
  AND p.currency_code = $2
  AND p.price_list_id IS NULL  AND p.min_quantity IS NULL
LIMIT 1;

-- name: CreatePriceSet :one
INSERT INTO price_sets (id, variant_id)
VALUES ($1, $2)
RETURNING *;

-- name: GetPriceSetByVariant :one
SELECT * FROM price_sets WHERE variant_id = $1;

-- name: UpsertBasePrice :one
INSERT INTO prices (id, price_set_id, amount, currency_code)
VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE SET amount = EXCLUDED.amount
RETURNING *;

-- name: DeleteBasePrice :exec
DELETE FROM prices
WHERE price_set_id = $1
  AND currency_code = $2
  AND price_list_id IS NULL  AND min_quantity IS NULL;

-- name: ListBasePricesByProduct :many
SELECT p.id, p.price_set_id, p.amount, p.currency_code,
       p.min_quantity, p.max_quantity,
       p.price_list_id, p.starts_at, p.ends_at,
       ps.variant_id
FROM variants v
JOIN price_sets ps ON ps.variant_id = v.id
JOIN prices p ON p.price_set_id = ps.id
WHERE v.product_id = $1
  AND p.currency_code = $2
  AND p.price_list_id IS NULL  AND p.min_quantity IS NULL;

-- name: GetPriceListPrice :one
SELECT p.id, p.price_set_id, p.amount, p.currency_code,
       p.min_quantity, p.max_quantity,
       p.price_list_id, p.starts_at, p.ends_at
FROM price_sets ps
JOIN prices p ON p.price_set_id = ps.id
WHERE ps.variant_id = $1
  AND p.currency_code = $2
  AND p.price_list_id = $3  AND p.min_quantity IS NULL
LIMIT 1;

-- name: ListBasePricesByVariants :many
SELECT p.id, p.price_set_id, p.amount, p.currency_code,
       p.min_quantity, p.max_quantity,
       p.price_list_id, p.starts_at, p.ends_at,
       ps.variant_id
FROM price_sets ps
JOIN prices p ON p.price_set_id = ps.id
WHERE ps.variant_id = ANY($1::uuid[])
  AND p.currency_code = $2
  AND p.price_list_id IS NULL  AND p.min_quantity IS NULL;

-- name: ListPriceListPricesByVariants :many
SELECT p.id, p.price_set_id, p.amount, p.currency_code,
       p.min_quantity, p.max_quantity,
       p.price_list_id, p.starts_at, p.ends_at,
       ps.variant_id
FROM price_sets ps
JOIN prices p ON p.price_set_id = ps.id
WHERE ps.variant_id = ANY($1::uuid[])
  AND p.currency_code = $2
  AND p.price_list_id = $3  AND p.min_quantity IS NULL;

-- name: UpsertPriceListPrice :one
INSERT INTO prices (id, price_set_id, amount, currency_code, price_list_id)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE SET amount = EXCLUDED.amount
RETURNING *;

-- name: DeletePriceListPrice :exec
DELETE FROM prices
WHERE price_set_id = $1
  AND currency_code = $2
  AND price_list_id = $3  AND min_quantity IS NULL;

-- name: ListPriceListPricesByProduct :many
SELECT p.id, p.price_set_id, p.amount, p.currency_code,
       p.min_quantity, p.max_quantity,
       p.price_list_id, p.starts_at, p.ends_at,
       ps.variant_id
FROM variants v
JOIN price_sets ps ON ps.variant_id = v.id
JOIN prices p ON p.price_set_id = ps.id
WHERE v.product_id = $1
  AND p.currency_code = $2
  AND p.price_list_id IS NOT NULL  AND p.min_quantity IS NULL;

-- name: CreatePriceList :one
INSERT INTO price_lists (id, name, type, status)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetPriceListByID :one
SELECT * FROM price_lists WHERE id = $1;

-- name: ListPriceLists :many
SELECT * FROM price_lists ORDER BY name;

-- name: UpdatePriceList :one
UPDATE price_lists
SET name = $2,
    status = $3
WHERE id = $1
RETURNING *;

-- name: DeletePriceList :exec
DELETE FROM price_lists WHERE id = $1;

-- name: CountCustomersByPriceList :one
SELECT count(*) FROM customers WHERE price_list_id = $1;
