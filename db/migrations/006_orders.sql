-- +goose Up
CREATE TABLE carts (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id             uuid REFERENCES customers(id) ON DELETE SET NULL,
    currency_code           text NOT NULL,
    shipping_address_id     uuid REFERENCES addresses(id) ON DELETE RESTRICT,
    billing_address_id      uuid REFERENCES addresses(id) ON DELETE RESTRICT,
    applied_discount_id     uuid,
    applied_coupon_code_id  uuid,
    metadata                jsonb NOT NULL DEFAULT '{}',
    expires_at              timestamptz,
    created_at              timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_carts_customer_id ON carts(customer_id);

CREATE TABLE orders (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    number              text NOT NULL UNIQUE,
    customer_id         uuid REFERENCES customers(id) ON DELETE RESTRICT,
    status              text NOT NULL DEFAULT 'pending',
    payment_status      text NOT NULL DEFAULT 'awaiting',
    fulfillment_status  text NOT NULL DEFAULT 'unfulfilled',
    currency_code       text NOT NULL,
    subtotal            int NOT NULL,
    discount_total      int NOT NULL DEFAULT 0,
    shipping_total      int NOT NULL DEFAULT 0,
    tax_total           int NOT NULL DEFAULT 0,
    total               int NOT NULL,
    shipping_address_id uuid NOT NULL REFERENCES addresses(id) ON DELETE RESTRICT,
    billing_address_id  uuid NOT NULL REFERENCES addresses(id) ON DELETE RESTRICT,
    subscription_id     uuid,
    draft_by_user_id    uuid REFERENCES staff(id) ON DELETE SET NULL,
    tax_exempt          boolean NOT NULL DEFAULT false,
    tax_exempt_reason   text,
    stripe_tax_id       text,
    notes               text,
    metadata            jsonb NOT NULL DEFAULT '{}',
    placed_at           timestamptz NOT NULL DEFAULT now(),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_orders_customer_id ON orders(customer_id);
CREATE INDEX idx_orders_status ON orders(status);
CREATE INDEX idx_orders_placed_at ON orders(placed_at);

CREATE TABLE line_items (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id        uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    variant_id      uuid NOT NULL REFERENCES variants(id) ON DELETE RESTRICT,
    quantity        int NOT NULL,
    unit_price      int NOT NULL,
    subtotal        int NOT NULL,
    discount_total  int NOT NULL DEFAULT 0,
    tax_total       int NOT NULL DEFAULT 0,
    total           int NOT NULL,
    metadata        jsonb NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_line_items_order_id ON line_items(order_id);

CREATE TABLE adjustments (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id      uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    line_item_id  uuid REFERENCES line_items(id) ON DELETE CASCADE,
    label         text NOT NULL,
    amount        int NOT NULL,
    source_type   text NOT NULL,
    source_id     uuid NOT NULL
);

CREATE INDEX idx_adjustments_order_id ON adjustments(order_id);

-- +goose Down
DROP TABLE IF EXISTS adjustments;
DROP TABLE IF EXISTS line_items;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS carts;
