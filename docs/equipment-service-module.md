# Equipment Service — an optional Hiri module

Status: v1 built — module registry, schema, store layer, the equipment register,
the ticket queue with its merged timeline, parts and hours, the daily stale
sweep, and the wholesale portal. Preventive maintenance and the cost roll-ups
(the first two thirds of v2) plus labour rates built 2026-08-29 — see *Preventive maintenance* and
*Cost roll-ups* below. What is left is the rest of the v2 list at the end.
Written 2026-08-24, last updated 2026-08-29.

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
-- Built: db/migrations/076_modules.sql
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
Preventive maintenance schedules (**built** — interval-based; volume-based
still open), service cost reporting (**built** — per machine, per account, and
across accounts), billing a ticket out as a draft order.

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

One migration, `077_equipment_service.sql`, with a real `-- +goose Down` that
drops all five tables in dependency order. It is additive: nothing existing
changes, so it is safe to ship before any UI exists and safe to roll back.

## v2, briefly

- **Preventive maintenance schedules — built.** See *Preventive maintenance*
  below. Interval-based only; the volume-based trigger (every N pounds of
  coffee, which Hiri already knows) is still the differentiated one and is
  still unbuilt.
- **Billing a ticket out.** "Create draft order" from a resolved billable
  ticket: parts as line items, billable minutes against a labor variant at a
  configured rate. Reuses the draft order → invoice → QuickBooks path that
  already exists. Do not build a second money path.
- **Per-account service report — built.** Hours and part spend appear on a
  machine's page, on a customer's rail, and ranked across every account at
  `/admin/service/costs`. See *Cost roll-ups*.

## Preventive maintenance

Migration `081_service_plans.sql`. Four tables, in two pairs: a **template**
(`service_plans` + `service_plan_tasks`) and its **instances**
(`equipment_service_plans` + `service_maintenance_due`).

A shop services a dozen Linea PBs and the manufacturer's schedule is the same on
every one, so the series is written once as a plan and assigned to machines.
What is per-machine is the *anchor date* and *whether the customer pays*.

### The tables

| Table | What it holds |
|---|---|
| `service_plans` | A named series — "Linea PB — warranty schedule". Case-insensitively unique name; `active` retires one from the picker without disturbing machines on it. |
| `service_plan_tasks` | One job in the series: `interval_days`, `lead_days` (how far ahead it starts asking), `warranty_required`. |
| `equipment_service_plans` | A plan on a machine from `starts_on`, with `under_contract` / `contract_ends_on`. `ended_at` stops it generating work. |
| `service_maintenance_due` | One dated occurrence. Exactly one `pending` row per (assignment, task), enforced by a partial unique index. |

Occurrences are **materialised**, not computed from `anchor + n × interval`. The
schedule has to survive contact with reality: a task done two weeks late
re-anchors the next one to when it was *actually* done, a task skipped keeps the
original cadence, and a pure function of the anchor could express neither.

### The rules

- **Closing writes the successor, in the same transaction** — for a live
  assignment. A completion that produced no next occurrence is a machine that
  silently falls off the schedule at the moment it was last serviced. Ending an
  assignment clears its pending row, and closing one out afterwards from a stale
  page must not resurrect the schedule, so that case deliberately writes no
  successor. `ServicePlanService.closeDue` is the only
  path, and `CloseDue` is scoped to `status = 'pending'` so a double submit
  closes it once.
- **Completion re-anchors; a skip does not.** `NextDueAfterCompletion` counts
  from the day the work happened. `NextDueAfterSkip` counts from the day it was
  *due*, then steps forward whole intervals until it is in the future — so
  skipping a badly overdue item still clears the row.
- **The first occurrence is never pushed forward.** Assigning a plan with an
  anchor two years back is how a shop *discovers* a machine is overdue.
- **The contract decides what "due" does.** Covered work inside its lead window
  opens itself a `routine` ticket overnight and attaches it. Uncovered work is
  never booked — that would commit the shop to a visit nobody agreed to pay for
  — and lands on the **call list** instead, which is a human's job.

### Admin UI

Three new tabs in the Service section — `Maintenance`, `Plans` and `Costs`.

