-- +goose Up
-- Tracks the highest past-due reminder milestone (in days since the order was
-- placed) already notified for a wholesale QB invoice. The reconciliation poll
-- uses this as the dedup ledger so each milestone (7/14/21/30) sends exactly
-- one past-due email. 0 = no reminder sent yet.
ALTER TABLE orders ADD COLUMN overdue_reminder_stage smallint NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE orders DROP COLUMN IF EXISTS overdue_reminder_stage;
