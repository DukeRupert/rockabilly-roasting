-- name: CreateProduct :one
INSERT INTO products (id, slug, title, description, status, product_type_id, taxon_id, metadata, available_on, discontinue_on)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetProductByID :one
SELECT * FROM products WHERE id = $1;

-- name: GetProductBySlug :one
SELECT * FROM products WHERE slug = $1;

-- name: UpdateProduct :one
UPDATE products
SET slug = $2, title = $3, description = $4, product_type_id = $5, taxon_id = $6,
    metadata = $7, available_on = $8, discontinue_on = $9, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateProductStatus :one
UPDATE products
SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateProductSubscribable :one
UPDATE products
SET subscribable = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateProductTaxExempt :one
UPDATE products
SET tax_exempt = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateProductFeatured :one
UPDATE products
SET is_featured = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ClearOtherFeaturedProducts :exec
UPDATE products
SET is_featured = false, updated_at = now()
WHERE is_featured = true AND id <> $1;

-- name: DeleteProduct :exec
DELETE FROM products WHERE id = $1;

-- name: CreateVariant :one
INSERT INTO variants (id, product_id, sku, barcode, position, is_default, weight_grams, metadata, retail_available, wholesale_available)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetVariantByID :one
SELECT * FROM variants WHERE id = $1;

-- name: GetVariantBySKU :one
SELECT * FROM variants WHERE sku = $1;

-- name: ListVariantsByProduct :many
SELECT * FROM variants
WHERE product_id = $1
ORDER BY position;

-- name: ListActiveVariantsByProduct :many
SELECT * FROM variants
WHERE product_id = $1 AND archived_at IS NULL
ORDER BY position;

-- name: ClearDefaultVariants :exec
UPDATE variants SET is_default = false WHERE product_id = $1 AND is_default = true;

-- name: UpdateVariant :one
UPDATE variants
SET sku = $2, barcode = $3, position = $4, is_default = $5, weight_grams = $6,
    metadata = $7, retail_available = $8, wholesale_available = $9, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateVariantChannels :one
UPDATE variants
SET retail_available = $2, wholesale_available = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ArchiveVariant :one
UPDATE variants
SET archived_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UnarchiveVariant :one
UPDATE variants
SET archived_at = NULL, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteVariant :exec
DELETE FROM variants WHERE id = $1;

-- name: CreateProductOption :one
INSERT INTO product_options (id, product_id, name, position)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListProductOptionsByProduct :many
SELECT * FROM product_options
WHERE product_id = $1
ORDER BY position;

-- name: DeleteProductOption :exec
DELETE FROM product_options WHERE id = $1;

-- name: CreateProductOptionValue :one
INSERT INTO product_option_values (id, product_option_id, value, position)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListProductOptionValuesByOption :many
SELECT * FROM product_option_values
WHERE product_option_id = $1
ORDER BY position;

-- name: DeleteProductOptionValue :exec
DELETE FROM product_option_values WHERE id = $1;

-- name: CreateVariantOptionValue :exec
INSERT INTO variant_option_values (variant_id, product_option_value_id)
VALUES ($1, $2);

-- name: ListVariantOptionValuesByVariant :many
SELECT * FROM variant_option_values
WHERE variant_id = $1;

-- name: DeleteVariantOptionValuesByVariant :exec
DELETE FROM variant_option_values WHERE variant_id = $1;

-- name: CreateProductMedia :one
INSERT INTO product_media (id, product_id, variant_id, r2_key, alt_text, position, media_type)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetProductMediaByID :one
SELECT * FROM product_media WHERE id = $1;

-- name: ListProductMediaByProduct :many
SELECT * FROM product_media
WHERE product_id = $1
ORDER BY position;

-- name: UpdateProductMediaPosition :exec
UPDATE product_media SET position = $2 WHERE id = $1;

-- name: DeleteProductMedia :exec
DELETE FROM product_media WHERE id = $1;

-- name: CreateTaxon :one
INSERT INTO taxons (id, parent_id, name, slug, position, depth)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetTaxonByID :one
SELECT * FROM taxons WHERE id = $1;

-- name: GetTaxonBySlug :one
SELECT * FROM taxons WHERE slug = $1;

-- name: ListTaxonsByParent :many
SELECT * FROM taxons
WHERE parent_id = $1
ORDER BY position;

-- name: ListRootTaxons :many
SELECT * FROM taxons
WHERE parent_id IS NULL
ORDER BY position;

-- name: UpdateTaxon :one
UPDATE taxons
SET parent_id = $2, name = $3, slug = $4, position = $5, depth = $6
WHERE id = $1
RETURNING *;

-- name: DeleteTaxon :exec
DELETE FROM taxons WHERE id = $1;

-- name: SetProductCustomerVisibility :exec
INSERT INTO product_customer_visibility (product_id, customer_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: RemoveProductCustomerVisibility :exec
DELETE FROM product_customer_visibility
WHERE product_id = $1 AND customer_id = $2;

-- name: ListProductCustomerVisibility :many
SELECT customer_id FROM product_customer_visibility
WHERE product_id = $1;

-- name: ListVariantOptionLabels :many
-- Option values for a variant in display order ("Whole Bean", "12oz").
-- Empty for single-variant products with no options.
SELECT pov.value
FROM variant_option_values vov
JOIN product_option_values pov ON pov.id = vov.product_option_value_id
JOIN product_options po ON po.id = pov.product_option_id
WHERE vov.variant_id = $1
ORDER BY po.position, pov.position;
