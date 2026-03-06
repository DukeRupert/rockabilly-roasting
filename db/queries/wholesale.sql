-- name: CreateWholesaleCustomer :one
INSERT INTO customers (id, email, password_hash, first_name, last_name, phone,
                       account_type, wholesale_status, company_name, website)
VALUES ($1, $2, $3, $4, $5, $6, 'wholesale', 'pending', $7, $8)
RETURNING *;

-- name: ApproveWholesaleCustomer :one
UPDATE customers
SET wholesale_status = 'approved', approved_at = now(), approved_by = $2, updated_at = now()
WHERE id = $1 AND account_type = 'wholesale'
RETURNING *;

-- name: SuspendWholesaleCustomer :one
UPDATE customers
SET wholesale_status = 'suspended', updated_at = now()
WHERE id = $1 AND account_type = 'wholesale'
RETURNING *;

-- name: UpdateWholesaleNotes :exec
UPDATE customers
SET wholesale_notes = $2, updated_at = now()
WHERE id = $1 AND account_type = 'wholesale';

-- name: ListWholesaleByStatus :many
SELECT * FROM customers
WHERE account_type = 'wholesale' AND wholesale_status = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountWholesaleByStatus :one
SELECT count(*) FROM customers
WHERE account_type = 'wholesale' AND wholesale_status = $1;
