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
