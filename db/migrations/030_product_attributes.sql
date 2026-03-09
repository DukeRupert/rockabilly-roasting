-- +goose Up

CREATE TYPE attribute_value_type AS ENUM ('single', 'multi');

-- Named groups of attribute keys
CREATE TABLE attribute_sets (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    slug        text NOT NULL UNIQUE,
    position    int  NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Individual attributes within a set
CREATE TABLE attribute_keys (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    attribute_set_id uuid NOT NULL REFERENCES attribute_sets(id) ON DELETE CASCADE,
    name             text NOT NULL,
    slug             text NOT NULL,
    value_type       attribute_value_type NOT NULL DEFAULT 'single',
    position         int  NOT NULL DEFAULT 0,
    filterable       bool NOT NULL DEFAULT false,
    sortable         bool NOT NULL DEFAULT false,
    CONSTRAINT uq_attribute_key_slug UNIQUE (attribute_set_id, slug)
);

-- Which attribute sets apply to a product (many-to-many)
CREATE TABLE product_attribute_sets (
    product_id       uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    attribute_set_id uuid NOT NULL REFERENCES attribute_sets(id) ON DELETE CASCADE,
    PRIMARY KEY (product_id, attribute_set_id)
);

-- Per-product values for each attribute key
CREATE TABLE product_attribute_values (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id       uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    attribute_key_id uuid NOT NULL REFERENCES attribute_keys(id) ON DELETE CASCADE,
    value            text,
    values           jsonb,
    CONSTRAINT uq_product_attribute UNIQUE (product_id, attribute_key_id)
);

-- Indexes
CREATE INDEX idx_attr_values_product ON product_attribute_values (product_id);
CREATE INDEX idx_attr_values_key     ON product_attribute_values (attribute_key_id);
CREATE INDEX idx_attr_values_multi   ON product_attribute_values USING gin (values)
    WHERE values IS NOT NULL;
CREATE INDEX idx_attr_keys_set       ON attribute_keys (attribute_set_id, position);
CREATE INDEX idx_attr_sets_position  ON attribute_sets (position);

-- +goose Down
DROP TABLE IF EXISTS product_attribute_values;
DROP TABLE IF EXISTS product_attribute_sets;
DROP TABLE IF EXISTS attribute_keys;
DROP TABLE IF EXISTS attribute_sets;
DROP TYPE IF EXISTS attribute_value_type;
