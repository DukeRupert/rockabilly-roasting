# Admin detail pages

A companion to `docs/admin-ui.md`. That doc covers *what things look like* (tokens, classes,
the deny-list the lint enforces). This one covers *how a detail page is put together* — the
`/admin/<thing>/{id}` screens where staff read one record and act on it.

It was written after rebuilding `order_show.templ`, which had drifted a long way from the
shape `customer_show.templ` already had. Most of what follows is not new invention: it is
the customer page's structure written down, plus the failure modes that showed up when a
page grew for two years without anyone re-reading it end to end.

---

## The shape

`internal/ui/admin/customer_show.templ` is the reference implementation. Read it before
building or reworking one of these.

```
header          identity + every status badge + computed back path + secondary actions
banner          (optional) the one thing that needs attention before you touch anything
grid            main column + rail
  main          what this record IS and what happened to it
  rail          what you can DO about it, who it belongs to, how it is configured
  activity      timeline at the bottom of the main column
```

**Main column** holds the substance: line items, orders, subscriptions, shipments, tables,
and the activity log. **Rail** holds actions and identity: the next action, payment, the
customer, addresses, configuration, notes, danger zone. The rail is `sticky top-6 self-start`
so it stays put while the main column scrolls.

### Rail width and breakpoint

Use a **fixed-width rail**, not a proportional one:

```
class="mt-6 grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]"
```

A proportional rail (`lg:grid-cols-3` + `lg:col-span-2`) keeps growing on wide monitors, which
a stack of small cards does not need. `22rem` fits a textarea, a full-width button, and an
address without wrapping.

Pick the breakpoint from what the **main column** has to hold:

| Main column contains | Breakpoint |
| --- | --- |
| Cards, lists, narrow tables | `lg:` (1024px) |
| A wide table — 5+ columns, like order line items | `xl:` (1280px) |

Below the breakpoint everything stacks into one column, which is the old behaviour and safe
by construction.

> **Known divergence.** `customer_show.templ` still uses the proportional `lg:grid-cols-3`
> form. `order_show.templ` uses fixed `22rem` at `xl:`. Between 1024 and 1280px they behave
> differently. Aligning the customer page to the fixed rail is an open follow-up.

---

## Six rules

### 1. Facts above actions, or beside them — never below

The single worst thing about the old order page: it opened with eighteen stacked full-width
bands, and *every action came before every fact*. You were asked to decide Fulfill / Cancel /
Refund before you could see the total, the customer, the address, or what was in the box.
Staff read down, then scrolled back up to act.

The main+rail grid fixes this structurally — the rail sits *beside* the facts, not above
them. If you find yourself adding a full-width action row above the content, that is the
smell.

### 2. One home per class of action

| Class | Home |
| --- | --- |
| Advances the workflow (fulfill, ship, mark paid) | Rail, "Next action" card |
| Undoes a mistake (revert fulfillment/shipment) | Rail, under a "Correct a mistake" divider |
| Cannot be walked back (cancel, refund, delete) | Rail, "Danger zone" card at the bottom |
| Print / export / open elsewhere | Header, **secondary** styling |
| Acts on a sub-record (a shipment, an address) | Next to that sub-record, in the main column |

The old page had actions in five places, including two state-changing buttons buried inside
a metadata strip that read as text. **A control that changes state must never live in a
block that looks read-only.**

### 3. Show every status axis, always

An order has three independent axes — order status, payment status, fulfillment status. The
old page rendered the order badge *only when cancelled or refunded*, inferred payment from a
progress bar, and never showed fulfillment at all.

Put all axes in the header as badges, unconditionally. And be wary of progress indicators
that model a happy path: the old three-step bar returned `-1` and vanished entirely for
cancelled orders — losing the timeline exactly when someone most wants to know how far the
order got. Four badges said more than the bar did and never disappear.

### 4. Absence needs a reason

These pages gate buttons behind `can*()` predicates — `canBuyLabel`, `canRefundOrder`,
`canMarkOrderPaid` and four more on the order page alone. When they all return false the
page renders *nothing*, and staff cannot tell a closed record from a broken feature.

Give every action area a fallback that explains itself:

```go
func nextActionReason(props OrderShowProps) string {
	switch {
	case o.Status == domain.OrderStatusCancelled:
		return "This order is cancelled. Nothing left to do here."
	case o.PaymentStatus != domain.PaymentStatusCaptured:
		return "Waiting on payment. Fulfillment unlocks once it clears."
	...
```

### 5. Every detail page gets an activity timeline

`AuditQueryService.ListByResource(ctx, tx, "<type>", id)` already exists, and
`timelineRow` in `timeline.templ` is shared. A page needs only a `<thing>EventLabel` mapper
and a `<thing>EventMarker` colour function.

Two things worth knowing:

- **Email history comes free.** Sends are audited (`email.order_shipped`,
  `email.order_confirmed`, …), so the same query answers "what were they actually told?"
  Label those entries as "Emailed …" — it is the most common question the timeline gets.
