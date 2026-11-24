-- name: CreateSubscriptionPlan :one
INSERT INTO subscription_plans (id, name, interval, interval_count, discount_pct, is_active, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetSubscriptionPlanByID :one
SELECT * FROM subscription_plans WHERE id = $1;

-- name: ListSubscriptionPlans :many
SELECT * FROM subscription_plans ORDER BY name;

-- name: UpdateSubscriptionPlanActive :exec
UPDATE subscription_plans SET is_active = $2 WHERE id = $1;

-- name: UpdateSubscriptionPlanDiscount :exec
UPDATE subscription_plans SET discount_pct = $2 WHERE id = $1;

-- name: CreateSubscription :one
INSERT INTO subscriptions (id, customer_id, plan_id, variant_id, quantity, status, shipping_address_id,
                           current_period_start, current_period_end, next_order_at, ends_at,
                           stripe_payment_method_id, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
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
-- A status change, only from the status the caller read. A change that landed
-- in between (a cancel while a resume was deciding) leaves no row, so the
-- later write cannot undo the earlier one.
UPDATE subscriptions
SET status = sqlc.arg(status), updated_at = now()
WHERE id = sqlc.arg(id) AND status = sqlc.arg(from_status)
RETURNING *;

-- name: AdvanceRenewedSubscriptionPeriod :execrows
-- A renewal's period advance. Only a subscription still active or past due,
-- and still due when the renewal read it, advances: one cancelled, paused or
-- skipped during the charge reports zero rows, and the renewal refunds rather
-- than writes.
UPDATE subscriptions
SET current_period_start = @period_start, current_period_end = @period_end,
    next_order_at = @next_order_at, updated_at = now()
WHERE id = @id AND status IN ('active', 'past_due')
  AND next_order_at = @read_next_order_at;

-- name: UpdateSubscriptionPeriod :exec
UPDATE subscriptions
SET current_period_start = $2, current_period_end = $3, next_order_at = $4, updated_at = now()
WHERE id = $1;

-- name: ClaimSubscriptionRenewal :many
-- Claims every subscription named that nobody holds, or whose holder's lease
-- has run out. The caller compares the rows returned with the ids it asked
-- for and, on a short count, rolls back: the claim is all or nothing.
UPDATE subscriptions
SET renewal_claimed_at = now()
WHERE id = ANY(@ids::uuid[])
  AND (renewal_claimed_at IS NULL OR renewal_claimed_at < @stale_before)
RETURNING id;

-- name: ReleaseSubscriptionRenewalClaim :exec
UPDATE subscriptions
SET renewal_claimed_at = NULL
WHERE id = ANY(@ids::uuid[]);

-- name: SetSubscriptionRenewalPaymentIntent :exec
UPDATE subscriptions
SET renewal_payment_intent_id = @payment_intent_id::text
WHERE id = ANY(@ids::uuid[]);

-- name: ClearSubscriptionRenewalPaymentIntent :many
-- Cleared by intent rather than by subscription: a batch's charge covers
-- every member, and settling it — by an order or a refund — settles all of
-- them, including a member that has since left the box.
UPDATE subscriptions
SET renewal_payment_intent_id = NULL, renewal_refunding = false
WHERE renewal_payment_intent_id = @payment_intent_id::text
RETURNING id;

-- name: MarkSubscriptionRenewalRefunding :exec
-- The key generation moves once per refund, however many times a failed
-- refund is retried.
UPDATE subscriptions
SET renewal_key_generation = renewal_key_generation + CASE WHEN renewal_refunding THEN 0 ELSE 1 END,
    renewal_refunding = true
WHERE renewal_payment_intent_id = @payment_intent_id::text;

-- name: UpdateSubscriptionPauseUntil :exec
UPDATE subscriptions
SET pause_until = $2, updated_at = now()
WHERE id = $1;

-- name: UpdateSubscriptionVariant :one
UPDATE subscriptions
SET variant_id = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateSubscriptionPlan :one
UPDATE subscriptions
SET plan_id = $2,
    current_period_end = $3,
    next_order_at = $4,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateSubscriptionQuantity :one
UPDATE subscriptions
SET quantity = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: LockSubscriptionForCustomer :one
-- A customer's edit reads the row under a lock. A renewal claims before it
-- reads, and its claim is an UPDATE, so the two serialise on this row: either
-- the edit sees the claim and refuses, or the renewal waits and reads the edit.
SELECT * FROM subscriptions
WHERE id = $1 AND customer_id = $2
FOR UPDATE;

-- name: LockSubscriptionBox :many
-- Every member of one box, locked: the customer's active and past-due
-- subscriptions at one address on one renewal instant (domain.GroupIntoBoxes).
-- Adding to a box holds these so a renewal cannot claim the box between the
-- check and the insert.
SELECT * FROM subscriptions
WHERE customer_id = @customer_id
  AND shipping_address_id = @shipping_address_id
  AND next_order_at = @next_order_at
  AND status IN ('active', 'past_due')
ORDER BY id
FOR UPDATE;

-- name: UpdateSubscriptionStripePaymentMethodID :exec
UPDATE subscriptions
SET stripe_payment_method_id = $2, updated_at = now()
WHERE id = $1;

-- name: CancelSubscription :exec
UPDATE subscriptions
SET status = 'cancelled', cancelled_at = now(), updated_at = now()
WHERE id = $1;

-- name: ListSubscriptionsDueForRenewal :many
-- Picks up both fresh active renewals and past_due dunning retries: for a
-- past_due subscription next_order_at is the next dunning retry time, pushed
-- forward on each failed charge so this never spins. Exhausted subscriptions
-- flip to 'expired' (ends_at set) and drop out here.
SELECT * FROM subscriptions
WHERE status IN ('active', 'past_due') AND next_order_at <= now()
  AND (ends_at IS NULL OR ends_at > now())
ORDER BY next_order_at ASC;

-- name: ListSubscriptionsByOrder :many
-- Every subscription an order started or renewed. orders.subscription_id names
-- one; an order that covers several leaves it null, and this join is the link.
SELECT s.* FROM subscriptions s
JOIN subscription_orders so ON so.subscription_id = s.id
WHERE so.order_id = $1
ORDER BY s.created_at, s.id;

-- name: CreateSubscriptionOrder :exec
INSERT INTO subscription_orders (subscription_id, order_id, period_start, period_end)
VALUES ($1, $2, $3, $4);

-- name: ListSubscriptionOrdersBySubscription :many
SELECT * FROM subscription_orders
WHERE subscription_id = $1
ORDER BY period_start DESC;

-- name: CountLiveSubscriptionsAtAddress :one
-- Subscriptions that will still renew to this address: everything not
-- cancelled or expired. Paused ones count, because they resume to it.
SELECT count(*) FROM subscriptions
WHERE customer_id = @customer_id
  AND shipping_address_id = @shipping_address_id
  AND status IN ('active', 'paused', 'past_due');

-- name: MoveLiveSubscriptionsToAddress :many
-- Moves every live subscription at one address to another, together, so the
-- box they renew in (grouped by address) stays one box.
UPDATE subscriptions
SET shipping_address_id = @to_address_id, updated_at = now()
WHERE customer_id = @customer_id
  AND shipping_address_id = @from_address_id
  AND status IN ('active', 'paused', 'past_due')
RETURNING id;
