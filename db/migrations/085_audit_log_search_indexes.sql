-- +goose Up
-- The audit log grew a real filter bar (search, actor, area, resource, date
-- range). Every dimension it offers needs an index or the page turns into a
-- sequential scan over the whole history the first time somebody uses it.

CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- "Show me everything that happened to orders." The existing composite leads
-- with (resource_type, resource_id), which cannot serve a resource_type-only
-- filter ordered by date — the leading column matches, but the sort key is
-- three positions away.
CREATE INDEX idx_audit_log_resource_type ON audit_log (resource_type, created_at DESC);

-- "What did this person do?", asked without knowing whether they were staff or
-- a customer. idx_audit_log_actor leads with actor_type, so actor_id on its own
-- cannot use it. Partial because system events carry no actor id at all.
CREATE INDEX idx_audit_log_actor_id ON audit_log (actor_id, created_at DESC)
    WHERE actor_id IS NOT NULL;

-- The area filter groups the ~250 action constants by their namespace
-- ("order.refunded" -> "order"), which is the only way a dropdown of them stays
-- readable. Matching on split_part rather than a LIKE prefix is what keeps it
-- indexable: a left-anchored LIKE cannot use the plain (action, created_at)
-- btree under this database's non-C collation. split_part is immutable, so it
-- is legal in an index expression.
CREATE INDEX idx_audit_log_action_area ON audit_log (split_part(action, '.', 1), created_at DESC);

-- Free-text search is an ILIKE '%term%' across the actor's name and the action.
-- A leading wildcard defeats a btree; trigram GIN is what makes it survivable.
CREATE INDEX idx_audit_log_actor_name_trgm ON audit_log USING gin (actor_name gin_trgm_ops);
CREATE INDEX idx_audit_log_action_trgm ON audit_log USING gin (action gin_trgm_ops);

-- The list renders the first 8 characters of each resource id, so staff paste
-- that fragment back into the search box. text_pattern_ops makes the resulting
-- prefix LIKE an index range scan instead of a full scan — without it we would
-- be displaying an identifier that cannot be searched for.
CREATE INDEX idx_audit_log_resource_id_prefix ON audit_log ((resource_id::text) text_pattern_ops);

-- +goose Down
DROP INDEX IF EXISTS idx_audit_log_resource_id_prefix;
DROP INDEX IF EXISTS idx_audit_log_action_trgm;
DROP INDEX IF EXISTS idx_audit_log_actor_name_trgm;
DROP INDEX IF EXISTS idx_audit_log_action_area;
DROP INDEX IF EXISTS idx_audit_log_actor_id;
DROP INDEX IF EXISTS idx_audit_log_resource_type;
