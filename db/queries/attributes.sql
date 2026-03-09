-- name: CreateAttributeSet :one
INSERT INTO attribute_sets (id, name, slug, position)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetAttributeSetByID :one
SELECT * FROM attribute_sets WHERE id = $1;

-- name: ListAttributeSets :many
SELECT * FROM attribute_sets ORDER BY position, name;

-- name: UpdateAttributeSet :one
UPDATE attribute_sets
SET name = $2, slug = $3, position = $4
WHERE id = $1
RETURNING *;

-- name: DeleteAttributeSet :exec
DELETE FROM attribute_sets WHERE id = $1;

-- name: CreateAttributeKey :one
INSERT INTO attribute_keys (id, attribute_set_id, name, slug, value_type, position, filterable, sortable)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetAttributeKeyByID :one
SELECT * FROM attribute_keys WHERE id = $1;

-- name: ListAttributeKeysBySet :many
SELECT * FROM attribute_keys
WHERE attribute_set_id = $1
ORDER BY position;

-- name: UpdateAttributeKey :one
UPDATE attribute_keys
SET name = $2, slug = $3, value_type = $4, position = $5, filterable = $6, sortable = $7
WHERE id = $1
RETURNING *;

-- name: DeleteAttributeKey :exec
DELETE FROM attribute_keys WHERE id = $1;

-- name: AssignAttributeSetToProduct :exec
INSERT INTO product_attribute_sets (product_id, attribute_set_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: RemoveAttributeSetFromProduct :exec
DELETE FROM product_attribute_sets
WHERE product_id = $1 AND attribute_set_id = $2;

-- name: ListProductAttributeSets :many
SELECT as2.*
FROM attribute_sets as2
JOIN product_attribute_sets pas ON pas.attribute_set_id = as2.id
WHERE pas.product_id = $1
ORDER BY as2.position, as2.name;

-- name: UpsertProductAttributeValue :exec
INSERT INTO product_attribute_values (id, product_id, attribute_key_id, value, values)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (product_id, attribute_key_id)
DO UPDATE SET value = EXCLUDED.value, values = EXCLUDED.values;

-- name: DeleteProductAttributeValuesByProduct :exec
DELETE FROM product_attribute_values WHERE product_id = $1;

-- name: DeleteProductAttributeValuesByKey :exec
DELETE FROM product_attribute_values WHERE attribute_key_id = $1;