- `/admin/service/maintenance` — the due list, scoped: everything due, overdue,
  **warranty at risk**, the **call list**, and history. Each pending row carries
  an inline "done on `<date>`" form (back-dating is the common case), Skip, and
  "Book a visit", which passes `maintenance_id` through the ticket form so the
  finished ticket attaches to the occurrence.
- `/admin/service/maintenance/calendar` — a six-week month grid, Monday-first.
- `/admin/service/plans` and `/admin/service/plans/{id}` — write a plan, add
  jobs to the series. Editing a task's interval reschedules every pending
  occurrence of it, which is the point of a plan being a template.
- The machine's page grows a **Maintenance** card: which plans it is on, what is
  coming up, and the form for putting it on another.
- The dashboard command bar gets an overdue-maintenance chip, asked for only
  where the module is on.

### The daily job

`service_maintenance_sweep` (River, daily, no `RunOnStart` — the booking half
opens real tickets, and a deploy is not a reason for a machine to acquire a
visit). It backfills missing occurrences from `ListMissingDue`, books the
covered work, and publishes `service_maintenance_due_total{scope}`.

Backfill is the fan-out path for *existing* machines: `AddTask` deliberately
does not reach the forty already on a plan, because one job that finds every gap is easier
to trust than several paths that each find some. Booking is capped at
`bookingLimit` a day.

## Cost roll-ups

`ServiceTicketStore.CostSummary` widens the per-ticket `Totals` query to a whole
machine or a whole account, over an optional window. It is deliberately a
*widening* — `domain.ServiceCostSummary` embeds `ServiceTotals` rather than
restating its fields, so a machine page and the ticket beside it can never
disagree about what a repair cost.

**Everything recorded counts, billable or not.** What the work cost the shop and
what the customer was charged are different questions; billable minutes come
back alongside the total, never instead of it. Parts count from the moment the
line is written, whatever their status — a part sitting at `needed` has cost
committed, and dropping it would make a repair in progress look free.

Two decisions worth knowing:

- **The window is measured against the day work happened**, not the day the
  ticket was raised. A ticket opened in January and worked in April is April's
  cost. Time entries use `performed_on`; parts have no single spend date, so
  they fall through `installed_on → received_on → ordered_on → created_at`,
  which puts every part in exactly one window.
- **Visits are tickets that carried work**, not tickets that exist. A ticket
  that was only ever a phone call is not a visit, and counting it would flatter
  the hours-per-visit figure the number exists to expose.

Three windows are drawn at once — 90 days, 12 months, all time — rather than
behind a period selector. The question is never "what did last quarter cost" on
its own; it is whether this machine is getting worse, and that is a comparison a
toggle makes you hold in your head.

Surfaces: the **What it has cost** card in the main column of a machine's page
(the quantified form of the repair history under it), and a compact **Service
cost** card on the customer page's rail, scoped to the customer so the
call-outs that never named a machine still count.

An unscoped `CostSummary` is an error rather than the whole shop's numbers.

### The cross-account table

`/admin/service/costs` — the Costs tab. Every account that took service work in
a period, ranked, with the machines it has beside the hours it took. The row
worth finding is large hours over few machines, which is why the machine count
is in the table rather than left to memory.

`CostByCustomer` normalises parts and time entries into one shape and aggregates
them together. A FULL OUTER JOIN between two per-customer aggregates is the
obvious implementation, and it is the one that quietly drops the account with
parts but no hours logged — there is a test for exactly that.

**The money figure exists only once the shop supplies a rate.** Parts are in
cents and work is in minutes; blending them needs an hourly cost, and inventing
one would put a made-up number at the top of a report meant to settle arguments.
Set a rate in Settings → Service and a Cost column appears on all three
surfaces, and Cost becomes the default ranking. With nothing to cost — no rate
set and no hour carrying one — the Cost option is not offered at all and a cost
ranking asked for by URL falls back to hours, because ordering by
`parts_cents + 0` under a Cost heading would be the misleading label this report
was built to avoid.

