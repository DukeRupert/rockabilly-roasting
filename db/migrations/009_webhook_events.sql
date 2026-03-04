-- +goose Up
CREATE TABLE webhook_events (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider        text NOT NULL,
    event_id        text NOT NULL,
    event_type      text NOT NULL,
    payload         jsonb NOT NULL DEFAULT '{}',
    processed_at    timestamptz,
    failed_at       timestamptz,
    failure_reason  text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, event_id)
);

CREATE INDEX idx_webhook_events_event_type ON webhook_events(event_type);

-- +goose Down
DROP TABLE IF EXISTS webhook_events;
