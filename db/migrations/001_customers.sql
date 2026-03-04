-- +goose Up
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE customer_groups (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    metadata    jsonb NOT NULL DEFAULT '{}'
);

CREATE TABLE customers (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email               text NOT NULL UNIQUE,
    email_verified      boolean NOT NULL DEFAULT false,
    password_hash       text,
    first_name          text NOT NULL,
    last_name           text NOT NULL,
    phone               text,
    is_guest            boolean NOT NULL DEFAULT false,
    tax_exempt          boolean NOT NULL DEFAULT false,
    tax_exempt_reason   text,
    customer_group_id   uuid REFERENCES customer_groups(id) ON DELETE SET NULL,
    metadata            jsonb NOT NULL DEFAULT '{}',
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE customer_group_memberships (
    customer_id       uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    customer_group_id uuid NOT NULL REFERENCES customer_groups(id) ON DELETE CASCADE,
    assigned_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, customer_group_id)
);

CREATE TABLE addresses (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id   uuid REFERENCES customers(id) ON DELETE CASCADE,
    first_name    text NOT NULL,
    last_name     text NOT NULL,
    company       text,
    line1         text NOT NULL,
    line2         text,
    city          text NOT NULL,
    state         text NOT NULL,
    postal_code   text NOT NULL,
    country_code  text NOT NULL,
    is_default    boolean NOT NULL DEFAULT false
);

CREATE INDEX idx_addresses_customer_id ON addresses(customer_id);

-- +goose Down
DROP TABLE IF EXISTS addresses;
DROP TABLE IF EXISTS customer_group_memberships;
DROP TABLE IF EXISTS customers;
DROP TABLE IF EXISTS customer_groups;
DROP EXTENSION IF EXISTS "pgcrypto";
