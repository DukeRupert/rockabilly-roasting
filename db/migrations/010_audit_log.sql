-- +goose Up
CREATE TABLE audit_log (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_type      text NOT NULL,
    actor_id        uuid,
    actor_name      text NOT NULL,
    action          text NOT NULL,
    resource_type   text NOT NULL,
    resource_id     uuid NOT NULL,
    after_snapshot  jsonb NOT NULL DEFAULT '{}',
    request_id      text NOT NULL DEFAULT '',
    ip_address      text,
    reason          text,
    metadata        jsonb NOT NULL DEFAULT '{}',
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_log_resource ON audit_log(resource_type, resource_id);
CREATE INDEX idx_audit_log_actor_id ON audit_log(actor_id);
CREATE INDEX idx_audit_log_action ON audit_log(action);
CREATE INDEX idx_audit_log_created_at ON audit_log(created_at);

-- +goose Down
DROP TABLE IF EXISTS audit_log;
