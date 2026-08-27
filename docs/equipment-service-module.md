# Equipment Service — an optional Hiri module

Status: v1 built — module registry, schema, store layer, the equipment register,
the ticket queue with its merged timeline, parts and hours, the daily stale
sweep, and the wholesale portal. What is left is the v2 list at the end.
Written 2026-08-24, last updated 2026-08-26.

## The job

A roaster sells a cafe coffee, and then — because nobody else will — ends up
maintaining the espresso machine and the grinders that coffee goes through. That
work is real: parts get ordered, a tech drives out and spends two hours, the
shop calls at 6am because the group head is leaking. Today it lives in a
notebook, a text thread, and somebody's memory.

Hiri already knows the customer, their addresses, their order history, and how
to bill them. The service module adds the missing half: **the machines, the
tickets, the parts, the hours, and the conversation.**

Concretely it has to answer, for any machine or any account:

- What is installed at this shop, and how old is it?
- What did we last do to it, and when?
- What parts went in — ordered when, received when, installed when, what did
  they cost?
- How many hours have we put into this account this quarter?
- **When did we last talk to them?** (the question that actually causes churn)
- What is broken right now, and who told us?

## Decision: what "plugin" means here

Hiri is single-tenant — one container and one database per merchant
(`saas-readiness-hiri.md`). So a module is **per-instance**, not per-user and
not per-role. A merchant who doesn't fix espresso machines never sees the word
"service" anywhere in the product; a merchant who does flips one switch.

There is no plugin mechanism today. Feature flags have been ad-hoc columns
(`shipping_config.local_pickup_enabled`, `customers.announcements_enabled`).
That's fine for a per-row preference and wrong for "does this whole section of
the app exist", so this design introduces the smallest general thing that works:

```sql
-- Built: db/migrations/073_modules.sql
CREATE TABLE modules (
    key        text PRIMARY KEY,
    enabled    boolean NOT NULL DEFAULT false,
    enabled_at timestamptz,
    enabled_by uuid REFERENCES staff(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO modules (key) VALUES ('equipment_service');
```

- `domain/module.go` holds the known keys and their display metadata. A key in
  the table that the binary doesn't know about is ignored; a known key with no
  row reads as disabled. Both directions are safe across a rolling deploy.
- `app.ModuleService` owns reads and the toggle. Because Hiri is one process
  (server + River workers in one binary), it can cache the enabled set in an
  `atomic.Value` and invalidate on toggle. **That's only safe while the process
  count is one** — write the comment saying so, and if the fleet ever runs two
  replicas, drop to reading the single indexed row per request.
- Toggling is `PermManageSystem`, lives on a **Settings → Modules** tab, and
  writes an audit record. Turning a module off **never deletes data** — the
  routes 404 and the nav rows vanish, the tables stay. Say that on the toggle
  itself, because "will this nuke my service history" is the first question.

Enforcement, three places, all cheap:

| Surface | Mechanism |
|---|---|
| Routes | `deps.requireModule(domain.ModuleEquipmentService)` middleware wrapping the service mux — returns 404, not 403. A disabled module should look like it was never built. |
| Nav | `navItem` gains an optional `Module` field; `adminNav` filters rows whose module is off. Same for the wholesale portal nav. |
| Jobs | Every service worker re-checks enablement and returns nil (not an error) if the module is off. A job enqueued before a toggle must not fail forever in River. |

Note for the control plane later: module keys are exactly the shape a pricing
tier wants to set at provision time. Don't build that now, but don't design the
key space so it can't.

## Scope

**v1 — the notebook, replaced.**
Equipment register, tickets, parts, time, notes, customer-reported problems,
and the "we haven't talked to them" flag.

**v2 — the parts that need v1 in the wild first.**
Preventive maintenance schedules (interval- or volume-based), billing a ticket
out as a draft order, per-account service reporting.

**Not building.** A second invoicing engine (tickets bill through the existing
draft-order → invoice → QuickBooks path or not at all), a tech scheduling
calendar, parts inventory as its own system, mobile-native anything.

## Domain model

Five tables. The shape follows how the work actually happens: a **machine** at a
site accumulates **tickets**; each ticket accumulates **notes**, **parts**, and
**time**.

### `equipment`

```sql
CREATE TABLE equipment (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id        uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    address_id         uuid REFERENCES addresses(id) ON DELETE SET NULL,
    category           text NOT NULL,             -- espresso_machine | grinder | brewer | water | other
    make               text NOT NULL,
    model              text NOT NULL DEFAULT '',
    serial_number      text NOT NULL DEFAULT '',
    ownership          text NOT NULL DEFAULT 'customer',  -- customer | loaner | leased
    status             text NOT NULL DEFAULT 'active',    -- active | in_shop | retired
    installed_on       date,
    warranty_expires_on date,
    notes              text NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX equipment_customer_idx ON equipment (customer_id) WHERE status <> 'retired';
CREATE INDEX equipment_serial_idx   ON equipment (serial_number) WHERE serial_number <> '';
```

