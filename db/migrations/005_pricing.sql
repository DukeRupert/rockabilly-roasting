-- +goose Up
CREATE TABLE price_sets (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    variant_id  uuid NOT NULL UNIQUE REFERENCES variants(id) ON DELETE CASCADE
);

CREATE TABLE price_lists (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    type       text NOT NULL DEFAULT 'sale',
    status     text NOT NULL DEFAULT 'draft',
    starts_at  timestamptz,
    ends_at    timestamptz
);

CREATE TABLE prices (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    price_set_id       uuid NOT NULL REFERENCES price_sets(id) ON DELETE CASCADE,
    amount             int NOT NULL,
    currency_code      text NOT NULL,
    min_quantity       int,
    max_quantity       int,
    customer_group_id  uuid REFERENCES customer_groups(id) ON DELETE CASCADE,
    price_list_id      uuid REFERENCES price_lists(id) ON DELETE CASCADE,
    starts_at          timestamptz,
    ends_at            timestamptz
);

CREATE INDEX idx_prices_price_set_id ON prices(price_set_id);
CREATE INDEX idx_prices_customer_group_id ON prices(customer_group_id);
CREATE INDEX idx_prices_price_list_id ON prices(price_list_id);

-- +goose Down
DROP TABLE IF EXISTS prices;
DROP TABLE IF EXISTS price_lists;
DROP TABLE IF EXISTS price_sets;
