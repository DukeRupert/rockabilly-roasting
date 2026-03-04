-- +goose Up
CREATE TABLE subscription_plans (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name            text NOT NULL,
    interval        text NOT NULL,
    interval_count  int NOT NULL DEFAULT 1,
    variant_id      uuid NOT NULL REFERENCES variants(id) ON DELETE RESTRICT,
    price_set_id    uuid NOT NULL REFERENCES price_sets(id) ON DELETE RESTRICT,
    is_active       boolean NOT NULL DEFAULT true,
    metadata        jsonb NOT NULL DEFAULT '{}'
);

CREATE TABLE subscriptions (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id           uuid NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    plan_id               uuid NOT NULL REFERENCES subscription_plans(id) ON DELETE RESTRICT,
    status                text NOT NULL DEFAULT 'active',
    shipping_address_id   uuid NOT NULL REFERENCES addresses(id) ON DELETE RESTRICT,
    current_period_start  timestamptz NOT NULL,
    current_period_end    timestamptz NOT NULL,
    next_order_at         timestamptz NOT NULL,
    cancelled_at          timestamptz,
    pause_until           timestamptz,
    metadata              jsonb NOT NULL DEFAULT '{}',
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_subscriptions_customer_id ON subscriptions(customer_id);
CREATE INDEX idx_subscriptions_status ON subscriptions(status);
CREATE INDEX idx_subscriptions_next_order_at ON subscriptions(next_order_at);

-- Add the FK from orders.subscription_id now that subscriptions table exists.
ALTER TABLE orders
    ADD CONSTRAINT fk_orders_subscription_id
    FOREIGN KEY (subscription_id) REFERENCES subscriptions(id) ON DELETE SET NULL;

CREATE TABLE subscription_orders (
    subscription_id  uuid NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
    order_id         uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    period_start     timestamptz NOT NULL,
    period_end       timestamptz NOT NULL,
    PRIMARY KEY (subscription_id, order_id)
);

-- +goose Down
ALTER TABLE orders DROP CONSTRAINT IF EXISTS fk_orders_subscription_id;
DROP TABLE IF EXISTS subscription_orders;
DROP TABLE IF EXISTS subscriptions;
DROP TABLE IF EXISTS subscription_plans;