`address_id` because a customer can be a small chain with three locations and
"which shop is the machine at" is the first thing a tech asks. Nullable and
`ON DELETE SET NULL` — the machine outlives the address record.

`ownership` earns its column: a roaster-owned **loaner** placed against a volume
commitment is an asset the merchant needs a list of, and it changes who pays for
the repair. Retired machines stay in the table forever — history hangs off them.

Serial is indexed but **not unique**. Serials collide across manufacturers and
get typed in wrong; a unique constraint would reject real data at the worst
moment.

### `service_tickets`

```sql
CREATE TABLE service_tickets (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    number         text NOT NULL UNIQUE,          -- e.g. SVC-3A9F2C1B04
    customer_id    uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    equipment_id   uuid REFERENCES equipment(id) ON DELETE SET NULL,
    address_id     uuid REFERENCES addresses(id) ON DELETE SET NULL,
    title          text NOT NULL,
    description    text NOT NULL DEFAULT '',
    severity       text NOT NULL DEFAULT 'routine',  -- down | degraded | routine
    status         text NOT NULL DEFAULT 'new',
    opened_by_staff_id    uuid REFERENCES staff(id),
    opened_by_customer_user_id uuid REFERENCES customer_users(id),
    assigned_staff_id     uuid REFERENCES staff(id),
    scheduled_for  timestamptz,
    resolved_at    timestamptz,
    resolution     text NOT NULL DEFAULT '',
    billable       boolean NOT NULL DEFAULT false,
    last_contact_at timestamptz NOT NULL DEFAULT now(),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
```

