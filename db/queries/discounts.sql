-- name: CreateDiscount :one
INSERT INTO discounts (id, name, description, type, value, minimum_order_cents, starts_at, expires_at, active)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetDiscountByID :one
SELECT * FROM discounts WHERE id = $1;

-- name: UpdateDiscount :one
UPDATE discounts
SET name = $2, description = $3, type = $4, value = $5, minimum_order_cents = $6,
    starts_at = $7, expires_at = $8, active = $9, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteDiscount :exec
DELETE FROM discounts WHERE id = $1;

-- name: CreateCouponCode :one
INSERT INTO coupon_codes (id, discount_id, code, customer_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetCouponCodeByCode :one
SELECT * FROM coupon_codes WHERE code = $1;

-- name: GetCouponCodeByID :one
SELECT * FROM coupon_codes WHERE id = $1;

-- name: ListCouponCodesByDiscount :many
SELECT * FROM coupon_codes
WHERE discount_id = $1
ORDER BY created_at DESC;

-- name: MarkCouponCodeRedeemed :exec
UPDATE coupon_codes
SET redeemed_at = now(), redeemed_by = $2
WHERE id = $1;

-- name: DeleteCouponCode :exec
DELETE FROM coupon_codes WHERE id = $1;
