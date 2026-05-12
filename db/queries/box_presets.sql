-- name: ListBoxPresets :many
SELECT * FROM box_presets
ORDER BY sort_order ASC, max_weight_oz ASC;

-- name: ListBoxPresetsByMaxWeightAsc :many
SELECT * FROM box_presets
ORDER BY max_weight_oz ASC;

-- name: GetBoxPresetByID :one
SELECT * FROM box_presets WHERE id = $1;

-- name: CreateBoxPreset :one
INSERT INTO box_presets (id, name, length_in, width_in, height_in, max_weight_oz, sort_order)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateBoxPreset :one
UPDATE box_presets
SET name = $2,
    length_in = $3,
    width_in = $4,
    height_in = $5,
    max_weight_oz = $6,
    sort_order = $7,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteBoxPreset :exec
DELETE FROM box_presets WHERE id = $1;
