-- +goose Up

-- Let a task come off a plan without taking its history with it.
--
-- Until now a task that had ever been used could not be removed at all. Deleting
-- it cascades service_maintenance_due, which would erase completed visits and
-- skips — so the delete was refused outright. That left no way out: when a
-- manufacturer revises a procedure or the shop stops offering it, the task kept
-- generating due rows on every assigned machine forever, and the only escape was
-- retiring the whole plan and orphaning the very history the refusal protected.
--
-- retired_at stops it generating new work while every past occurrence stays
-- exactly where it is. Nullable and null by default, so nothing existing changes.
--
-- A timestamp rather than an active flag, for two reasons. equipment_service_plans
-- already ends assignments with ended_at and equipment retires by status, both
-- tested in the same WHERE clause in ListMissingDue — tasks were the odd one out.
-- And "since when" is the question staff ask when a machine stops getting a job
-- it used to get; a boolean cannot answer it.
ALTER TABLE service_plan_tasks
    ADD COLUMN retired_at timestamptz;

COMMENT ON COLUMN service_plan_tasks.retired_at IS
    'When this task stopped generating work, or NULL while it is live. Set deliberately by a staff action, never by a date somebody types — a nullable timestamp that silently stops something is how subscriptions stopped billing (migration 075).';

-- Partial index: every read of a plan's live series filters on this, and the
-- retired rows are the minority that never need scanning.
CREATE INDEX service_plan_tasks_live_idx
    ON service_plan_tasks (plan_id, sort_order, created_at)
    WHERE retired_at IS NULL;

-- +goose Down

DROP INDEX service_plan_tasks_live_idx;
ALTER TABLE service_plan_tasks DROP COLUMN retired_at;
