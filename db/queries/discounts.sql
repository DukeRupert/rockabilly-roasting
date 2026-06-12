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
-- Case-insensitive: codes are stored uppercase by the admin form, but legacy
-- or hand-inserted rows may differ and customers type in any case.
SELECT * FROM coupon_codes WHERE UPPER(code) = UPPER($1);

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

-- name: RedeemCouponCode :one
-- Atomically redeem a coupon — only succeeds if redeemed_at IS NULL.
-- Returns the row if successful; pgx.ErrNoRows if already redeemed.
UPDATE coupon_codes
SET redeemed_at = now(),
    redeemed_by = $2,
    redeemed_by_order_id = $3
WHERE id = $1
  AND redeemed_at IS NULL
RETURNING *;

-- name: DeleteCouponCode :exec
DELETE FROM coupon_codes WHERE id = $1;

-- name: ReleaseCouponCodeByOrderID :exec
-- Reverses a coupon redemption tied to a specific order. Used when an order
-- is cancelled (admin or abandoned-checkout cleanup) so the code can be used
-- again. No-op if the coupon was never redeemed for that order.
UPDATE coupon_codes
SET redeemed_at = NULL,
    redeemed_by = NULL,
    redeemed_by_order_id = NULL
WHERE redeemed_by_order_id = $1;
