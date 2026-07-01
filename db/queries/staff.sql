-- name: GetStaffByID :one
SELECT * FROM staff WHERE id = $1;

-- name: GetStaffByEmail :one
SELECT * FROM staff WHERE email = $1;

-- name: ListStaff :many
SELECT * FROM staff ORDER BY name ASC LIMIT $1 OFFSET $2;

-- name: CountActiveStaffByRole :one
SELECT count(*) FROM staff WHERE role = $1 AND is_active = true;

-- name: CreateStaff :one
INSERT INTO staff (id, email, name, password_hash, role)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateStaffPassword :exec
UPDATE staff SET password_hash = $2, updated_at = now() WHERE id = $1;

-- name: UpdateStaffRole :exec
UPDATE staff SET role = $2, updated_at = now() WHERE id = $1;

-- name: UpdateStaffActive :exec
UPDATE staff SET is_active = $2, updated_at = now() WHERE id = $1;
