-- +goose Up
-- Replace simple indexes with composite indexes that include created_at DESC
-- for optimal query ordering on the three primary audit query patterns.

DROP INDEX IF EXISTS idx_audit_log_resource;
DROP INDEX IF EXISTS idx_audit_log_actor_id;
DROP INDEX IF EXISTS idx_audit_log_action;

-- "What happened to this resource?"
CREATE INDEX idx_audit_log_resource ON audit_log (resource_type, resource_id, created_at DESC);

-- "What did this actor do recently?"
CREATE INDEX idx_audit_log_actor ON audit_log (actor_type, actor_id, created_at DESC);

-- "Show me all X actions in the last N days"
CREATE INDEX idx_audit_log_action ON audit_log (action, created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_audit_log_resource;
DROP INDEX IF EXISTS idx_audit_log_actor;
DROP INDEX IF EXISTS idx_audit_log_action;

CREATE INDEX idx_audit_log_resource ON audit_log (resource_type, resource_id);
CREATE INDEX idx_audit_log_actor_id ON audit_log (actor_id);
CREATE INDEX idx_audit_log_action ON audit_log (action);
