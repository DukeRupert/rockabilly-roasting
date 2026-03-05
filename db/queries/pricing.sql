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
