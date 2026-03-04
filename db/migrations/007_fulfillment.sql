-- +goose Up
CREATE TABLE stock_locations (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    address_id  uuid REFERENCES addresses(id) ON DELETE SET NULL,
    is_active   boolean NOT NULL DEFAULT true
);

CREATE TABLE inventory_items (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    variant_id         uuid NOT NULL UNIQUE REFERENCES variants(id) ON DELETE CASCADE,
    track_inventory    boolean NOT NULL DEFAULT true,
    requires_shipping  boolean NOT NULL DEFAULT true
);

CREATE TABLE stock_levels (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    inventory_item_id   uuid NOT NULL REFERENCES inventory_items(id) ON DELETE CASCADE,
    location_id         uuid NOT NULL REFERENCES stock_locations(id) ON DELETE CASCADE,
    quantity_on_hand    int NOT NULL DEFAULT 0,
    quantity_reserved   int NOT NULL DEFAULT 0,
    quantity_available  int GENERATED ALWAYS AS (quantity_on_hand - quantity_reserved) STORED,
    UNIQUE (inventory_item_id, location_id)
);

CREATE INDEX idx_stock_levels_inventory_item_id ON stock_levels(inventory_item_id);
CREATE INDEX idx_stock_levels_location_id ON stock_levels(location_id);

CREATE TABLE fulfillments (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id         uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    location_id      uuid NOT NULL REFERENCES stock_locations(id) ON DELETE RESTRICT,
    status           text NOT NULL DEFAULT 'pending',
    tracking_number  text,
    tracking_url     text,
    provider         text,
    shipped_at       timestamptz,
    delivered_at     timestamptz,
    metadata         jsonb NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_fulfillments_order_id ON fulfillments(order_id);

CREATE TABLE fulfillment_items (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    fulfillment_id  uuid NOT NULL REFERENCES fulfillments(id) ON DELETE CASCADE,
    line_item_id    uuid NOT NULL REFERENCES line_items(id) ON DELETE CASCADE,
    quantity        int NOT NULL
);

CREATE INDEX idx_fulfillment_items_fulfillment_id ON fulfillment_items(fulfillment_id);

-- +goose Down
DROP TABLE IF EXISTS fulfillment_items;
DROP TABLE IF EXISTS fulfillments;
DROP TABLE IF EXISTS stock_levels;
DROP TABLE IF EXISTS inventory_items;
DROP TABLE IF EXISTS stock_locations;
