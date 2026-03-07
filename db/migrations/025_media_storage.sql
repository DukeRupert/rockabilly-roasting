-- +goose Up

-- Rename url to cf_image_id on product_media: we now store the
-- Cloudflare Images image ID and construct URLs at render time.
ALTER TABLE product_media RENAME COLUMN url TO cf_image_id;

-- Add R2 label storage columns to shipments.
-- Nullable because existing shipments still reference EasyPost URLs only.
ALTER TABLE shipments
    ADD COLUMN label_r2_key  text,
    ADD COLUMN label_format  text CHECK (label_format IN ('pdf', 'png'));

-- +goose Down
ALTER TABLE shipments
    DROP COLUMN label_format,
    DROP COLUMN label_r2_key;

ALTER TABLE product_media RENAME COLUMN cf_image_id TO url;
