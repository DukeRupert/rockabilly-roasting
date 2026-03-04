-- name: CreateSubscriptionPlan :one
INSERT INTO subscription_plans (id, name, interval, interval_count, variant_id, price_set_id, is_active, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetSubscriptionPlanByID :one
SELECT * FROM subscription_plans WHERE id = $1;

-- name: ListSubscriptionPlans :many
SELECT * FROM subscription_plans ORDER BY name;

-- name: UpdateSubscriptionPlanActive :exec
UPDATE subscription_plans SET is_active = $2 WHERE id = $1;

-- name: CreateSubscription :one
INSERT INTO subscriptions (id, customer_id, plan_id, status, shipping_address_id,
                           current_period_start, current_period_end, next_order_at, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetSubscriptionByID :one
SELECT * FROM subscriptions WHERE id = $1;

-- name: GetSubscriptionByIDAndCustomer :one
SELECT * FROM subscriptions WHERE id = $1 AND customer_id = $2;

-- name: ListSubscriptionsByCustomer :many
SELECT * FROM subscriptions
WHERE customer_id = $1
ORDER BY created_at DESC;

-- name: UpdateSubscriptionStatus :one
UPDATE subscriptions
SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateSubscriptionPeriod :exec
UPDATE subscriptions
SET current_period_start = $2, current_period_end = $3, next_order_at = $4, updated_at = now()
WHERE id = $1;

-- name: UpdateSubscriptionPauseUntil :exec
UPDATE subscriptions
SET pause_until = $2, updated_at = now()
WHERE id = $1;

-- name: CancelSubscription :exec
UPDATE subscriptions
SET status = 'cancelled', cancelled_at = now(), updated_at = now()
WHERE id = $1;

-- name: ListSubscriptionsDueForRenewal :many
SELECT * FROM subscriptions
WHERE status = 'active' AND next_order_at <= now()
ORDER BY next_order_at ASC;

-- name: CreateSubscriptionOrder :exec
INSERT INTO subscription_orders (subscription_id, order_id, period_start, period_end)
VALUES ($1, $2, $3, $4);

-- name: ListSubscriptionOrdersBySubscription :many
SELECT * FROM subscription_orders
WHERE subscription_id = $1
ORDER BY period_start DESC;
