-- name: CreateAuditEntry :one
INSERT INTO audit_log (id, actor_type, actor_id, actor_name, action, resource_type, resource_id,
                       after_snapshot, request_id, ip_address, reason, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: ListAuditByResource :many
SELECT * FROM audit_log
WHERE resource_type = $1 AND resource_id = $2
ORDER BY created_at DESC;

-- name: ListAuditByActor :many
SELECT * FROM audit_log
WHERE actor_id = $1
ORDER BY created_at DESC;

-- name: ListAuditByAction :many
SELECT * FROM audit_log
WHERE action = $1
ORDER BY created_at DESC;