**`severity`, not `priority`.** A cafe doesn't report "P1", it reports "the
machine is down and we're pulling shots on the backup". `down | degraded |
routine` is the same information without asking anyone to translate, and it maps
straight onto the customer-facing form ("Is the machine unusable right now?").

`equipment_id` is nullable and singular. A visit sometimes touches three
machines — model that as three tickets sharing a `scheduled_for`, not as a
join table. One ticket, one machine, one repair history is worth more than
modelling the truck route.

**Status:** `new → scheduled → in_progress → waiting_parts → waiting_customer →
resolved`, plus `cancelled`. Each of the waiting states exists because it
answers "why is this old ticket still open", which is the whole point of the
list view. Badge mapping uses the existing admin palette: `badge-red` (down /
new), `badge-amber` (waiting_*), `badge-blue` (scheduled/in_progress),
`badge-green` (resolved), `badge-grey` (cancelled).

**`last_contact_at` is the load-bearing column.** It is `NOT NULL`, defaulting
to creation time — the clock starts when the ticket opens, so every staleness
query is a plain comparison with no `COALESCE` onto `created_at` and no null
case to get wrong. It then moves only on
communication events — a note of kind `call`/`email`/`visit`, an outbound status
email, or a customer reply — never by an internal edit. An open ticket whose
`last_contact_at` is older than the configured window (default 7 days) renders
with the persistent inset rust bar the admin already uses for stale rows, and
gets counted on the Service tab badge. That single rule is what turns this from
a filing cabinet into something that prevents a lost account.

`number` mirrors `orders.number` exactly — `SVC-` plus ten random hex
characters, generated in the app layer, `UNIQUE` so re-runs and imports are
duplicate-safe. Random rather than sequential: it leaks no volume and needs no
counter. It is not sayable down a phone, which was the original hope, but a
second numbering scheme in one codebase costs more than staff ever gain from
reading a ticket number aloud — they click the row.

### `service_ticket_notes`

```sql
CREATE TABLE service_ticket_notes (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id      uuid NOT NULL REFERENCES service_tickets(id) ON DELETE CASCADE,
    kind           text NOT NULL,   -- note | call | email | visit | customer_report
    body           text NOT NULL,
    occurred_at    timestamptz NOT NULL DEFAULT now(),
    staff_id       uuid REFERENCES staff(id),
    customer_user_id uuid REFERENCES customer_users(id),
    customer_visible boolean NOT NULL DEFAULT false,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX service_ticket_notes_ticket_idx ON service_ticket_notes (ticket_id, occurred_at DESC);
```

Notes are content, not audit — they're written by humans, they have an
`occurred_at` that differs from `created_at` (you log Tuesday's call on
Thursday), and some are shown to the customer. Status changes stay in the audit
log; the detail page renders **both** merged into one timeline, the way order
and customer detail pages already do (`ListByResource` on resource type
`service_ticket`).

`customer_visible` is the switch that lets the portal show progress without
exposing "owner is being difficult about the bill".

### `service_parts`

```sql
CREATE TABLE service_parts (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id      uuid NOT NULL REFERENCES service_tickets(id) ON DELETE CASCADE,
    variant_id     uuid REFERENCES variants(id) ON DELETE SET NULL,
    name           text NOT NULL,
    part_number    text NOT NULL DEFAULT '',
    supplier       text NOT NULL DEFAULT '',
    quantity       integer NOT NULL DEFAULT 1 CHECK (quantity > 0),
    unit_cost_cents integer NOT NULL DEFAULT 0,
    status         text NOT NULL DEFAULT 'needed',  -- needed | ordered | received | installed
    ordered_on     date,
    received_on    date,
    installed_on   date,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
```

**Free text first, catalog reference optional.** Most parts are ordered ad hoc
from a distributor and will never be a Hiri product; forcing a catalog entry
before you can write down "group head gasket, $4" guarantees nobody writes it
down. `variant_id` is there for the minority of merchants who stock common
parts — when set, install decrements inventory through the existing catalog
path. When null, nothing else happens. Costs are stored in cents, like
everywhere else.

The four statuses plus three dates are the "what was ordered and replaced"
question the whole feature was asked for. `waiting_parts` on a ticket should be
derivable from — and cross-checked against — parts in `ordered` status.

### `service_time_entries`

```sql
CREATE TABLE service_time_entries (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id    uuid NOT NULL REFERENCES service_tickets(id) ON DELETE CASCADE,
    staff_id     uuid NOT NULL REFERENCES staff(id),
    kind         text NOT NULL DEFAULT 'labor',   -- labor | travel
    minutes      integer NOT NULL CHECK (minutes > 0),
    performed_on date NOT NULL,
    billable     boolean NOT NULL DEFAULT false,
    note         text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);
```

Minutes, not a start/stop timer — nobody remembers to hit stop, and every field
tech records "about an hour and a half" after the fact. `travel` is split from
`labor` because it's billed differently (or not at all) and because "we drove 90
minutes for a $4 gasket" is a fact worth being able to total.

`billable` is captured in v1 even though nothing bills off it yet. It costs a
boolean now and is unrecoverable later — you cannot retroactively remember which
of last year's hours were billable.

## Layers

Standard Hiri layering; nothing here needs an exception.

| Layer | Files | Notes |
|---|---|---|
| `domain/` | `module.go`, `equipment.go`, `service_ticket.go` | Typed string enums + constants for every status above. No logic. |
| `store/` | `modules.go`, `equipment.go`, `service_tickets.go` | All SQL. Customer-scoped reads take `customerID` (`Get(ctx, tx, id, customerID)`) for the portal; staff reads use `GetByID`. |
| `app/` | `modules.go`, `equipment.go`, `service.go` | State machine for ticket status, `last_contact_at` maintenance, stale-window policy, part/time roll-ups. Sentinel errors in `errors.go`: `ErrModuleDisabled`, `ErrTicketNotEditable`, `ErrEquipmentRetired`. |
| `web/` | `admin_service.go`, `wholesale_equipment.go`, `admin_modules.go` | Thin handlers, htmx `Content` split per the admin partial convention. |
| `ui/admin/` | `service_ticket_list.templ`, `service_ticket_show.templ`, `service_ticket_activity.templ`, `equipment_list.templ`, `equipment_show.templ`, `modules.templ` | Read `docs/admin-ui.md` and `docs/admin-detail-pages.md` first — `mage checkAdminUI` will fail the build otherwise. |
| `ui/storefront/` | wholesale portal equipment list + report form | |
| `jobs/` | `email_service_ticket_opened.go`, `email_service_ticket_update.go`, `service_stale_sweep.go` | Idempotent, `domain.SystemActor`, module check first. |

Every mutating service method takes `pgx.Tx`, writes its audit record in the
same transaction, enqueues its email job in the same transaction, and bumps
metrics after commit. No external calls inside a transaction (there are none in
v1 beyond email, which is a queued job).

## Admin UI

The sidebar is a flat seven-item list by deliberate design. The module adds an
**eighth row, "Service"**, rendered only when enabled — and that is exactly the
"don't clutter the app" requirement, honoured at the nav level rather than by
burying the feature.

`resolveActiveNav` gains `/admin/service*` → `/admin/service`. Keep the JS twin
in the page script in sync.

Section tabs under Service (`section_nav.templ`, not sidebar rows):

- **Tickets** — default. Filters: open / mine / waiting on parts / stale. Rows
  show number, customer, machine, severity badge, status badge, assignee, age,
  and last contact. Stale rows carry the persistent rust bar.
- **Equipment** — the register. Filter by customer, category, ownership. A
  loaner-only view is worth a saved filter.
- **Schedules** — v2, hidden until then.

`/admin/service` is the queue itself: it is what staff open the section to look
at. Scopes across the top — Open, Mine, Gone quiet, Closed — are links rather
than a filter dropdown, because they are the four questions actually asked.

**Ticket detail** follows `docs/admin-detail-pages.md`: main column holds the
parts table, the hours table, and the timeline (notes + audit, merged, newest
first) with the note composer above it; the rail holds the next action, the
assignee, the machine card, customer card with a link back to their account, and
the cost roll-up. Parts and hours went to the main column rather than the rail
as first sketched — rule 2 puts a control that acts on a sub-record next to that
sub-record, and a 22rem rail wraps every line of a table with per-row buttons.
The rail keeps the summary, which is what a rail is for. Destructive or
outward-facing actions (email the customer, cancel the ticket) use the existing
tooltip + Alpine confirm pattern — and remember the confirm hook binds to the
**button click**, never form submit, because of `hx-boost`.

**Equipment detail** is the machine's life story: identity block, warranty
countdown, and every ticket against it in date order. This is the screen that
answers "we've replaced this pump twice in a year, the machine is the problem",
which is the argument a merchant needs when a shop is angry.

Entry points from existing pages, all module-gated: an **Equipment** card on the
admin customer detail rail, and a "Log service" action on it.

## Wholesale portal

This is what makes the module worth more than a spreadsheet: the malfunction
report comes in structured, at 6am, without a phone call.

- `GET /wholesale/account/equipment` — their machines, with the last service
  date on each.
- `POST /wholesale/account/equipment/{id}/report` — "Report a problem": is it
  down right now, what's happening, best time to reach you. Creates a ticket in
  `new`, `opened_by_customer_user_id` set, the description copied into a
  `customer_report` note, and enqueues staff notification email. Rate-limited
  per customer, the same way `/wholesale/apply` is.
- The ticket's `customer_visible` notes and its status show back on the same
  page, so "did they see my message" answers itself.

Built: `web/wholesale_equipment.go`, `ui/storefront/wholesale_equipment.templ`,
`app/service_ticket_opened.go`. Two things were easy to get wrong here and are
worth stating. The routes need `requireModule` of their own — the portal's
`/wholesale/account/{path...}` catch-all is one mount for the whole account
area, so the equipment routes get their own more-specific mounts rather than
gating the catch-all and taking the portal down with the module. And the nav row
reads the enabled set from the request context (`withModules` now wraps the
portal mount, as it already did the admin one) rather than from props, because
the account nav renders on six pages that have nothing to do with this module.

Retail storefront gets nothing. Retail customers don't own espresso machines the
merchant maintains.

## Permissions

Two new constants in `platform/auth/permissions.go`:

```go
PermViewService  = "service:view"
PermWriteService = "service:write"
```

| Role | Grant | Why |
|---|---|---|
| admin | view + write | |
| fulfillment | view + write | The tech is nearly always the person already driving the van. |
| support | view + write | They take the 6am call; a support person who can read a ticket but not log the call is useless. |
| finance | view | Needs the hours and part costs; shouldn't be changing repair records. |
| catalog | — | No overlap. |

Do **not** add a sixth staff role for technicians. Five coarse roles is a
deliberate choice; revisit only if a merchant actually asks for a login that
sees service and nothing else.

Checks go in middleware in `web/router.go`, stacked after `requireModule` —
module check first, so a disabled module 404s for everyone including admins,
rather than 403ing for some.

## Jobs and email

| Job | Trigger | Content |
|---|---|---|
| `service_ticket_opened` | Customer reports a problem from the portal | Staff notification. `down` severity bypasses quiet hours — that's the whole reason to build it. |
| `service_ticket_update` | Status change with a customer-visible note, or manual "send update" | Customer-facing: what's happening, what's next. |
| `service_stale_sweep` | Daily cron, alongside the existing scheduler jobs | Digest of open tickets past the contact window, to staff. No customer mail. |

All three: check module enablement first and return nil when off. All idempotent
— key the sweep on the day, the update on the note id.

## Audit actions

In `platform/audit/actions.go`, resource types `equipment` and `service_ticket`:

```
AuditModuleEnabled          = "module.enabled"
AuditModuleDisabled         = "module.disabled"
AuditEquipmentCreated       = "equipment.created"
AuditEquipmentUpdated       = "equipment.updated"
AuditEquipmentRetired       = "equipment.retired"
AuditServiceTicketOpened    = "service_ticket.opened"
AuditServiceTicketAssigned  = "service_ticket.assigned"
AuditServiceTicketStatus    = "service_ticket.status_changed"
AuditServiceTicketResolved  = "service_ticket.resolved"
AuditServiceTicketNoteAdded = "service_ticket.note_added"
AuditServicePartAdded       = "service_ticket.part_added"
AuditServicePartStatus      = "service_ticket.part_status_changed"
AuditServiceTimeLogged      = "service_ticket.time_logged"
```

These double as the detail-page timeline, so write useful metadata — old and new
status, part name, minutes — not just the verb.

## Metrics

After commit, not inside the transaction:

- `hiri_service_tickets_opened_total{source="staff|customer",severity=""}`
- `hiri_service_tickets_resolved_total`
- `hiri_service_ticket_open_count{status=""}` (gauge, set by the sweep)
- `hiri_service_stale_tickets` (gauge — the one worth alerting on)

## Migration

One migration, `074_equipment_service.sql`, with a real `-- +goose Down` that
drops all six tables in dependency order. It is additive: nothing existing
changes, so it is safe to ship before any UI exists and safe to roll back.

## v2, briefly

- **Preventive maintenance schedules.** `service_schedules` on an equipment row:
  every N days, or every N pounds of coffee — Hiri already knows the pounds.
  A daily job opens a `routine` ticket when one comes due. The volume-based
  trigger is the differentiated one; nobody else can compute it.
- **Billing a ticket out.** "Create draft order" from a resolved billable
  ticket: parts as line items, billable minutes against a labor variant at a
  configured rate. Reuses the draft order → invoice → QuickBooks path that
  already exists. Do not build a second money path.
- **Per-account service report.** Hours and part spend by customer over a
  period — the number that tells a merchant which account is unprofitable.

## Open decisions

1. **Does service get billed?** The schema captures `billable` from day one, so
   this can be answered late — but it decides whether v2 leads with billing or
   with schedules.
2. **Stale window default.** 7 days is a guess, and currently a constant
   (`domain.DefaultStaleContactWindow`) rather than a settings row — nothing has
   run long enough to argue with it. The sweep job in step 6 makes it
   configurable.
4. **Loaner machines and volume commitments.** If loaners are placed against a
   pounds-per-month commitment, tracking that commitment is a natural neighbour
   of this module — but it's a sales feature wearing a service costume. Keep it
   out of v1.

## Build order

1. **Done.** `modules` table (migration `073_modules.sql`) + `domain/module.go`
   + `app.ModuleService` + Settings → Modules tab + `requireModule` middleware
   + `filterNavItems` in the admin layout. Shipped alone, as planned.
2. **Done.** Migration `074_equipment_service.sql`, `domain/equipment.go` and
   `domain/service_ticket.go`, `store/equipment.go` and `store/service_tickets.go`.
3. **Done.** Equipment register: list with filters, detail page, add/edit form,
   and the Equipment card on the customer rail. The **Service** sidebar row
   arrived with it — module-gated, so it exists only where the module is on.
4. **Done.** Tickets: queue with scopes and the stale flag, detail page with the
   merged notes-and-audit timeline, status machine, note composer, assignment,
   and the repair history on the machine page. The section tab strip arrived
   with it.
5. **Done.** Parts and time entries on the ticket detail, with per-row status
   advance, removal, and the parts/hours roll-up in the rail.
6. **Done.** The daily sweep (`service_stale_sweep`): a staff digest of open
   tickets past the contact window, the `service_tickets_open_total` /
   `service_tickets_stale_total` gauges, and the audit row recording the send.
   The queue-side stale flag arrived earlier, with step 4 — what was missing was
   anything that pushed. No customer mail; silent on a day with nothing quiet.
7. **Done.** The wholesale portal: `/wholesale/account/equipment` lists the
   cafe's machines with the last date work on each was *finished*, every open
   ticket's status, and the customer-visible half of its timeline — so "did
   anyone see my message" is answered by the page. The report form opens a
   ticket in `new` with `opened_by_customer_user_id`, copies the words into a
   customer-visible `customer_report` note, and enqueues `service_ticket_opened`
   to staff in the same transaction. `down` skips the quiet-hours deferral,
   which is what `Enqueuer.notifyOpts` was built for. Rate-limited **per
   account**, not per IP: a cafe is one account behind one router.
