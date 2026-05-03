-- name: GetBasePrice :one
SELECT p.id, p.price_set_id, p.amount, p.currency_code,
       p.min_quantity, p.max_quantity, p.customer_group_id,
       p.price_list_id, p.starts_at, p.ends_at
FROM price_sets ps
JOIN prices p ON p.price_set_id = ps.id
WHERE ps.variant_id = $1
  AND p.currency_code = $2
  AND p.price_list_id IS NULL
  AND p.customer_group_id IS NULL
  AND p.min_quantity IS NULL
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
  AND price_list_id IS NULL
  AND customer_group_id IS NULL
  AND min_quantity IS NULL;

-- name: ListBasePricesByProduct :many
SELECT p.id, p.price_set_id, p.amount, p.currency_code,
       p.min_quantity, p.max_quantity, p.customer_group_id,
       p.price_list_id, p.starts_at, p.ends_at,
       ps.variant_id
FROM variants v
JOIN price_sets ps ON ps.variant_id = v.id
JOIN prices p ON p.price_set_id = ps.id
WHERE v.product_id = $1
  AND p.currency_code = $2
  AND p.price_list_id IS NULL
  AND p.customer_group_id IS NULL
  AND p.min_quantity IS NULL;

-- name: GetGroupPrice :one
SELECT p.id, p.price_set_id, p.amount, p.currency_code,
       p.min_quantity, p.max_quantity, p.customer_group_id,
       p.price_list_id, p.starts_at, p.ends_at
FROM price_sets ps
JOIN prices p ON p.price_set_id = ps.id
WHERE ps.variant_id = $1
  AND p.currency_code = $2
  AND p.customer_group_id = $3
  AND p.price_list_id IS NULL
  AND p.min_quantity IS NULL
LIMIT 1;

-- name: UpsertGroupPrice :one
INSERT INTO prices (id, price_set_id, amount, currency_code, customer_group_id)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE SET amount = EXCLUDED.amount
RETURNING *;

-- name: DeleteGroupPrice :exec
DELETE FROM prices
WHERE price_set_id = $1
  AND currency_code = $2
  AND customer_group_id = $3
  AND price_list_id IS NULL
  AND min_quantity IS NULL;

-- name: ListGroupPricesByProduct :many
SELECT p.id, p.price_set_id, p.amount, p.currency_code,
       p.min_quantity, p.max_quantity, p.customer_group_id,
       p.price_list_id, p.starts_at, p.ends_at,
       ps.variant_id
FROM variants v
JOIN price_sets ps ON ps.variant_id = v.id
JOIN prices p ON p.price_set_id = ps.id
WHERE v.product_id = $1
  AND p.currency_code = $2
  AND p.customer_group_id IS NOT NULL
  AND p.price_list_id IS NULL
  AND p.min_quantity IS NULL;

-- name: GetPriceListPrice :one
SELECT p.id, p.price_set_id, p.amount, p.currency_code,
       p.min_quantity, p.max_quantity, p.customer_group_id,
       p.price_list_id, p.starts_at, p.ends_at
FROM price_sets ps
JOIN prices p ON p.price_set_id = ps.id
WHERE ps.variant_id = $1
  AND p.currency_code = $2
  AND p.price_list_id = $3
  AND p.customer_group_id IS NULL
  AND p.min_quantity IS NULL
LIMIT 1;

-- name: ListBasePricesByVariants :many
SELECT p.id, p.price_set_id, p.amount, p.currency_code,
       p.min_quantity, p.max_quantity, p.customer_group_id,
       p.price_list_id, p.starts_at, p.ends_at,
       ps.variant_id
FROM price_sets ps
JOIN prices p ON p.price_set_id = ps.id
WHERE ps.variant_id = ANY($1::uuid[])
  AND p.currency_code = $2
  AND p.price_list_id IS NULL
  AND p.customer_group_id IS NULL
  AND p.min_quantity IS NULL;

-- name: ListPriceListPricesByVariants :many
SELECT p.id, p.price_set_id, p.amount, p.currency_code,
       p.min_quantity, p.max_quantity, p.customer_group_id,
       p.price_list_id, p.starts_at, p.ends_at,
       ps.variant_id
FROM price_sets ps
JOIN prices p ON p.price_set_id = ps.id
WHERE ps.variant_id = ANY($1::uuid[])
  AND p.currency_code = $2
  AND p.price_list_id = $3
  AND p.customer_group_id IS NULL
  AND p.min_quantity IS NULL;
