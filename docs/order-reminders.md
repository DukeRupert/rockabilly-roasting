# Weekly wholesale order reminder

The "get your order in before the cutoff" nudge that wholesale customers receive
every week. This ran as a **standalone Go service** (repo `rr`, Echo + SQLite +
gocron, deployed to its own VPS container at `ghcr.io/dukerupert/rr`) against the
Orderspace API until 2026-08. It now runs inside Hiri as a River periodic job.

**The `rr` service can be shut down.** Nothing in Hiri talks to Orderspace.

---

## What it does

Every **Friday at 10:00 merchant-local**, a periodic job scans for eligible
accounts and enqueues one email job per account.

An account is eligible when **all** of these hold:

| Condition | Why |
|-----------|-----|
| `account_type = 'wholesale'` and `wholesale_status = 'approved'` | Retail customers and pending/suspended/declined accounts are never reminded |
| At least one order with `channel = 'wholesale'` placed in the last **21 days** | The reminder is for accounts that are actively buying; a dormant account needs a sales call, not an automated nudge |
| That order's status is not `cancelled` or `refunded` | A cancelled order is not buying activity |
| Their **most recent** order is older than **7 days** | Someone who ordered Wednesday doesn't need Friday's nudge. A few weeks of irrelevant reminders teaches people to ignore the email, which costs more than the missed prompt |
| `customers.order_reminders_enabled = true` | Per-customer opt-out |

The window is measured against `orders.placed_at`, never `created_at`, so
imported and backfilled orders sort by real-world order date.

The 7-day suppression equals the reminder interval by definition, so the rule
reads as "skip anyone who has ordered since the last time we asked". It keys off
the *most recent* order (a `HAVING` on the aggregate), so a customer who ordered
three weeks ago and again on Wednesday is correctly skipped.

## Reordering from the email

The reminder prints the customer's last order — item names, as labelled in the
confirmation email, with quantities — so the "do I need this again?" decision
happens in the inbox rather than after a login. The CTA is **Reorder This**,
pointing at `GET /wholesale/reorder`.

That route resolves "last order" server-side at click time rather than baking an
order ID into a URL that sits in an inbox indefinitely, and shares
`lastWholesaleOrder` with the email so the two can never disagree about which
order "last time" means. It then runs the same cart-loading path as the
order-history Reorder button (`reorderIntoCart`): set semantics, retired or
no-longer-available variants skipped and counted, landing on checkout with the
explanatory banner.

A mutating GET is acceptable here because the route sits behind the wholesale
auth guard — inbox link scanners and prefetchers arrive with no session cookie,
so they get the login redirect and never touch a cart.

Logged-out clicks now round-trip: `requireCustomerSession` carries the requested
page through `?redirect=`, validated by `safeNextOr` against off-site bounces,
and the login form preserves it across a failed password attempt. GETs only —
replaying a POST after login would re-submit something the customer never
re-confirmed. This fixed a pre-existing gap that dropped the destination for
*any* wholesale deep link or bookmark hit after session expiry.

If the customer has no completed wholesale order, the link lands on the
quick-order sheet; the portal's "Same as last time?" card hides itself, so the
page explains the situation without needing a banner.

Eligibility is re-checked at send time, not just at scan time — an account
suspended in the gap between the scan and the send (which River retries can
widen to hours) is skipped silently rather than mailed.

---

## Staff controls

**Preview + one-off notice — `/admin/wholesale/reminders`**
(Customers → Reminders)

- Lists exactly who the next scheduled send would email, with each account's
  last order date. It runs the *same query* the scheduler uses, so it is a true
  dry run rather than an approximation.
- "Send a one-off notice" mails a staff-composed subject and body to that same
  audience — for a corrected cutoff or a holiday schedule. Requires
  `customers:write`. Body is **plain text**; blank lines start new paragraphs,
  and it renders into the branded shell (staff cannot inject HTML). Every send
  is recorded in the audit log per recipient.
  - A notice deliberately **ignores** the weekly-reminder opt-out — it is
    operational, not promotional — but still never goes to an account that is
    no longer approved wholesale.

## Customer opt-out

The reminder footer carries a signed unsubscribe link, and the message carries
`List-Unsubscribe` / `List-Unsubscribe-Post` headers so Gmail and Apple Mail
render their own native unsubscribe control. Both land on the same endpoint and
both do exactly what the staff toggle does — clear
`customers.order_reminders_enabled` — but the audit row records the **customer**
as the actor, so it is obvious afterwards who asked to stop.

Tokens are stateless HMACs (`platform/auth/unsubscribe.go`), not rows in
`magic_link_tokens`. An unsubscribe link must keep working for as long as the
email sits in an inbox, so there is no sensible expiry to enforce, and a stored
token would mean one row per recipient per week written purely to support a link
most people never click. The customer ID travels in the clear and the HMAC is
what makes it unforgeable — so this token authorizes exactly one low-stakes
action and must never be treated as proof of identity anywhere else.

**GET never unsubscribes.** `GET /wholesale/unsubscribe` only renders a
confirmation page; the button POSTs. This is not ceremony: corporate mail
gateways and inbox scanners (Outlook Safe Links and friends) fetch every link in
an incoming message, so a GET that acted would let a customer's own IT
department unsubscribe them without anyone clicking. `POST` also accepts the
token from the query string, which is what RFC 8058 one-click needs — Gmail
POSTs the header URL directly, and that request comes from the mail provider
acting on a real click, so it applies immediately and returns bare `200`.

