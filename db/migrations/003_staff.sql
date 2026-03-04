-- +goose Up
CREATE TABLE staff (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email          text NOT NULL UNIQUE,
    name           text NOT NULL,
    password_hash  text NOT NULL,
    role           text NOT NULL CHECK (role IN ('admin', 'fulfillment', 'finance', 'catalog', 'support')),
    is_active      boolean NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS staff;