`ServiceAccountCostByCost` ranks on `parts_cents + labor_cents` — the same
figure the Cost column prints, ordered in SQL because it has to precede the
LIMIT. `ServiceAccountReport.CanCost` decides both whether that ranking is
offered and whether the column is drawn, from one signal so the two cannot
disagree: true as soon as either an hour carries a rate or the shop has set one.

Every link in the control strip names its ranking, including Hours. A link that
omitted the parameter would read to the handler as "no preference", which is the
request for the default — so the Hours tab would come back ranked by cost.

Rows are driven by work recorded, not by the customer list: an account nobody
touched in the window has no row, so the table stays as short as the finding is.
The footer totals only what is shown, and a truncated table says so in red
rather than looking complete.

## Labour rates

Migration `082_service_labor_rates.sql` adds two nullable columns to
`store_settings`, edited on **Settings → Service** (module-gated, so the tab
does not exist on a shop that only sells coffee).

**These are cost rates, not prices.** The reports they feed say plainly that
they measure what work cost the shop rather than what it earned, and a
charge-out rate dropped into that column would quietly turn a cost report into a
revenue one. Billing a ticket out — still unbuilt — will want its own charge
rate when it lands; it is deliberately not this column.

- **Nullable, not defaulted to zero.** There is no rate a shop can be assumed to
  have, and a zero would render as "$0.00 of labour" — a number that looks
  measured. Unset is the shipped state and every money surface hides itself.
  `Set()` is false for a nil *or* zero labour rate, so the two cannot diverge.
- **Travel has its own rate, falling back to labour.** `service_time_entries`
  splits travel from labour because it bills differently or not at all; a second
  rate honours that split. Nil means "cost it as labour", and an explicit `0.00`
  means the shop absorbs the drive — which is why the form takes strings and
  blank is not zero.
- **A travel rate with no labour rate is refused**, and so is a labour rate of
  zero. There is one spelling of "unset" and it is blank: `Set()` reads a zero
  as unset, so saving one would stamp every hour uncosted while the settings
  page showed a figure somebody had typed. Travel keeps its zero, where it
  means the shop absorbs the drive.
- **Costing rounds to the nearest cent**, not truncated. These figures are
  summed across hundreds of entries; a fraction lost on each would drift away
  from the same numbers computed any other way.
- **Rates are snapshotted per time entry** (migration `083`). See below.

### The snapshot

`service_time_entries.rate_cents` holds what the hour was booked at, stamped
when the entry is written and never touched by a settings change. The reports
sum `minutes × rate_cents` per entry; there is no "current rate" for them to
apply, which is the point.

Before this, raising the rate in March silently made last August more expensive.
That is the opposite of what a cost record is for: the hour was bought at the
rate of the day, and that is a fact about the past.

- **The travel fallback resolves at stamp time.** A travel entry logged while
  only a labour rate existed records the labour rate, so a shop that later adds
  a travel rate does not retrospectively re-price drives it already made.
- **Migration 083 backfills at the rate the reports were already using**, so it
  changes no number anybody can see. A shop with no rate set gets NULL, which is
  what its reports already showed.
- **NULL means uncosted, not free.** `UncostedMinutes` rides alongside the cost
  on every summary, and each surface says how many hours are unpriced rather
  than letting a total read as complete. `FullyCosted()` is the check.
- **An explicit `0.00` is a decision** — the shop absorbs the hour — and stays
  distinguishable from never having priced it. That is why the forms take
  strings: blank is not zero.
- **Repricing is by hand, one row at a time**, on the ticket the hour was logged
  on (`POST /admin/service/tickets/{id}/time/{childID}/rate`, audited as
  `service_ticket.time_repriced`). Blank returns an hour to unpriced without
  touching the minutes. This is the deliberate counterpart to the snapshot: a
  correction is a decision somebody makes and signs for, not a side effect of a
  settings save.

### Rescheduling on an interval change

`domain.RescheduledDue` shifts a pending occurrence by the difference between
the old and new intervals, rather than recomputing it from an anchor.

Recomputing was wrong in a way that took a review to find. The anchor came from
`max(completed_on)`, and a **skip leaves no completion** — so a skipped
occurrence jumped back to one interval after the last time the task was actually
done, months into the past. Past-due covered work is inside the sweep's booking
window, so the next night's run opened a real customer ticket for the visit the
customer had just declined.

