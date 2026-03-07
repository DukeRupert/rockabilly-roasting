# Lean Commerce — Critical Infrastructure

Companion to the Core Domain Model. This document is a summary of infrastructure decisions. Each concern has a dedicated detailed document; this doc records the *what* and *why* of each decision for quick reference.

Each concern is a separate, bounded responsibility. They compose with the domain through well-defined interfaces rather than being woven into domain logic.

---

## 1. Authentication & Sessions

**Decision: database-backed opaque sessions, two actor types only.**

Actors in scope: **customers** (retail/wholesale) and **staff** (merchant operators). Background jobs run under application database credentials and do not need sessions.

Sessions are opaque random tokens (32 bytes, hex-encoded). The raw token is sent to the client exactly once. `SHA-256(raw_token)` is stored in the database — never the raw token. On every request the application hashes the presented token and looks it up.

```sql
CREATE TABLE sessions (
    id           uuid PRIMARY KEY,
    actor_type   session_actor_type NOT NULL,  -- 'customer' | 'staff'
    actor_id     uuid NOT NULL,
    token_hash   text NOT NULL UNIQUE,
    ip_address   text,
    user_agent   text,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
```

A single `sessions` table serves both actor types. The `actor_type` column discriminates — a customer token cannot authenticate a staff request.

**Session lifetimes:**

| Actor | Condition | Lifetime |
|---|---|---|
| Customer | Remember me | 30 days |
| Customer | No remember me | 24 hours |
| Guest customer | — | 72 hours |
| Staff | — | 8 hours |

**Why not JWTs:** JWTs cannot be revoked before expiry without a denylist — which reintroduces a database lookup. Staff termination, account takeover response, and "log out all devices" all require instant revocation. Database-backed sessions provide this simply.

**Auth flows are separate by design.** `/auth/customer/login` and `/auth/staff/login` are distinct endpoints with distinct rate limits and audit rules. A shared `/auth/login` is not used.

**Guest customers** get a temporary record (`is_guest = true`) with a 72-hour session. On registration with the same email, cart, addresses, and orders merge into the permanent account — no new row is created.

**Passwords** are hashed with bcrypt. The hash is never returned in API responses.

**Email verification** is required before a customer can place orders. Unverified accounts exist but cannot complete checkout.

*Full detail: `lean-commerce-auth.md`*

---

## 2. Authorization & Permissions

**Decision: resource ownership for customers; coarse RBAC for staff.**

These are two separate models operating on different axes. They are never mixed.

**Customers — ownership by query construction.**
Customers have no roles. Authorization is enforced by scoping every query to the authenticated customer's ID:

```sql
SELECT * FROM orders WHERE id = $1 AND customer_id = $2
```

`customer_id` is a required parameter on every repository function that returns customer-sensitive data. Omitting it is a compile error, not a runtime access check. When a row doesn't belong to the requesting customer the query returns nothing — the handler returns 404, not 403 (403 leaks that the resource exists).

**Staff — coarse role enum.**
Staff roles are defined as a single enum column on the `staff` table. The role-to-permission mapping lives in code as a `map[StaffRole][]string` — not in the database. Changing who can do what is a deployment, not a runtime admin action.

| Role | Key capabilities |
|---|---|
| admin | Everything |
| fulfillment | View orders, update fulfillment, manage inventory |
| finance | View orders, issue refunds, view reports, manage pricing |
| catalog | Create/edit products, manage pricing, manage inventory |
| support | View orders, view customers, create draft orders |

Permission checks run as middleware, wrapping routes at registration time. Handlers and services contain no authorization logic — they receive a request, trust the middleware stack, and do their work.

**Layering rule:** rate limiting → session middleware → permission middleware → handler → service/repository. Domain code is unaware of who called it.

*Full detail: `lean-commerce-auth.md`*

---

## 3. Audit Logging

**Decision: staff + system actors; after snapshot; same transaction as the domain change.**

