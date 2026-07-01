-- name: CreateStaffInviteToken :one
INSERT INTO staff_invite_tokens (id, staff_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: RedeemStaffInviteToken :one
UPDATE staff_invite_tokens
SET used_at = now()
WHERE token_hash = $1
  AND used_at IS NULL
  AND expires_at > now()
RETURNING *;

-- name: GetValidStaffInviteToken :one
SELECT * FROM staff_invite_tokens
WHERE token_hash = $1
  AND used_at IS NULL
  AND expires_at > now();

-- name: DeleteExpiredStaffInviteTokens :exec
DELETE FROM staff_invite_tokens
WHERE expires_at < now() OR used_at IS NOT NULL;
