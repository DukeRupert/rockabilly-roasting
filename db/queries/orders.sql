-- name: CreateOrder :one
INSERT INTO orders (id, number, customer_id, status, payment_status, fulfillment_status,
                    currency_code, subtotal, discount_total, shipping_total, tax_total, total,
                    shipping_address_id, billing_address_id, subscription_id, draft_by_user_id,
                    tax_exempt, tax_exempt_reason, stripe_tax_id, notes, metadata, placed_at,
                    shipping_method, requested_delivery_date)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
RETURNING *;

-- name: GetOrderByID :one
SELECT * FROM orders WHERE id = $1;

-- name: GetOrderByNumber :one
SELECT * FROM orders WHERE number = $1;

-- name: UpdateOrderStatus :one
UPDATE orders
SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateOrderPaymentStatus :one
UPDATE orders
SET payment_status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateOrderFulfillmentStatus :one
UPDATE orders
SET fulfillment_status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateOrderStripePaymentIntentID :one
UPDATE orders
SET stripe_payment_intent_id = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetOrderByStripePaymentIntentID :one
SELECT * FROM orders WHERE stripe_payment_intent_id = $1;

-- name: GetOrderByStripePaymentIntentIDForUpdate :one
-- Same as GetOrderByStripePaymentIntentID but takes a row-level lock.
-- Use this on the redirect-back and webhook paths where two transactions
-- can race for the same order — the second waits for the first to commit
-- and then sees the post-transition state, so side effects (audit, email,
-- coupon redemption) don't double-fire.
SELECT * FROM orders WHERE stripe_payment_intent_id = $1 FOR UPDATE;

-- name: UpdateOrderShippingMethod :one
UPDATE orders
SET shipping_method = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateOrderRequestedDeliveryDate :one
UPDATE orders
SET requested_delivery_date = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteOrder :exec
DELETE FROM orders WHERE id = $1;

-- name: CreateCart :one
INSERT INTO carts (id, customer_id, currency_code, shipping_address_id, billing_address_id, metadata, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetCartByID :one
SELECT * FROM carts WHERE id = $1;

-- name: GetCartByCustomerID :one
SELECT * FROM carts WHERE customer_id = $1;

-- name: UpdateCartAddresses :one
UPDATE carts
SET shipping_address_id = $2, billing_address_id = $3
WHERE id = $1
RETURNING *;

-- name: UpdateCartDiscount :one
UPDATE carts
SET applied_discount_id = $2, applied_coupon_code_id = $3
WHERE id = $1
RETURNING *;

-- name: DeleteCart :exec
DELETE FROM carts WHERE id = $1;

-- name: CreateLineItem :one
INSERT INTO line_items (id, order_id, variant_id, quantity, unit_price, subtotal, discount_total, tax_total, total, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: ListLineItemsByOrder :many
SELECT * FROM line_items
WHERE order_id = $1
ORDER BY id;

-- name: GetLineItem :one
SELECT * FROM line_items
WHERE id = $1;

-- name: UpdateLineItemVariant :one
UPDATE line_items
SET variant_id = $2
WHERE id = $1
RETURNING *;

-- name: DeleteLineItem :exec
DELETE FROM line_items WHERE id = $1;

-- name: DeleteLineItemsByOrder :exec
DELETE FROM line_items WHERE order_id = $1;

-- name: CreateAdjustment :one
INSERT INTO adjustments (id, order_id, line_item_id, label, amount, source_type, source_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListAdjustmentsByOrder :many
SELECT * FROM adjustments
WHERE order_id = $1
ORDER BY id;

-- name: ListAdjustmentsByLineItem :many
SELECT * FROM adjustments
WHERE line_item_id = $1
ORDER BY id;

-- name: DeleteAdjustment :exec
DELETE FROM adjustments WHERE id = $1;
