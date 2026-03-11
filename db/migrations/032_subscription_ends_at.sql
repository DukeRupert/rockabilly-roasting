-- +goose Up
ALTER TABLE subscriptions ADD COLUMN ends_at timestamptz;

-- +goose Down
ALTER TABLE subscriptions DROP COLUMN ends_at;
