-- +goose Up
ALTER TABLE orders
    ADD COLUMN customer_po_number text,
    ADD COLUMN internal_note      text;

-- +goose Down
ALTER TABLE orders
    DROP COLUMN IF EXISTS internal_note,
    DROP COLUMN IF EXISTS customer_po_number;