**What is logged:** staff-initiated actions and system/background job actions. Customer self-service actions (place order, update address) are recoverable from domain data and are not logged separately.

**What is captured:** action + resource reference + after-state snapshot. No before snapshot — the previous state is always queryable as the most recent prior audit row for the same resource. After snapshots preserve point-in-time state that would otherwise be overwritten.

**Where it's written:** inside the same database transaction as the domain change. A refund that commits without an audit record is worse than a failed refund. The `AuditWriter.Record(ctx, tx, entry)` signature enforces this — it never opens its own transaction.

```sql
CREATE TABLE audit_log (
    id            uuid PRIMARY KEY,
    occurred_at   timestamptz NOT NULL DEFAULT now(),
    actor_type    audit_actor_type NOT NULL,  -- 'staff' | 'system'
    actor_id      uuid,          -- null for pure system events
    actor_name    text,          -- denormalized: name at time of action
    action        text NOT NULL, -- namespaced: 'order.refunded', 'subscription.cancelled'
    resource_type text NOT NULL,
    resource_id   uuid NOT NULL,
    after         jsonb,         -- post-change snapshot; null for deletions
    request_id    text,          -- correlates to application log
    ip_address    text,
    reason        text,
    metadata      jsonb
);
```

**`actor_name` is denormalized** — staff can be deactivated or renamed. The audit record must be self-contained and human-readable in perpetuity.

**Actions are namespaced string constants** (`order.refunded`, `subscription.cancelled`) defined in one place in code. Enums require migrations to extend; string constants do not.

**Audited entities** — all staff-initiated writes to these resources produce an audit record in the same transaction:
- Orders (status changes, refunds)
- Payments (capture, refund, void)
- Products and variant pricing
- Subscriptions (pause, cancel, skip, renewal)
- Customers — staff-initiated changes only (tax exemption grant/revocation)
- Staff accounts (role changes)
- Discounts (created, updated, deactivated)
- Shipping configuration (rate or threshold changes)

**Background jobs** use a sentinel `SystemActor` (`actor_id = uuid.Nil`, `actor_name = "system"`). The `river_job_id` is included in `metadata` to correlate an audit entry to the exact River job that produced it.

Audit log rows are append-only and never updated or deleted. Retention: keep 2 years hot; archive older rows to cold storage. Partition by `occurred_at` when the table exceeds tens of millions of rows.

*Full detail: `lean-commerce-audit.md`*

---

## 4. Telemetry & Metrics

**Decision: Prometheus + Grafana, custom registry, two dashboards.**

Library: `github.com/prometheus/client_golang` (MIT licensed). Metrics are registered on a custom `prometheus.Registry` — not the default global — to prevent conflicts and simplify testing.

The `/metrics` endpoint is exposed on an internal port only (not the public-facing port). Prometheus scrapes every 15 seconds.

**Two classes of metrics serving two questions:**

*Is the system healthy?*
- `http_requests_total` — counter, labels: method, path (normalized), status
- `http_request_duration_seconds` — histogram, same labels
- `payments_failed_total` — counter, labels: provider, reason
- `job_queue_depth` — gauge, label: kind

*What is the business doing?*
- `orders_placed_total` — counter, label: currency
- `orders_revenue_cents_total` — counter, label: currency
- `payments_captured_total` — counter, label: provider
- `subscription_renewals_total` — counter, label: status (success/failed/skipped)
- `active_subscriptions` — gauge, label: plan_interval

**Path normalization is required.** `/orders/{id}` is the correct label value; `/orders/abc-123` is not. Without normalization, every UUID in a URL path creates a new Prometheus time series — cardinality explodes and scraping degrades. Use the router's route template, not the matched URL.

Business metrics are incremented at the **service layer**, not the handler layer — the service is where the business event occurs and has access to the relevant values.

**Two Grafana dashboards:**
1. *System Health* — request rate, error rate, p50/p95/p99 latency, payment failure rate, job queue depth
2. *Business Activity* — orders per hour, revenue per hour, subscription renewal success rate, active subscriptions by interval

