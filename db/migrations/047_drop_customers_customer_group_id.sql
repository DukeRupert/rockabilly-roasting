-- +goose Up
-- The single customer_group_id column is superseded by the customer_group_memberships
-- join table, which is the source of truth for a customer's group access. Nothing reads
-- this column post product-access feature; drop it.
ALTER TABLE customers DROP COLUMN IF EXISTS customer_group_id;

-- +goose Down
ALTER TABLE customers
    ADD COLUMN customer_group_id uuid REFERENCES customer_groups(id) ON DELETE SET NULL;