- **Merge related resources.** An order's story includes its shipments. Query each and
  merge newest-first; a single `resource_id` lookup tells half the story.

Coverage today:

| Page | Timeline |
| --- | --- |
| `customer_show` | yes (`ListForCustomer` — actor *or* resource) |
| `order_show` | yes (order + shipments merged) |
| `subscription_show` | yes |
| `invoice_show` | yes |
| `route_show` | yes (route + its stops' orders, merged) |
| `price_list_show` | yes |
| `announcement_show` | yes |
| `discount_show` | yes |
| `product_edit` | yes (product + its variants, merged — its own **Activity** tab) |

All of them go through one shared `ActivityTimeline(entries, label, marker, loc)` in
`timeline.templ`. A page contributes only the two functions and a thin named wrapper
(`RouteActivity`, `InvoiceActivity`, …) so call sites read well. Do not hand-roll the heading
and `<ol>` again — there is a render test asserting the wrappers still share one shape.

`CustomerActivity` is the one wrapper that takes more than the shared four arguments: it
also takes the customer's id, and renders a link to `/admin/audit?customer_id=…` under the
timeline. The timeline is capped at 25 entries, so without that link the cap is a dead end —
staff can see older events exist and have no route to them. Adding a *third* wrapper with its
own extra argument is a sign the extra thing belongs in `ActivityTimeline` itself; note that
`TestActivityWrappers_ShareOneShape` asserts the shared heading and `<ol>` are present, not
that nothing follows them, so it will not catch divergence on its own.

`product_edit` is the odd one out in shape: it is tabbed rather than main+rail, so its
timeline is a sixth tab instead of a section under the main column. That keeps the audit
query off the four tabs staff actually live in. Two things are deliberately true of it:
archived variants are included (pulling a SKU is exactly what someone comes to attribute),
and `product_media.*` events are **not** merged in — those are keyed to the media row, and a
deleted image takes its id with it, so the only reachable half would be additions. Half a
story is worse than a named gap.

**Check where an event is actually keyed before assuming `ListByResource` is enough.** The
route page is the cautionary case: `delivery_route.stop_skipped` is recorded against the
**order**, not the route, so the obvious lookup silently omitted the one event a delivery
route page most needs to show. `AuditQueryService.ListByRelatedResource` exists for this —
it takes the related ids plus an action prefix, so a route gets its stops'
`delivery_route.*` events without dragging in each order's entire history.

### 6. Audit the model for fields you render nowhere

`Order.InternalNote` sat on the model for the life of the app with no way to read or write
it. That is a whole feature, invisible, and nothing catches it — the compiler is happy, the
tests pass, the lint is silent.

Run this against any type behind a detail page:

```bash
python3 - <<'PYEOF'
import re, glob
ui = "".join(open(f).read() for f in glob.glob('internal/ui/admin/*.templ'))
name, path = 'Customer', 'internal/domain/customer.go'
m = re.search(r'type '+name+r' struct \{\n(.*?)\n\}', open(path).read(), re.S)
skip = {'ID','CreatedAt','UpdatedAt','Metadata','PasswordHash','CurrencyCode'}
for line in m.group(1).split('\n'):
    fm = re.match(r'\t([A-Z]\w+)\s+\S', line)
    if fm and fm.group(1) not in skip and not fm.group(1).endswith('ID'):
        if ('.'+fm.group(1)) not in ui:
            print("never rendered:", fm.group(1))
PYEOF
```

Every hit is a judgement call, not automatically a bug. Triage each one into three buckets,
because they need different responses:

**Real gaps** — written and/or enforced, invisible in admin. All the ones this scan first
turned up have since been closed; the bucket is kept because the next run will refill it.

**Dead scaffolding** — a field nothing writes. Not a display gap; there is nothing to show.
Deleting it, or finishing the feature, is the fix — rendering it is not.

- **`Customer.TwoFAEnabled` / `TwoFAMethod`** — read out of the database into the struct and
  never set by anything. There is no 2FA feature.
- **`PriceList.StartsAt` / `EndsAt`** — were the same: neither CreatePriceList nor
  UpdatePriceList ever set them. Removed from the type rather than rendered.

**Deliberate** — leave alone.

- `DeliveryRoute.ShareToken` is the QR link secret. `Order.OverdueReminderStage` is a reminder
  dedup ledger. `DeliveryRoute.OriginLat/Lng`, `EndLat/Lng` are routing inputs that belong in
  shipping settings, not on a route record.

Two traps in the scan itself, both of which bit:

- It matches `.FieldName` as a Go expression, so a field whose form input is a **string
  literal** (`name="minimum_order"`) looks unrendered when it is merely un-*read*-back.
- A `written in Go` heuristic also misses **database defaults**. `CustomerUser.InvitedAt` was
  reported dead; the column is `invited_at timestamptz NOT NULL DEFAULT now()`.
- A `written in Go` heuristic that looks for `Field =` misses raw SQL. `DeliveryRoute.ActivatedAt`
  was reported "never written" when `SET activated_at = now()` writes it. Always confirm
  against `internal/store/*.go` and `db/queries/*.sql` before concluding a column is dead.

`Variant.WholesaleMinQty` / `WholesaleMultiple` were on this list and are now fixed — they were
the worst kind, an ordering rule *enforced at wholesale checkout* whose only writer was the
white-label variant copier, so the values could only ever have come from the importer or
hand-written SQL. Live data existed in the wild that no one could see or change.

The scan also caught the wholesale application fields — `Website`, `WholesaleNotes`,
`ApprovedAt`/`ApprovedBy` — which have since been fixed. That case is worth keeping as the
worked example, because the three fields had failed in three *different* ways and only one
of them matched the `InternalNote` shape:

| Field | What was actually wrong |
| --- | --- |
| `Website` | Collected on the public form, stored, rendered nowhere. The `InternalNote` shape. |
| `ApprovedAt` / `ApprovedBy` | Written correctly on approval, rendered nowhere. |
| `WholesaleNotes` | The decline *reason*. Store method, service, handler and audit all complete — but no form ever submitted a `notes` field and no page ever read it back. Written empty every time, for years. |

The last one is the instructive one: a grep for the field name finds plenty of hits and the
feature looks finished. **A write path with no form feeding it is as invisible as no write
path at all.** When the scan flags a field, trace it to a *form input* and to a *rendered
element*, not merely to a function that mentions it.

---

## Mechanical traps

Small, silent, and each one bit during the order rework.

**Back links must be computed when the list is split.** One record type can have two list
pages — orders split retail/wholesale, customers split by segment. A hardcoded
`href="/admin/orders"` sends you to the wrong list. Derive it the same way the page derives
its `ActivePath`:

```go
<a href={ templ.SafeURL(orderShowActivePath(props.Order)) }>&larr; Back to orders</a>
```

Still hardcoded today: `subscription_show.templ:154`, `price_list_show.templ:41`. Both are
currently correct — neither list is split — but they will break silently on the day one is.

**Element ids inside `for` loops must be scoped.** `@ActionWrap("hint-download-label", …)`
inside a shipments loop produces duplicate ids on any order with two labels, and every
`aria-describedby` resolves to the first one. Scope it:

```go
func shipmentHintID(prefix string, id uuid.UUID) string { return prefix + "-" + id.String() }
```

**An error response that is only a toast must cancel the swap.** `toast.Toast` is a single
root element carrying `hx-swap-oob`, and htmx lifts every top-level OOB element out of the
response before the normal swap runs — what is left is an empty fragment. A form that targets
itself (`hx-target="this" hx-swap="outerHTML"`) therefore *deletes itself* on any rejection,
and a boosted form blanks the page body. `web.Error` and the `render*Error` helpers now send:

```go
w.Header().Set("HX-Reswap", "none")
```

which cancels only the main swap; OOB toasts still land. A handler that re-renders a real
panel alongside the toast does not need it — its fragment is not empty.

**`<textarea>` renders its leading whitespace.** A templ `if` block inside a textarea gets
indented by the generator and that indentation becomes part of the value. Use a Go
expression on one line instead:

```go
<textarea …>{ internalNoteValue(props.Order) }</textarea>
```

**New arbitrary Tailwind classes need `mage css`.** `mage templ` alone will not emit
`xl:grid-cols-[minmax(0,1fr)_22rem]`, and the page silently renders single-column. The
stylesheet is at `internal/ui/assets/css/output.css` (*not* `static/css/`) and its `<link>`
carries no cache-busting param, so a browser will keep serving the old sheet after a rebuild.

---

## Writing the copy

Action hints and confirm dialogs are part of the design, not decoration. The house style,
already consistent across `ActionWrap` and `ConfirmAttrs`:

- State what happens, as a plain fact: *"Records the order as picked and packed."*
- **Always say whether the customer is emailed.** Every hint on the order page does. This is
  the thing staff most need to know before clicking and cannot find out afterwards.
- Say what the action does *not* do: *"This refunds the postage only — it does not refund
  the customer's order."*
- Make the hint conditional when the answer is conditional. `markShippedHint()` returns
  different copy depending on whether a label exists, because that determines whether an
  email actually goes out.
- Confirm `Points` are statements, not warnings. No "Are you sure?", no exclamation marks.

---

## Checklist

For a new or reworked detail page:

- [ ] Main + rail grid; fixed `22rem` rail; breakpoint chosen from the main column's widest content
- [ ] Header: identity, **all** status badges unconditionally, computed back path
- [ ] Secondary styling on print/export; exactly one primary CTA on screen
- [ ] Actions grouped by class, each in its one home; none inside a read-only-looking block
- [ ] Every action area explains itself when empty
- [ ] Activity timeline at the bottom of the main column, related resources merged
- [ ] Field-coverage scan run against the domain type; each hit ruled on
- [ ] Action hints say whether the customer gets an email
- [ ] `mage check` passes (`lint` + `checkAdminUI` + `checkScoping` + tests)
- [ ] Looked at in a browser, at both a wide and a narrow width
