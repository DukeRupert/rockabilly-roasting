-- name: CreateCustomerUser :one
INSERT INTO customer_users (id, customer_id, email, name, role, receives_notifications)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetCustomerUserByID :one
SELECT * FROM customer_users
WHERE id = $1;

-- name: GetCustomerUserByEmail :one
SELECT * FROM customer_users
WHERE email = $1;

-- GetCustomerUserForCustomer is the ownership-scoped fetch: a caller holding an
-- account id cannot read a member of some other account.
-- name: GetCustomerUserForCustomer :one
SELECT * FROM customer_users
WHERE id = $1 AND customer_id = $2;

-- name: ListCustomerUsersByCustomer :many
SELECT * FROM customer_users
WHERE customer_id = $1
ORDER BY created_at;

-- ListNotifiedCustomerUsers returns the additional recipients for an account's
-- transactional mail. The account's primary contact (customers.email) is not in
-- this table and is added by the caller.
-- name: ListNotifiedCustomerUsers :many
SELECT * FROM customer_users
WHERE customer_id = $1
  AND receives_notifications = true
ORDER BY created_at;

-- name: UpdateCustomerUserPassword :exec
UPDATE customer_users
SET password_hash = $2, updated_at = now()
WHERE id = $1;

-- name: UpdateCustomerUserNotifications :exec
UPDATE customer_users
SET receives_notifications = $2, updated_at = now()
WHERE id = $1;

-- name: TouchCustomerUserLastLogin :exec
UPDATE customer_users
SET last_login_at = now(), updated_at = now()
WHERE id = $1;

-- DeleteCustomerUser is scoped by customer_id as well as id so a caller cannot
-- revoke a member of an account it does not own — the same ownership-by-query
-- discipline the customer-scoped store methods use.
-- name: DeleteCustomerUser :execrows
DELETE FROM customer_users
WHERE id = $1 AND customer_id = $2;

-- name: CreateCustomerUserInviteToken :one
INSERT INTO customer_user_invite_tokens (id, customer_user_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: RedeemCustomerUserInviteToken :one
UPDATE customer_user_invite_tokens
SET used_at = now()
WHERE token_hash = $1
  AND used_at IS NULL
  AND expires_at > now()
RETURNING *;

-- name: GetValidCustomerUserInviteToken :one
SELECT * FROM customer_user_invite_tokens
WHERE token_hash = $1
  AND used_at IS NULL
  AND expires_at > now();

-- name: DeleteExpiredCustomerUserInviteTokens :exec
DELETE FROM customer_user_invite_tokens
WHERE expires_at < now() OR used_at IS NOT NULL;
