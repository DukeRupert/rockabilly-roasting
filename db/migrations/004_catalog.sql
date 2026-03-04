-- +goose Up
CREATE TABLE taxons (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id  uuid REFERENCES taxons(id) ON DELETE CASCADE,
    name       text NOT NULL,
    slug       text NOT NULL,
    position   int NOT NULL DEFAULT 0,
    depth      int NOT NULL DEFAULT 0
);

CREATE INDEX idx_taxons_parent_id ON taxons(parent_id);
CREATE UNIQUE INDEX idx_taxons_slug ON taxons(slug);

CREATE TABLE products (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug             text NOT NULL UNIQUE,
    title            text NOT NULL,
    description      text NOT NULL DEFAULT '',
    status           text NOT NULL DEFAULT 'draft',
    product_type_id  uuid,
    taxon_id         uuid REFERENCES taxons(id) ON DELETE RESTRICT,
    metadata         jsonb NOT NULL DEFAULT '{}',
    available_on     timestamptz,
    discontinue_on   timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_products_taxon_id ON products(taxon_id);
CREATE INDEX idx_products_status ON products(status);

CREATE TABLE product_options (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id  uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    name        text NOT NULL,
    position    int NOT NULL DEFAULT 0
);

CREATE INDEX idx_product_options_product_id ON product_options(product_id);

CREATE TABLE product_option_values (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    product_option_id   uuid NOT NULL REFERENCES product_options(id) ON DELETE CASCADE,
    value               text NOT NULL,
    position            int NOT NULL DEFAULT 0
);

CREATE INDEX idx_product_option_values_option_id ON product_option_values(product_option_id);

CREATE TABLE variants (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id    uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    sku           text NOT NULL UNIQUE,
    barcode       text,
    position      int NOT NULL DEFAULT 0,
    is_default    boolean NOT NULL DEFAULT false,
    weight_grams  int,
    metadata      jsonb NOT NULL DEFAULT '{}',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_variants_product_id ON variants(product_id);

CREATE TABLE variant_option_values (
    variant_id               uuid NOT NULL REFERENCES variants(id) ON DELETE CASCADE,
    product_option_value_id  uuid NOT NULL REFERENCES product_option_values(id) ON DELETE CASCADE,
    PRIMARY KEY (variant_id, product_option_value_id)
);

CREATE TABLE product_media (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id  uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    variant_id  uuid REFERENCES variants(id) ON DELETE SET NULL,
    url         text NOT NULL,
    alt_text    text NOT NULL DEFAULT '',
    position    int NOT NULL DEFAULT 0,
    media_type  text NOT NULL DEFAULT 'image'
);

CREATE INDEX idx_product_media_product_id ON product_media(product_id);

-- +goose Down
DROP TABLE IF EXISTS product_media;
DROP TABLE IF EXISTS variant_option_values;
DROP TABLE IF EXISTS variants;
DROP TABLE IF EXISTS product_option_values;
DROP TABLE IF EXISTS product_options;
DROP TABLE IF EXISTS products;
DROP TABLE IF EXISTS taxons;
