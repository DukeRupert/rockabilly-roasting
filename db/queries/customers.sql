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

-- name: UpdateCustomerName :one
UPDATE customers
SET first_name = $2, last_name = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateCustomerEmail :one
UPDATE customers
SET email = $2, email_verified = false, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateCustomerPhone :one
UPDATE customers
SET phone = $2, updated_at = now()
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

-- name: UpdateCustomerPriceList :exec
UPDATE customers
SET price_list_id = $2, updated_at = now()
WHERE id = $1;

-- name: UpdateCustomerPaymentTerms :exec
UPDATE customers
SET payment_terms_days = $2, updated_at = now()
WHERE id = $1;

-- name: UpdateCustomerBillingMethod :exec
UPDATE customers
SET billing_method = $2, updated_at = now()
WHERE id = $1;

-- name: UpdateCustomerStripeCustomerID :one
UPDATE customers
SET stripe_customer_id = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetCustomerByStripeCustomerID :one
SELECT * FROM customers WHERE stripe_customer_id = $1;

-- name: UpdateCustomerPreferredLocalFulfillment :exec
UPDATE customers
SET preferred_local_fulfillment = $2, updated_at = now()
WHERE id = $1;

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

-- name: UpdateAddress :one
UPDATE addresses
SET first_name = $3, last_name = $4, company = $5, line1 = $6, line2 = $7,
    city = $8, state = $9, postal_code = $10, country_code = $11
WHERE id = $1 AND customer_id = $2
RETURNING *;

-- name: ClearDefaultAddresses :exec
UPDATE addresses SET is_default = false WHERE customer_id = $1 AND is_default = true;

-- name: SetDefaultAddress :exec
UPDATE addresses SET is_default = true WHERE id = $1 AND customer_id = $2;

-- name: CountAddresses :one
SELECT count(*) FROM addresses WHERE customer_id = $1;

-- name: DeleteAddress :exec
DELETE FROM addresses WHERE id = $1 AND customer_id = $2;
