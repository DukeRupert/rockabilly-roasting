-- name: CreateSession :one
INSERT INTO sessions (id, actor_type, actor_id, token_hash, ip_address, user_agent, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetSessionByTokenHash :one
SELECT * FROM sessions
WHERE token_hash = $1 AND expires_at > now();

-- name: UpdateSessionLastSeen :exec
UPDATE sessions SET last_seen_at = now() WHERE id = $1;

-- name: RevokeSession :exec
DELETE FROM sessions WHERE id = $1;

-- name: RevokeAllSessionsForActor :exec
DELETE FROM sessions WHERE actor_type = $1 AND actor_id = $2;

-- name: PruneExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= now();

-- name: CreateResetToken :one
INSERT INTO reset_tokens (id, actor_type, actor_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetResetTokenByTokenHash :one
SELECT * FROM reset_tokens
WHERE token_hash = $1 AND expires_at > now() AND used_at IS NULL;

-- name: MarkResetTokenUsed :exec
UPDATE reset_tokens SET used_at = now() WHERE id = $1;

-- name: CreateEmailVerification :one
INSERT INTO email_verifications (id, customer_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetEmailVerificationByTokenHash :one
SELECT * FROM email_verifications
WHERE token_hash = $1 AND expires_at > now() AND verified_at IS NULL;

-- name: MarkEmailVerified :exec
UPDATE email_verifications SET verified_at = now() WHERE id = $1;
