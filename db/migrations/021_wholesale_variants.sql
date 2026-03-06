-- +goose Up
ALTER TABLE variants
    ADD COLUMN wholesale_min_qty  int,
    ADD COLUMN wholesale_multiple int;

-- +goose Down
ALTER TABLE variants
    DROP COLUMN IF EXISTS wholesale_multiple,
    DROP COLUMN IF EXISTS wholesale_min_qty;
