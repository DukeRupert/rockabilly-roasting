-- +goose Up
CREATE TABLE shipping_config (
    flat_rate_cents          int NOT NULL,
    free_shipping_threshold  int,
    currency                 text NOT NULL DEFAULT 'usd'
);

CREATE TABLE shipments (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id          uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    status            text NOT NULL DEFAULT 'pending',
    provider          text NOT NULL,
    tracking_number   text NOT NULL,
    label_url         text NOT NULL,
    carrier_name      text NOT NULL,
    service_name      text NOT NULL,
    label_cost_cents  int NOT NULL,
    label_currency    text NOT NULL,
    weight_oz         numeric NOT NULL,
    length_in         numeric NOT NULL,
    width_in          numeric NOT NULL,
    height_in         numeric NOT NULL,
    created_by        uuid NOT NULL REFERENCES staff(id) ON DELETE RESTRICT,
    created_at        timestamptz NOT NULL DEFAULT now(),
    label_created_at  timestamptz,
    shipped_at        timestamptz,
    delivered_at      timestamptz
);

CREATE INDEX idx_shipments_order_id ON shipments(order_id);

-- +goose Down
DROP TABLE IF EXISTS shipments;
DROP TABLE IF EXISTS shipping_config;
