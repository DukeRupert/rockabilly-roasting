-- +goose Up

-- Preventive maintenance for the equipment service module: the v2 half sketched
-- in docs/equipment-service-module.md, built now that the register and the
-- ticket queue have been in the wild.
--
-- The shape is a template and its instances. A shop services a dozen Linea PBs
-- and the PM series is the same on every one of them, so the series is defined
-- once as a *plan* and assigned to machines. What is per-machine is the anchor
-- date and whether the customer pays for the work.
--
-- Additive and module-gated like 077 — safe on instances that will never switch
-- the module on.

-- A named series of preventive maintenance, defined once and assigned to many
-- machines. Usually mirrors a manufacturer's warranty schedule.
CREATE TABLE service_plans (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    -- Which kind of machine the plan is written for. Nullable = any machine.
    --
    -- NOTE: written as "advisory only — it sorts the picker"; the application
    -- has always filtered on it (ListPlans: category IS NULL OR category = $1),
    -- so a plan is offered only for machines of its kind. Corrected here rather
    -- than in a new migration because the column and its CHECK are unchanged.
    category    text
                CHECK (category IN ('espresso_machine', 'grinder', 'brewer', 'water', 'other')),
    -- Retiring a plan hides it from the assignment picker without disturbing
    -- the machines already on it. Plans are never deleted once assigned —
    -- ON DELETE RESTRICT below makes that a database rule, not a convention.
    active      boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Case-insensitive, because "Linea PB warranty" and "Linea PB Warranty" being
-- two plans is a data-entry accident nobody notices until the second one has
-- machines on it.
CREATE UNIQUE INDEX service_plans_name_idx ON service_plans (lower(name));
CREATE INDEX service_plans_active_idx ON service_plans (category) WHERE active;

-- One item in the series: "backflush and change screens, every 30 days".
CREATE TABLE service_plan_tasks (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id           uuid NOT NULL REFERENCES service_plans(id) ON DELETE CASCADE,
    name              text NOT NULL,
    -- What the tech actually does. Copied into the ticket description when a
    -- due item books itself, so the person on site has the procedure in hand.
    instructions      text NOT NULL DEFAULT '',
    -- Days between occurrences. Every interval a coffee shop cares about is a
    -- whole number of days — 30, 90, 180, 365 — and months would drag in a
    -- calendar arithmetic argument for no gain.
    interval_days     integer NOT NULL CHECK (interval_days > 0),
    -- How far ahead of the due date it starts asking for attention. A 365-day
    -- full service needs weeks of warning to get booked; a 30-day backflush
    -- needs days. Per task, because the same plan holds both.
    lead_days         integer NOT NULL DEFAULT 14 CHECK (lead_days >= 0),
    -- Whether missing it voids the manufacturer's warranty. This is the whole
    -- reason the feature exists: an overdue warranty task on a machine still
    -- inside its warranty is the loudest thing on the due list.
    warranty_required boolean NOT NULL DEFAULT false,
    sort_order        integer NOT NULL DEFAULT 0,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX service_plan_tasks_plan_idx ON service_plan_tasks (plan_id, sort_order, created_at);

-- A plan put on a machine, from a date.
CREATE TABLE equipment_service_plans (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    equipment_id   uuid NOT NULL REFERENCES equipment(id) ON DELETE CASCADE,
    -- RESTRICT: a plan with machines on it cannot be deleted out from under
    -- them. Deactivate it instead.
    plan_id        uuid NOT NULL REFERENCES service_plans(id) ON DELETE RESTRICT,
    -- The anchor. "This machine was installed / last fully serviced on this
    -- day"; every task's first occurrence counts its interval forward from
    -- here. Staff set it deliberately when assigning, which is what makes a
    -- mid-life machine land on a believable schedule instead of all-due-today.
    starts_on      date NOT NULL,
    -- Whether the customer pays for this maintenance. It decides what happens
    -- when a task comes due: a contract account gets a routine ticket opened
    -- for it automatically, a no-contract account gets a row on the call list
    -- so somebody can ring them and sell the visit.
    under_contract boolean NOT NULL DEFAULT false,
    -- When the contract lapses, if it is a term. Past this date the assignment
    -- behaves as if it were never under contract — it keeps generating due
    -- items, they just stop booking themselves.
    contract_ends_on date,
    -- Ending an assignment stops it generating work without deleting the
    -- history of what was done under it.
    ended_at       timestamptz,
    notes          text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- A machine can be on several plans at once (the manufacturer's and the shop's
-- own), but not on the same plan twice — that would double every due item.
CREATE UNIQUE INDEX equipment_service_plans_live_idx
    ON equipment_service_plans (equipment_id, plan_id) WHERE ended_at IS NULL;
CREATE INDEX equipment_service_plans_equipment_idx
    ON equipment_service_plans (equipment_id) WHERE ended_at IS NULL;
CREATE INDEX equipment_service_plans_plan_idx ON equipment_service_plans (plan_id);

-- One occurrence of one task on one machine: due on a day, then completed,
-- skipped, or still waiting.
--
-- Materialised rather than computed from (anchor + n * interval) because the
-- schedule has to survive contact with reality. A task done two weeks late
-- re-anchors the next one to when it was *actually* done, a task skipped keeps
-- the original cadence, and both facts have to be recorded somewhere. A pure
-- function of the anchor date could express neither.
--
-- Exactly one pending row exists per (assignment, task) at a time; completing
-- or skipping it writes the next one.
CREATE TABLE service_maintenance_due (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    assignment_id         uuid NOT NULL REFERENCES equipment_service_plans(id) ON DELETE CASCADE,
    task_id               uuid NOT NULL REFERENCES service_plan_tasks(id) ON DELETE CASCADE,
    -- Denormalised from the assignment. The due list is the one view that spans
    -- every customer, and reaching the machine from a due row is otherwise a
    -- four-table join on every page load.
    equipment_id          uuid NOT NULL REFERENCES equipment(id) ON DELETE CASCADE,
    due_on                date NOT NULL,
    status                text NOT NULL DEFAULT 'pending'
                          CHECK (status IN ('pending', 'completed', 'skipped')),
    completed_on          date,
    completed_by_staff_id uuid REFERENCES staff(id) ON DELETE SET NULL,
    -- The ticket the work was done on, where there was one. Set both by the
    -- auto-booking sweep and by a staffer marking an item done against a ticket
    -- they already had open.
    ticket_id             uuid REFERENCES service_tickets(id) ON DELETE SET NULL,
    notes                 text NOT NULL DEFAULT '',
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);

-- The invariant: one open occurrence per task per machine. Without it a double
-- submit or a re-run of the sweep silently doubles the schedule.
CREATE UNIQUE INDEX service_maintenance_due_pending_idx
    ON service_maintenance_due (assignment_id, task_id) WHERE status = 'pending';
-- The due list and the calendar both read this: pending, ordered by day.
CREATE INDEX service_maintenance_due_calendar_idx
    ON service_maintenance_due (due_on) WHERE status = 'pending';
-- The machine's own maintenance history, newest first.
CREATE INDEX service_maintenance_due_equipment_idx
    ON service_maintenance_due (equipment_id, due_on DESC);
-- No index on ticket_id. Nothing looks an occurrence up by its ticket, and the
-- scopes that mention the column test `ticket_id IS NULL` as one predicate
-- among several on an already-narrow pending set — not a lookup an index of its
-- own would serve.

-- +goose Down

DROP TABLE service_maintenance_due;
DROP TABLE equipment_service_plans;
DROP TABLE service_plan_tasks;
DROP TABLE service_plans;
