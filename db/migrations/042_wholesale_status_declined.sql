-- +goose Up
ALTER TABLE customers
    DROP CONSTRAINT customers_wholesale_status_check,
    ADD  CONSTRAINT customers_wholesale_status_check
        CHECK (wholesale_status IN ('pending', 'approved', 'suspended', 'declined'));

-- +goose Down
ALTER TABLE customers
    DROP CONSTRAINT customers_wholesale_status_check,
    ADD  CONSTRAINT customers_wholesale_status_check
        CHECK (wholesale_status IN ('pending', 'approved', 'suspended'));
