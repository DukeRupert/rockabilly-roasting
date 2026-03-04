-- name: CreateWebhookEvent :one
INSERT INTO webhook_events (id, provider, event_id, event_type, payload)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (provider, event_id) DO NOTHING
RETURNING *;

-- name: GetWebhookEventByProviderAndEventID :one
SELECT * FROM webhook_events
WHERE provider = $1 AND event_id = $2;

-- name: MarkWebhookEventProcessed :exec
UPDATE webhook_events
SET processed_at = now()
WHERE id = $1;

-- name: MarkWebhookEventFailed :exec
UPDATE webhook_events
SET failed_at = now(), failure_reason = $2
WHERE id = $1;

-- name: ListUnprocessedWebhookEvents :many
SELECT * FROM webhook_events
WHERE processed_at IS NULL AND failed_at IS NULL
ORDER BY created_at ASC;
