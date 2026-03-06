-- +goose Up
ALTER TABLE subscriptions ADD COLUMN quantity int NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE subscriptions DROP COLUMN quantity;
