-- name: CreateCustomer :one
INSERT INTO customers (id, email, password_hash, first_name, last_name, phone)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetCustomerByID :one
SELECT * FROM customers WHERE id = $1;

-- name: GetCustomerByEmail :one
SELECT * FROM customers WHERE email = $1;

-- name: ListCustomers :many
SELECT * FROM customers ORDER BY created_at DESC;

-- name: UpdateCustomerEmail :one
UPDATE customers
SET email = $2, email_verified = false, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateCustomerPassword :exec
UPDATE customers
SET password_hash = $2, updated_at = now()
WHERE id = $1;

-- name: UpdateCustomerEmailVerified :exec
UPDATE customers
SET email_verified = $2, updated_at = now()
WHERE id = $1;

-- name: UpdateCustomerTaxExempt :exec
UPDATE customers
SET tax_exempt = $2, tax_exempt_reason = $3, updated_at = now()
WHERE id = $1;

-- name: UpdateCustomerGroup :exec
UPDATE customers
SET customer_group_id = $2, updated_at = now()
WHERE id = $1;

-- name: UpdateCustomerStripeCustomerID :one
UPDATE customers
SET stripe_customer_id = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetCustomerByStripeCustomerID :one
SELECT * FROM customers WHERE stripe_customer_id = $1;

-- name: DeleteCustomer :exec
DELETE FROM customers WHERE id = $1;

-- name: CreateAddress :one
INSERT INTO addresses (id, customer_id, first_name, last_name, company, line1, line2,
                       city, state, postal_code, country_code, is_default)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: GetAddress :one
SELECT * FROM addresses WHERE id = $1 AND customer_id = $2;

-- name: GetAddressByID :one
SELECT * FROM addresses WHERE id = $1;

-- name: ListAddresses :many
SELECT * FROM addresses WHERE customer_id = $1 ORDER BY is_default DESC, id;

-- name: DeleteAddress :exec
DELETE FROM addresses WHERE id = $1 AND customer_id = $2;