**Minimum alert set:**
- Error rate > 5% for 2 minutes
- p95 latency > 2s for 5 minutes
- Job queue depth > 500 for 10 minutes
- Subscription renewal failure rate elevated for 5 minutes
- Login rate limit hits elevated for 2 minutes (credential stuffing signal)

*Full detail: `lean-commerce-observability.md`*

---

## 5. Structured Application Logging

**Decision: `log/slog` with JSON handler to stdout; Promtail → Loki.**

Library: Go standard library `log/slog` (Go 1.21+). No external logging dependency. JSON output to stdout; Promtail ships logs to Loki.

**Standard fields on every log line:**

| Field | Description |
|---|---|
| `request_id` | Unique per HTTP request; set by logging middleware; propagated through context |
| `actor_id` | Authenticated actor ID; added by session middleware after identity is established |
| `actor_type` | `customer` \| `staff` \| `system` |
| `method` | HTTP method |
| `path` | URL path — never `r.URL.String()`, which includes query params that may contain tokens |
| `status` | HTTP response status code |
| `duration_ms` | Request processing time |
| `service` | `lean-commerce` — disambiguates in multi-app Loki |
| `env` | `production` \| `staging` |

**Request-scoped logger via context.** The logging middleware builds a logger with base fields and injects it into the request context. The session middleware enriches it with actor fields after identity is resolved. Downstream handlers call `LoggerFromContext(ctx)` — they never construct their own logger.

**Log levels:**
- `DEBUG` — detailed flow tracing; off in production
- `INFO` — business events (order placed, payment captured, subscription renewed) and request completion
- `WARN` — unexpected but handled (payment retry attempt, deprecated API call)
- `ERROR` — failures requiring investigation (unhandled error, database failure, River job exhausted retries)

A 404 is not an error. A 422 is not an error. `ERROR` means a human should look at this.

**Key business events** get explicit `INFO` log lines with structured fields (`order_id`, `customer_id`, `amount`, `currency`). These are the lines queried in Loki when investigating a specific order or renewal.

**Loki label promotion (Promtail config):** `level`, `request_id`, and `actor_id` are promoted to Loki labels — low cardinality, used as exact-match filters. `order_id` and similar high-cardinality fields stay as log line content and are filtered with `| json | order_id="..."` in LogQL. Promoting high-cardinality fields to labels causes Loki index bloat.

**The `request_id` is the bridge between metrics and logs.** A Grafana alert fires on elevated error rate → filter Loki by `level=ERROR` in the same window → find the `request_id` → see the full request trace in sequence.

*Full detail: `lean-commerce-observability.md`*

---

## 6. Rate Limiting & Abuse Prevention

**Decision: token bucket; auth endpoints only; in-memory now, Redis migration path designed.**

**Scope:** authentication endpoints only — login (customer and staff), register, password reset. General API browsing is not rate limited at this stage.

**Algorithm: token bucket.** Each actor gets a bucket with a capacity (burst allowance) that replenishes at a fixed rate. Sustained hammering drains the bucket; legitimate sporadic use is absorbed by burst capacity.

**Keys:** per IP address and/or per hashed email. Email is SHA-256 hashed before use as a map key (cache key — no salt needed).

**Configurations:**

| Endpoint | Key | Capacity | Rate |
|---|---|---|---|
| Customer login | per IP | 5 | 0.1 req/s |
| Customer login | per email | 5 | 0.1 req/s |
| Staff login | per IP | 3 | 0.05 req/s |
| Staff login | per email | 3 | 0.05 req/s |
| Customer register | per IP | 10 | 0.003 req/s |
| Password reset | per email | 3 | 0.001 req/s |
| Password reset | per IP | 10 | 0.003 req/s |

**The `Store` interface is the migration seam:**