Shifting needs no anchor: the occurrence's date was produced by adding the old
interval to *something*, so subtracting it and adding the new one keeps the same
cadence whatever that something was.

Two rules on top of the shift, both about not letting an interval edit rewrite
what is owed now:

- **Overdue work does not move at all.** It is outstanding today, and
  lengthening an interval must not clear it — a task one day late going from
  weekly to yearly would jump a year out, taking a warranty-critical job off the
  list nobody would then think to look for. The new interval governs the *next*
  occurrence, measured from whenever this one is finally done.
- **Everything else is shifted**, then stepped forward whole intervals if that
  lands it in the past, for the booking reason above. "Everything else" includes
  work due *today*: it is not late, so it moves with the future bucket.



## Open decisions

1. **Does service get billed?** The schema captures `billable` from day one.
   Schedules landed first; billing a ticket out as a draft order is still
   unbuilt. Auto-booked maintenance is opened `billable = false` on the grounds
   that a contract has already been paid for — revisit if service starts
   invoicing.
2. **Stale window default.** 7 days is a guess, and currently a constant
   (`domain.DefaultStaleContactWindow`) rather than a settings row — nothing has
   run long enough to argue with it. The sweep job in step 6 makes it
   configurable.
4. **Loaner machines and volume commitments.** If loaners are placed against a
   pounds-per-month commitment, tracking that commitment is a natural neighbour
   of this module — but it's a sales feature wearing a service costume. Keep it
   out of v1.

## Build order

1. **Done.** `modules` table (migration `076_modules.sql`) + `domain/module.go`
   + `app.ModuleService` + Settings → Modules tab + `requireModule` middleware
   + `filterNavItems` in the admin layout. Shipped alone, as planned.
2. **Done.** Migration `077_equipment_service.sql`, `domain/equipment.go` and
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
8. **Done.** Preventive maintenance (migration `081_service_plans.sql`): plans
   and their task series, assignment onto a machine from an anchor date with a
   contract flag, materialised occurrences that complete and re-anchor, the due
   list with its warranty-at-risk and call-list scopes, the month calendar, the
   Maintenance card on a machine's page, the dashboard chip, and the daily
   `service_maintenance_sweep` that backfills and books covered work. Details in
   *Preventive maintenance* above.
9. **Done.** Cost roll-ups: `CostSummary` widening `Totals` to a machine or an
   account over a window, the three-window card on the machine page, and the
   compact one on the customer rail. Details in *Cost roll-ups* above.
10. **Done.** The cross-account cost table at `/admin/service/costs`, ranked by
    hours, parts spend, or visits over 90 days / 12 months / all time. Read-only,
    so finance can open it. Details in *Cost roll-ups → The cross-account table*.
11. **Done.** Labour rates (migration `082_service_labor_rates.sql`) on a
    module-gated Settings → Service tab, and the Cost column and cost ranking
    they unlock across the three cost surfaces. Details in *Labour rates* above.
12. **Done.** Rate snapshots (migration `083_time_entry_rate_snapshot.sql`):
    `service_time_entries.rate_cents`, stamped at write time and backfilled at
    the rate then in force, plus the per-entry repricing form. Changing the
    shop's rate no longer re-costs the past. Details in *Labour rates → The
    snapshot*.
13. **Done.** Six rounds of independent review, every one of which failed the
    branch. The defects clustered: an interval edit that could book a visit the
    customer had declined; `AttachTicket` silently no-opping and crossing
    accounts; seventeen validation sentinels answering 500; `bg-rr-paper-warm`,
    which is not a token, leaving today unmarked on the calendar; Skip rejecting
    every attempt because the handler demanded a date its form never sent; and a
    helper rewritten by a careless regex into a call to itself, fatal to the
    process.

    Three of those were behaviours an earlier commit message *claimed* to have
    fixed and had not. That is the failure mode worth carrying forward from this
    build: none of the six was visible to the compiler, the linter, or the test
    suite, so a green `mage check` was never evidence against them. What caught
    them was opening the pages, posting what the forms actually post, and
    grepping each claimed fix back out of the tree before writing it down.
