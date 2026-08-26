-- +goose Up

-- The equipment service module's tables. Gated behind the 'equipment_service'
-- module (migration 073) — this migration is additive and safe to run on every
-- instance, including the ones that will never switch the module on.
--
-- Shape follows how the work actually happens: a machine at a site accumulates
-- tickets; each ticket accumulates notes, parts and time. See
-- docs/equipment-service-module.md for the design and the rejected alternatives.

-- A machine the shop maintains for a customer.
CREATE TABLE equipment (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id         uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    -- Which of the customer's locations it sits at. A wholesale account can be
    -- a three-cafe chain, and "which shop is it at" is the first thing a tech
    -- asks. Nullable and SET NULL: the machine outlives the address record.
    address_id          uuid REFERENCES addresses(id) ON DELETE SET NULL,
    category            text NOT NULL
                        CHECK (category IN ('espresso_machine', 'grinder', 'brewer', 'water', 'other')),
    make                text NOT NULL,
    model               text NOT NULL DEFAULT '',
    serial_number       text NOT NULL DEFAULT '',
    -- Who owns it. A roaster-owned loaner placed against a volume commitment is
    -- an asset the shop needs a list of, and it changes who pays for the repair.
    ownership           text NOT NULL DEFAULT 'customer'
                        CHECK (ownership IN ('customer', 'loaner', 'leased')),
    -- in_shop is "we took it away to work on it" — distinct from retired, which
    -- is permanent. Retired machines are never deleted: the repair history that
    -- justifies replacing a lemon hangs off them.
    status              text NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active', 'in_shop', 'retired')),
    installed_on        date,
    warranty_expires_on date,
    notes               text NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX equipment_customer_idx ON equipment (customer_id) WHERE status <> 'retired';
-- Indexed for lookup, deliberately NOT unique: serials collide across
-- manufacturers and get typed in wrong, and a unique constraint would reject
-- real data at the worst possible moment — mid-repair, with the tech waiting.
CREATE INDEX equipment_serial_idx ON equipment (serial_number) WHERE serial_number <> '';

-- One repair, one machine, one thread of conversation.
CREATE TABLE service_tickets (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Same shape as orders.number (app.generateTicketNumber): a random handle,
    -- not a sequence, so it leaks no volume and needs no counter. UNIQUE, which
    -- also makes any future import re-run duplicate-safe.
    number         text NOT NULL UNIQUE,
    customer_id    uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    -- Nullable and singular. A visit that touches three machines is three
    -- tickets sharing a scheduled_for — one machine's repair history is worth
    -- more than modelling the truck route.
    equipment_id   uuid REFERENCES equipment(id) ON DELETE SET NULL,
    address_id     uuid REFERENCES addresses(id) ON DELETE SET NULL,
    title          text NOT NULL,
    description    text NOT NULL DEFAULT '',
    -- Severity, not priority: a cafe does not report "P1", it reports that the
    -- machine is down and they are pulling shots on the backup. These are the
    -- words they would use, so the customer-facing form can ask directly.
    severity       text NOT NULL DEFAULT 'routine'
                   CHECK (severity IN ('down', 'degraded', 'routine')),
    -- The two waiting_* states are the point of the list view: they answer
    -- "why is this old ticket still open" without anyone opening it.
    status         text NOT NULL DEFAULT 'new'
                   CHECK (status IN ('new', 'scheduled', 'in_progress', 'waiting_parts',
                                     'waiting_customer', 'resolved', 'cancelled')),
    opened_by_staff_id         uuid REFERENCES staff(id) ON DELETE SET NULL,
    opened_by_customer_user_id uuid REFERENCES customer_users(id) ON DELETE SET NULL,
    assigned_staff_id          uuid REFERENCES staff(id) ON DELETE SET NULL,
    scheduled_for   timestamptz,
    resolved_at     timestamptz,
    resolution      text NOT NULL DEFAULT '',
    -- Captured from day one even though nothing bills off it yet. It costs a
    -- boolean now and is unrecoverable later: nobody can retroactively remember
    -- which of last year's hours were billable.
    billable        boolean NOT NULL DEFAULT false,
    -- The load-bearing column. Moved only by communication — a call, an email,
    -- a visit, a customer reply — never by an internal edit, so an open ticket
    -- that has gone quiet is visible as exactly that.
    --
    -- NOT NULL, defaulting to creation time: the clock starts when the ticket
    -- opens (a customer report *is* contact, and a staff-opened ticket is still
    -- activity). Every staleness query is then a plain comparison with no
    -- COALESCE onto created_at, and there is no null case to get wrong.
    last_contact_at timestamptz NOT NULL DEFAULT now(),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX service_tickets_customer_idx  ON service_tickets (customer_id, created_at DESC);
CREATE INDEX service_tickets_equipment_idx ON service_tickets (equipment_id, created_at DESC)
    WHERE equipment_id IS NOT NULL;
-- The queue: everything not finished, oldest contact first. Partial, because
-- the closed tickets accumulate forever and the list view never wants them.
CREATE INDEX service_tickets_open_idx ON service_tickets (last_contact_at)
    WHERE status NOT IN ('resolved', 'cancelled');
CREATE INDEX service_tickets_assigned_idx ON service_tickets (assigned_staff_id)
    WHERE assigned_staff_id IS NOT NULL AND status NOT IN ('resolved', 'cancelled');

-- Human-written entries: what was said, and when it was said.
--
-- Separate from the audit log on purpose. These have an occurred_at that
-- differs from created_at (you log Tuesday's call on Thursday), they are edited
-- and read as content, and some are shown to the customer. Status changes stay
-- in the audit log; the ticket page merges the two into one timeline.
CREATE TABLE service_ticket_notes (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id        uuid NOT NULL REFERENCES service_tickets(id) ON DELETE CASCADE,
    -- customer_report is what the portal's "report a problem" form writes.
    -- call/email/visit are the kinds that move service_tickets.last_contact_at.
    kind             text NOT NULL
                     CHECK (kind IN ('note', 'call', 'email', 'visit', 'customer_report')),
    body             text NOT NULL,
    occurred_at      timestamptz NOT NULL DEFAULT now(),
    staff_id         uuid REFERENCES staff(id) ON DELETE SET NULL,
    customer_user_id uuid REFERENCES customer_users(id) ON DELETE SET NULL,
    -- The switch that lets the portal show progress without exposing the
    -- internal half of the thread.
    customer_visible boolean NOT NULL DEFAULT false,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX service_ticket_notes_ticket_idx ON service_ticket_notes (ticket_id, occurred_at DESC);

-- What was ordered and what went in.
CREATE TABLE service_parts (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id       uuid NOT NULL REFERENCES service_tickets(id) ON DELETE CASCADE,
    -- Free text first; the catalog link is optional. Most parts are ordered ad
    -- hoc from a distributor and will never be a product here, and forcing a
    -- catalog entry before you can write down "group head gasket, $4"
    -- guarantees nobody writes it down. When variant_id is set, installing the
    -- part can decrement stock through the normal catalog path.
    variant_id      uuid REFERENCES variants(id) ON DELETE SET NULL,
    name            text NOT NULL,
    part_number     text NOT NULL DEFAULT '',
    supplier        text NOT NULL DEFAULT '',
    quantity        integer NOT NULL DEFAULT 1 CHECK (quantity > 0),
    unit_cost_cents integer NOT NULL DEFAULT 0 CHECK (unit_cost_cents >= 0),
    status          text NOT NULL DEFAULT 'needed'
                    CHECK (status IN ('needed', 'ordered', 'received', 'installed')),
    ordered_on      date,
    received_on     date,
    installed_on    date,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX service_parts_ticket_idx ON service_parts (ticket_id);

-- Hours, recorded after the fact.
CREATE TABLE service_time_entries (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id    uuid NOT NULL REFERENCES service_tickets(id) ON DELETE CASCADE,
    staff_id     uuid NOT NULL REFERENCES staff(id) ON DELETE RESTRICT,
    -- Travel is split from labour because it bills differently (or not at all),
    -- and because "we drove 90 minutes for a $4 gasket" is a fact worth totting
    -- up when deciding whether an account is worth keeping.
    kind         text NOT NULL DEFAULT 'labor' CHECK (kind IN ('labor', 'travel')),
    -- Minutes, not a start/stop timer: nobody remembers to hit stop, and every
    -- field tech records "about an hour and a half" afterwards.
    minutes      integer NOT NULL CHECK (minutes > 0),
    performed_on date NOT NULL,
    billable     boolean NOT NULL DEFAULT false,
    note         text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX service_time_entries_ticket_idx ON service_time_entries (ticket_id);
-- "How many hours went into this account this quarter" is the report that
-- decides whether the servicing is worth doing.
CREATE INDEX service_time_entries_staff_idx ON service_time_entries (staff_id, performed_on);

-- +goose Down

DROP TABLE IF EXISTS service_time_entries;
DROP TABLE IF EXISTS service_parts;
DROP TABLE IF EXISTS service_ticket_notes;
DROP TABLE IF EXISTS service_tickets;
DROP TABLE IF EXISTS equipment;
