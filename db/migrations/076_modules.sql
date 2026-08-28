-- +goose Up

-- Optional feature modules, toggled per instance.
--
-- Hiri is single-tenant — one container and one database per merchant — so a
-- module is a property of the whole shop, not of a user or a role. A merchant
-- who does not maintain espresso machines never sees the word "service"
-- anywhere in the product; a merchant who does flips one switch.
--
-- This is deliberately not another boolean column. shipping_config
-- .local_pickup_enabled and customers.announcements_enabled are per-row
-- preferences and belong where they are. "Does this whole section of the app
-- exist" is a different question, asked by the router, the sidebar and the job
-- workers alike, and it wants one place to ask it.
--
-- The registry of known keys lives in Go (domain/module.go), not in a CHECK
-- constraint. Both directions across a rolling deploy have to be safe: a key
-- this binary does not know about is ignored, and a key with no row here reads
-- as disabled. A CHECK would turn the first case into a failed INSERT during
-- the window where an older binary is still serving.
CREATE TABLE modules (
    key        text PRIMARY KEY,
    enabled    boolean NOT NULL DEFAULT false,
    -- When and by whom, kept because "who turned this on" is the first question
    -- asked after a module starts sending mail nobody expected. Both stay set
    -- after a disable — they record the last change either way, and the audit
    -- log holds the full history.
    enabled_at timestamptz,
    enabled_by uuid REFERENCES staff(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Seed the known modules as disabled. Enabling is a deliberate act in the
-- admin, never a migration — a deploy must not switch on a section of the app
-- that the merchant has not asked for.
INSERT INTO modules (key) VALUES ('equipment_service');

-- +goose Down

DROP TABLE IF EXISTS modules;