```go
type Store interface {
    Allow(key string, cfg LimitConfig) (allowed bool, remaining float64, retryAfter time.Duration)
}
```

`InMemoryStore` implements this today. `RedisStore` (Lua script, atomic bucket operations) implements the same interface when multiple instances require shared state. Swapping is a one-line change at startup.

**Middleware ordering on auth endpoints:**
1. IP rate limit — runs first, no body parsing needed
2. Body parsing — parse JSON once, store in context
3. Email rate limit — reads email from context
4. Handler

**IP extraction** trusts `X-Forwarded-For` / `X-Real-IP` only when the connection originates from a trusted proxy. Blind trust of these headers allows clients to spoof their IP and bypass IP-based limits.

**Fail-open on store unavailability.** If the rate limit store fails, requests are allowed through rather than blocking all users. The error is logged and alerted on separately.

**Rate limit hits are tracked in Prometheus** (`rate_limit_hits_total`, labels: config, key_type). Alert configured on elevated login hits — the signal for active credential stuffing.

*Full detail: `lean-commerce-rate-limiting.md`*

---

## 7. Webhook Integrity & Idempotency

**Decision: `stripe.ConstructEvent` for signature + replay protection; status state machine; River for async processing.**

**Signature verification** uses `stripe.ConstructEvent(rawBody, sigHeader, secret)` from `github.com/stripe/stripe-go`. Verifies HMAC signature and enforces a 5-minute timestamp tolerance (defeats replay attacks). Raw body must be read before any middleware parses it — signature verification is over raw bytes.

**Idempotency** is enforced by a unique constraint on `(provider, provider_event_id)` in `webhook_events`. A duplicate delivery hits the constraint and is ignored before any processing begins.

**Webhook event status is a state machine:**

```
pending → processing → processed
                    ↘ failed → dead (after max attempts)
```

The `dead` state is distinct from `failed` — a job that exhausts retries must graduate to dead for human review rather than retrying indefinitely.

**Processing flow:**

```
1. Read raw body bytes
2. stripe.ConstructEvent — verify signature + timestamp
3. BEGIN TRANSACTION
   INSERT INTO webhook_events ON CONFLICT (provider, provider_event_id) DO NOTHING
   If no row returned → duplicate, COMMIT, return 200
4. River.InsertTx(tx, ProcessEventArgs{...})
5. COMMIT — idempotency record and job are atomic
6. Return 200 immediately
7. River worker processes the event, updates status
```

Steps 3–5 are atomic. The idempotency record and the River job commit together or not at all. Safe to interrupt at any point.

*Full detail: prior research session; `lean-commerce-infrastructure.md` §7.*

---

## 8. Background Jobs & Scheduled Tasks

**Decision: River (`github.com/riverqueue/river`, MPL-2.0), Postgres-backed, transactional enqueueing.**

River was chosen over a hand-rolled `SKIP LOCKED` queue because it solves real edge cases (transactional enqueueing, dead letter handling, leader election for scheduled tasks, job UI) without adding an external service dependency. License confirmed compatible with proprietary SaaS use.

**Transactional enqueueing is the key property.** Jobs inserted with `River.InsertTx(tx, args)` inside a domain transaction are visible to workers if and only if that transaction commits. This eliminates the class of bugs where a job references data that doesn't exist yet.

**At-least-once semantics.** River re-queues jobs claimed but not completed within a visibility timeout. All job handlers must be idempotent.

**Common jobs:**

| Job | Trigger | Criticality |
|---|---|---|
| Send order confirmation email | Order placed | High |
| Generate subscription renewal order | `next_order_at` reached | High |
| Retry failed subscription payment | Payment failed event | High |
| Release expired cart reservations | Hourly schedule | Medium |
| Send abandoned cart email | 1hr after cart inactive | Medium |
| Restock alert notification | Stock level threshold | Medium |
| Prune expired sessions | Nightly schedule | Low |
| Generate daily sales report | Nightly schedule | Low |

