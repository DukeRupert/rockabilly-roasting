-- name: CreateInvoice :one
INSERT INTO invoices (id, order_id, number, status, subtotal, shipping, tax_total, total,
                      due_date, notes, internal_note, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: GetInvoiceByID :one
SELECT * FROM invoices WHERE id = $1;

-- name: ListInvoicesByOrder :many
SELECT * FROM invoices WHERE order_id = $1 ORDER BY created_at DESC;

-- name: UpdateInvoiceStatus :one
UPDATE invoices
SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateInvoiceSent :one
UPDATE invoices
SET status = 'sent', sent_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateInvoicePaid :one
UPDATE invoices
SET status = 'paid', paid_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateInvoiceVoided :one
UPDATE invoices
SET status = 'void', voided_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateInvoiceAmountPaid :one
UPDATE invoices
SET amount_paid = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CreateInvoiceLine :one
INSERT INTO invoice_lines (id, invoice_id, variant_id, description, quantity, unit_price, total)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListInvoiceLinesByInvoice :many
SELECT * FROM invoice_lines WHERE invoice_id = $1 ORDER BY id;

-- name: CreateInvoicePayment :one
INSERT INTO invoice_payments (id, invoice_id, amount, method, reference, note, recorded_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListInvoicePaymentsByInvoice :many
SELECT * FROM invoice_payments WHERE invoice_id = $1 ORDER BY paid_at;

-- name: SumInvoicePayments :one
SELECT COALESCE(SUM(amount), 0)::int AS total_paid
FROM invoice_payments
WHERE invoice_id = $1;

-- name: NextInvoiceNumber :one
SELECT COALESCE(MAX(CAST(SUBSTRING(number FROM 5) AS integer)), 0) + 1 AS next_num
FROM invoices
WHERE number LIKE 'INV-%';

-- name: UpdateProductVisibility :one
UPDATE products
SET visibility = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateVariantWholesale :one
UPDATE variants
SET wholesale_min_qty = $2, wholesale_multiple = $3, updated_at = now()
WHERE id = $1
RETURNING *;

