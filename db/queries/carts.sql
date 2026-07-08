-- name: CreateCartForSession :one
INSERT INTO carts (id, currency_code, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpsertCartItem :one
INSERT INTO cart_items (id, cart_id, variant_id, quantity, unit_price)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (cart_id, variant_id)
DO UPDATE SET quantity = cart_items.quantity + EXCLUDED.quantity,
              updated_at = now()
RETURNING *;

-- name: SetCartItemByVariant :one
INSERT INTO cart_items (id, cart_id, variant_id, quantity, unit_price)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (cart_id, variant_id)
DO UPDATE SET quantity = EXCLUDED.quantity,
              unit_price = EXCLUDED.unit_price,
              updated_at = now()
RETURNING *;

-- name: SetCartItemQuantity :one
UPDATE cart_items
SET quantity = $1, updated_at = now()
WHERE id = $2
RETURNING *;

-- name: DeleteCartItem :exec
DELETE FROM cart_items WHERE id = $1;

-- name: DeleteCartItemByCartAndVariant :exec
DELETE FROM cart_items WHERE cart_id = $1 AND variant_id = $2;

-- name: ListCartItems :many
SELECT * FROM cart_items WHERE cart_id = $1 ORDER BY created_at;

-- name: GetCartItemCount :one
SELECT COALESCE(SUM(quantity), 0)::int AS total_items
FROM cart_items WHERE cart_id = $1;