The done page offers **Turn reminders back on** (`POST /wholesale/resubscribe`),
because mis-clicks happen and the alternative is the customer emailing staff.

Copy is explicit that this stops *only* the weekly reminder — order
confirmations, shipping notices and invoices are unaffected.

Set `UNSUBSCRIBE_SECRET` to enable any of this. Unset degrades safely: no link,
no headers, footer falls back to "reply and we'll take you off the list", and
the server warns at boot. Rotating the secret invalidates every outstanding link
in already-delivered mail.

**Per-customer opt-out — customer detail page**

The wholesale settings card has a "Weekly order reminder" On/Off toggle. Both
directions are audited (`customer.order_reminders_enabled` /
`customer.order_reminders_disabled`).

---

## Configuration

All optional; the defaults reproduce the old service's schedule.

| Variable | Default | Notes |
|----------|---------|-------|
| `ORDER_REMINDER_WEEKDAY` | `Friday` | Weekday name, case-insensitive |
| `ORDER_REMINDER_HOUR` | `10` | 0–23 |
| `ORDER_REMINDER_TIMEZONE` | `MERCHANT_TIMEZONE` | IANA name |
| `DISABLE_ORDER_REMINDERS` | unset | Any value stops the weekly job. Use in dev/staging |
| `UNSUBSCRIBE_SECRET` | unset | Signs opt-out links. Unset = no link, no `List-Unsubscribe` headers, reply-to fallback |

> **Timezone discrepancy — decide before the first prod send.** The old `rr`
> service hardcoded `America/Denver`, while `MERCHANT_TIMEZONE` here defaults to
> `America/Los_Angeles` (and the reminder email copy says "Washington State").
> Unset, the reminder therefore fires an hour *later* in absolute terms than it
> used to. `ORDER_REMINDER_TIMEZONE` exists so this can be pinned to Denver
> without also moving the subscription renewal anchor, which reads
> `MERCHANT_TIMEZONE`. If Pacific is correct, leave it unset and delete this note.

The schedule is implemented by `jobs.WeeklySchedule` rather than River's
`PeriodicInterval`, which drifts against the wall clock and is timezone-blind: a
168h interval anchored at process boot lands on a different weekday after every
deploy and slides an hour at each DST transition. `WeeklySchedule` holds the
local hour across DST and rolls a full week if the process restarts after the
send time, so a deploy on Friday afternoon cannot trigger a second batch.

`RunOnStart` is deliberately **off** — a deploy must never fire an unscheduled
blast at the whole active wholesale list.

---

## Where the code lives

| Concern | File |
|---------|------|
| Audience query | `store/customers.go` → `ListOrderReminderRecipients` |
| Send logic, opt-out, notices | `app/order_reminders.go` |
| Weekly schedule | `jobs/weekly_schedule.go` |
| Scheduler + per-customer send jobs | `jobs/order_reminder.go` |
| Notice job | `jobs/wholesale_notice.go` |
| Admin page + toggle handlers | `web/admin_reminders.go`, `ui/admin/wholesale_reminders.templ` |
| Reorder deep link | `web/wholesale.go` → `handleWholesaleReorderLatest`, `reorderIntoCart` |
| Login return-trip | `web/customer_auth.go` → `wholesaleLoginWithReturn`, `safeNextOr` |
| Opt-out token | `platform/auth/unsubscribe.go` |
| Opt-out endpoints + pages | `web/unsubscribe.go`, `ui/storefront/unsubscribe.templ` |
| Email templates | `emailtemplates/{html,text}/order_reminder.*`, `wholesale_notice.*` |
| Schema | `db/migrations/062_customer_order_reminders.sql` |

Job kinds: `order_reminder_scheduler`, `order_reminder`, `wholesale_notice`.
Reminder sends are unique per customer per 7 days, so a scheduler retry or two
server instances firing the periodic job cannot double-mail an account.

---

## What deliberately did not carry over

| `rr` feature | Disposition |
|--------------|-------------|
| Orderspace OAuth client, token cache, customer/order sync | Dropped — Hiri owns this data |
| SQLite database and its `customers`/`orders`/`tokens` tables | Dropped |
| `GET /api/customers`, `GET /api/orders` | Dropped — admin pages cover this |
| `GET /api/email/preview-reminders` (unauthenticated; mailed the preview to a hardcoded address) | Replaced by the on-screen admin preview |
| `POST /api/email/send-adhoc` (unauthenticated; accepted raw HTML) | Replaced by the audited, permission-gated notice composer |
| Per-recipient inline sending in one loop | Replaced by fan-out to one job per recipient, so a single bad address retries instead of being logged and dropped |
| Orderspace storefront link in the email | Now points at `BASE_URL/wholesale` |

---

## Decommissioning `rr`

1. Confirm one successful send from Hiri (audit log: `email.order_reminder_sent`).
2. Stop the container on the VPS: `cd /opt/rr && docker compose down -v`.
3. Remove the Caddy reverse-proxy entry for it.
4. Disable the `deploy.yml` workflow / archive the `rr` repo.
5. Revoke the Orderspace API credentials once Orderspace itself is shut down.
