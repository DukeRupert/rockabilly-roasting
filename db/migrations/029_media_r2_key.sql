-- +goose Up

-- Rename cf_image_id to r2_key: images now stored in R2, not Cloudflare Images.
ALTER TABLE product_media RENAME COLUMN cf_image_id TO r2_key;

-- +goose Down
ALTER TABLE product_media RENAME COLUMN r2_key TO cf_image_id;
