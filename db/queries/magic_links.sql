-- name: CreateMagicLinkToken :one
INSERT INTO magic_link_tokens (id, customer_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: RedeemMagicLinkToken :one
UPDATE magic_link_tokens
SET used_at = now()
WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
RETURNING *;

-- name: DeleteExpiredMagicLinkTokens :exec
DELETE FROM magic_link_tokens
WHERE expires_at < now() OR used_at IS NOT NULL;
