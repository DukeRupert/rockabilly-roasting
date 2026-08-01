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
| `customers.order_reminders_enabled = true` | Per-customer opt-out |

The window is measured against `orders.placed_at`, never `created_at`, so
imported and backfilled orders sort by real-world order date.

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