**Scheduled task deduplication** is handled by River's built-in leader election (`river_leader` table) — no manual distributed lock implementation needed.

**River UI** (`github.com/riverqueue/riverui`, MPL-2.0) provides a self-hosted interface for queue visibility — inspect jobs, cancel, retry, view error history.

*Full detail: `lean-commerce-observability.md` (metrics), `lean-commerce-audit.md` (audit integration).*

---

## 9. Email Delivery & Notifications

**Decision: provider-agnostic `Sender` interface; embedded HTML/text templates; transactional job enqueueing.**

Email delivery is split into two packages with distinct responsibilities:

### `platform/email/` — Delivery Abstraction

The `Sender` interface decouples the application from any specific email provider:

```go
type Sender interface {
    Send(ctx context.Context, msg Message) (*SendResult, error)
    SendTemplate(ctx context.Context, msg TemplatedMessage) (*SendResult, error)
}
```

`Message` carries pre-rendered HTML + text bodies. `TemplatedMessage` delegates rendering to the provider's server-side templates (Postmark template aliases, etc.). `SendResult` returns the provider's message ID for debugging.

**Current implementation: Postmark** (`PostmarkSender` via `mrz1836/postmark`). Swapping to SendGrid, AWS SES, or any other provider means implementing the same two-method interface — no application code changes.

**`TestSender`** captures sent messages in-memory for unit tests. Thread-safe, provides `Last()` and `Reset()` helpers. Tests assert on email content without network calls.

### `emailtemplates/` — Template Rendering

A standalone package that owns all email HTML/text content. Templates are embedded into the binary via `go:embed` — no filesystem dependency at runtime.

```go
//go:embed html/*.html text/*.txt
var templateFiles embed.FS
```

**Template pairs:** every email has both an HTML and a plain-text variant, rendered from separate template files. The `Renderer.Render(name, data)` method returns both bodies.

**Six template types:**
- `order_confirm` — order placed confirmation with line items and totals
- `subscription_confirm` — new subscription welcome with plan details
- `invoice_sent` — invoice notification with payment link
- `magic_link` — passwordless login link
- `wholesale_approved` — wholesale application approval notification
- `wholesale_application` — staff notification of new wholesale application

**Template data structs** are defined in the renderer package. Money values are passed as cents (int); the `formatCents` template helper renders `$18.00`. Dates use `formatDate` which handles both `time.Time` and `*time.Time`. `FormatAddress` is exported for workers that need to build address strings.

**HTML templates use inline CSS** for email client compatibility. Brand palette: cream background (`#F5F0E6`), dark text (`#1A1612`), red CTA buttons (`#C0271D`).

### Notification Flow

Email sending always goes through a River job — never sent synchronously in an HTTP handler:

```
Handler
  └─▶ store.Tx(ctx, db, func(tx) {
          // Domain write
          order = checkoutService.PlaceOrder(ctx, tx, ...)
          // Enqueue email in same transaction
          river.InsertTx(ctx, tx, OrderConfirmEmailArgs{OrderID: order.ID}, nil)
      })
```

The job worker loads the data it needs, calls `Renderer.Render()`, then calls `Sender.Send()`. External email delivery happens outside any database transaction.

**Why transactional enqueueing matters:** if the order transaction rolls back, the email job disappears too. No orphaned confirmation emails for orders that don't exist.

**Worker constructors receive:** the relevant stores (to load data), the `pgxpool.Pool` (for read transactions), the `Sender`, the `Renderer`, and config values (`fromAddr`, `baseURL`, `storeName`). Workers are thin: load data → render template → send email.

**Environment variables:**
- `POSTMARK_SERVER_TOKEN` — Postmark API key
- `EMAIL_FROM` — sender address (e.g., `orders@example.com`)
- `BASE_URL` — used in email links (e.g., `https://shop.example.com`)
- `STORE_NAME` — used in email subject lines and body text
- `STAFF_NOTIFICATION_EMAIL` — recipient for staff-facing notifications (wholesale applications)

---

## Domain concerns with dedicated design documents

The following domain concerns have separate detailed documents that complement this infrastructure overview:

| Concern | Document | Key decisions |
|---|---|---|
| Tax calculation | `lean-commerce-tax.md` | Stripe Tax for standard customers; admin-controlled exemption for B2B; tax frozen on order at purchase |
| Shipping rates | `lean-commerce-shipping.md` | Flat rate + free shipping threshold (internal rules); label generation via Shippo/EasyPost behind a `LabelProvider` interface |
| Discounts & coupons | `lean-commerce-discounts.md` | Percentage off, fixed amount, free shipping; automatic discounts + single-use coupon codes; `Discount.Evaluate()` is a pure domain function |

These concerns integrate with the infrastructure layer as follows:
- **Tax, shipping, discounts** all produce values frozen onto the order at placement — `tax_total`, `shipping_total`, `discount_total`. These are written inside the same transaction as the order record.
- **Tax exemption grant/revocation** is an audited staff action (`customer.tax_exemption_granted`, `customer.tax_exemption_revoked`).
- **Shipping label generation** is an audited staff action (`shipment.label_created`). The external provider call (Shippo/EasyPost) happens outside the transaction; the result is written inside it.
- **Discount configuration changes** are audited staff actions (`discount.created`, `discount.updated`, `discount.deactivated`). Discount application is recorded in `order_discounts`, not the audit log.
- **Coupon code redemption** uses an optimistic database update (`WHERE redeemed_at IS NULL`) to handle concurrent checkout attempts without distributed locks.

---

## How the Nine Concerns Compose

```
HTTP Request
    │
    ├─▶ Rate Limiting          (IP check before body parse; email check after)
    │
    ├─▶ Logging Middleware     (assign request_id; build scoped logger)
    │
    ├─▶ Session Middleware     (hash token → DB lookup → attach actor to ctx)
    │       └─▶ Enrich logger with actor_id, actor_type
    │
    ├─▶ Permission Middleware  (staff routes only: check role → 403 if denied)
    │
    ├─▶ Domain Handler
    │       └─▶ Service method (explicit tx + actor params)
    │               ├─▶ Repository write    (inside tx)
    │               ├─▶ AuditWriter.Record  (inside same tx)
    │               └─▶ River.InsertTx      (inside same tx, if job needed)
    │
    ├─▶ Logging Middleware     (emit request log: status, duration_ms)
    │
    └─▶ Metrics Middleware     (increment http_requests_total, observe duration)

Webhook Request
    │
    ├─▶ Read raw body bytes
    ├─▶ stripe.ConstructEvent  (verify signature + replay window)
    ├─▶ BEGIN TRANSACTION
    │       ├─▶ INSERT webhook_events ON CONFLICT DO NOTHING
    │       └─▶ River.InsertTx (enqueue processing job)
    ├─▶ COMMIT
    └─▶ Return 200 immediately

Background Job (River worker)
    │
    ├─▶ BEGIN TRANSACTION
    ├─▶ Domain logic           (same services as HTTP handlers)
    ├─▶ AuditWriter.Record     (inside tx, SystemActor, river_job_id in metadata)
    ├─▶ COMMIT
    └─▶ Metrics increment      (jobs_processed_total, subscription_renewals_total, etc.)
```

Email Notification (River worker)
    │
    ├─▶ Load data (order, customer, etc.) — read-only tx
    ├─▶ Renderer.Render(name, data) — produces HTML + text bodies
    └─▶ Sender.Send(ctx, message) — external call, no tx

**The invariant that holds throughout:** domain logic sits in the middle of every stack, unaware of what wraps it. Services receive explicit parameters — `tx`, `actor`, `customerID` — and operate on them. No service reaches into a context for authorization state, no repository omits a `customer_id` scope, no handler contains a rate limit check. Each concern is independently testable, independently replaceable, and auditable by reading a single layer.
