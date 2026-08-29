# Lean Commerce — Backend Agent Conventions

You are working on a Go backend for a commerce platform. This file defines the rules, patterns, and guardrails that govern all backend code in this repository. Follow them without exception unless explicitly instructed otherwise by the developer.

---

## Stack

- **Language:** Go (1.21+)
- **Database:** PostgreSQL via `pgx/v5`
- **Templates:** `templ`
- **Job queue:** `river` (github.com/riverqueue/river)
- **Metrics:** `prometheus/client_golang`
- **Logging:** `log/slog` (standard library)
- **Migrations:** Goose + plain SQL in `db/migrations/`

---

## Package structure

The project is organized into the following packages under `internal/`. Know what belongs in each and never move logic between them without a clear reason.

```
internal/domain/    ← pure types only
internal/store/     ← all database access
internal/app/       ← all business logic
internal/web/       ← HTTP handlers and middleware
internal/ui/        ← templ templates
internal/jobs/      ← River worker definitions
internal/platform/  ← cross-cutting infrastructure (audit, metrics, sessions, auth, ratelimit, logging)
```

### Import rules — enforce strictly

Dependencies flow inward only. Never violate these boundaries:

```
web/      may import: app/, platform/*, domain/, ui/
ui/       may import: domain/ only
jobs/     may import: app/, platform/*, domain/
app/      may import: store/, platform/audit, platform/metrics, domain/
store/    may import: domain/ only
platform/ may import: domain/ only (audit sub-package only)
domain/   may import: nothing from this project
```

**If you find yourself needing to import `web/` from `app/`, stop.** The logic belongs in the handler, not the service. If you need to import `app/` from `store/`, stop — the store executes queries, it does not make business decisions.

---

## `internal/domain/` rules

- Contains Go types, enums, and constants only.
- No functions with business logic. No database tags. No HTTP tags.
- No imports from this project — standard library only (`time`, `uuid`, etc.).
- Status types are typed strings with named constants, not bare strings or ints:

```go
// CORRECT
type OrderStatus string
const (
    OrderStatusPending   OrderStatus = "pending"
    OrderStatusConfirmed OrderStatus = "confirmed"
)

// WRONG
status := "pending"
```

- Domain types are the shared vocabulary of the whole application. When in doubt about where a type belongs, it belongs in `domain/`.

---

## `internal/store/` rules

- One file per domain area: `orders.go`, `customers.go`, `catalog.go`, etc.
- Every function accepts `pgx.Tx` as its first data parameter (after `ctx`). The store never opens or commits transactions — callers control transaction scope.
- Customer-facing queries are always scoped to `customer_id`. Use naming to make this explicit:
  - `Get(ctx, tx, id, customerID)` — customer-scoped, safe for storefront use
  - `GetByID(ctx, tx, id)` — unscoped, staff use only, document this in the comment
- No business logic in the store. If a function is making a decision, it belongs in `app/`.
- No raw SQL strings scattered through handlers or services. All SQL lives in `store/`.

```go
// CORRECT — store executes, caller decides
func (s *OrderStore) UpdateStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.OrderStatus) (*domain.Order, error)

// WRONG — business decision in the store
func (s *OrderStore) RefundIfEligible(ctx context.Context, tx pgx.Tx, id uuid.UUID) error
```

---

## `internal/app/` rules

- One file per domain area: `orders.go`, `customers.go`, etc.
- Services own all business rules: validation, state machine transitions, eligibility checks.
- Every service method that modifies data must:
  1. Accept `pgx.Tx` as a parameter — never open its own transaction
  2. Call `audit.Record(ctx, tx, ...)` inside the same transaction
  3. Increment the relevant Prometheus counter after the transaction commits
  4. Enqueue any follow-on River jobs with `river.InsertTx(ctx, tx, ...)` inside the same transaction

```go
// CORRECT — all side effects are atomic
func (s *OrderService) Refund(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, amount int, reason string, actor domain.StaffActor) (*domain.Order, error) {
    order, err := s.orders.GetByID(ctx, tx, orderID)
    if err != nil { return nil, err }
    if order.PaymentStatus != domain.PaymentStatusCaptured {
        return nil, ErrOrderNotRefundable
    }
    updated, err := s.orders.ApplyRefund(ctx, tx, orderID, amount)
    if err != nil { return nil, err }
    s.audit.Record(ctx, tx, audit.AuditEntry{ ... })       // same tx
    s.river.InsertTx(ctx, tx, jobs.OrderRefundedArgs{ ... }) // same tx
    s.metrics.OrderRefundsTotal.Inc()                        // after tx
    return updated, nil
}

// WRONG — opens its own transaction, audit not guaranteed
func (s *OrderService) Refund(ctx context.Context, orderID uuid.UUID, ...) error {
    tx, _ := s.db.Begin(ctx)
    ...
    tx.Commit(ctx)
    s.audit.Record(...)  // if process dies here, no audit record
}
```

- Sentinel errors are defined in `app/errors.go` and checked by handlers with `errors.Is`. Never return raw strings as errors from service methods.
- Services never produce HTTP status codes, never write to `http.ResponseWriter`, never read from `*http.Request`.

---

## `internal/web/` rules

- Handlers are thin. They parse the request, call one service method, render the response.
- No business logic in handlers. No SQL in handlers. No direct database calls.
- No authorization logic in handlers — that is the middleware's job. By the time a handler runs, the actor is already verified.
- Read the actor from context; never pass it through query parameters or request headers:

```go
// CORRECT
actor := auth.StaffFromContext(r.Context())

// WRONG
actorID := r.URL.Query().Get("actor_id")
```

- All responses go through `web.Render` (for templ) or `web.JSON` (for API endpoints). Never call `component.Render` directly or `json.NewEncoder(w).Encode` directly.
- All errors go through `web.Error(w, r, err)`. Never write error strings directly to `w`.
- Route registration lives in `web/router.go`. Middleware is applied at registration time, not inside handlers.
- The `store.Tx` helper is used for all transaction management in handlers:

```go
// CORRECT
err := store.Tx(r.Context(), deps.DB, func(tx pgx.Tx) error {
    _, err := deps.Orders.Refund(r.Context(), tx, orderID, amount, reason, actor)
    return err
})

// WRONG — manual transaction management scattered through handlers
tx, _ := deps.DB.Begin(r.Context())
defer tx.Rollback(r.Context())
...
tx.Commit(r.Context())
```

---

## Transaction and audit patterns

These two rules are non-negotiable:

**Rule 1: Every write that touches audited domain state must audit in the same transaction.**
Audited entities: orders, payments, products, pricing, subscriptions, customers (staff-initiated changes including tax exemption), staff accounts, discounts, shipping configuration.
If you add a new write to any of these entities, add `audit.Record` to the same transaction before considering the implementation complete.

**Rule 2: Every River job that is enqueued as a result of a domain write must be enqueued in the same transaction.**
A job enqueued after `tx.Commit()` may be lost if the process dies. A job enqueued with `InsertTx` inside the transaction is guaranteed to exist if and only if the write committed.

```go
// The pattern — audit + job enqueue in same tx as domain write
store.Tx(ctx, db, func(tx pgx.Tx) error {
    result, err := store.DoTheThing(ctx, tx, ...)
    if err != nil { return err }
    audit.Record(ctx, tx, ...)          // same tx
    river.InsertTx(ctx, tx, args, nil)  // same tx
    return nil
})
// Metrics increment happens here, outside the tx
```

---

## Error handling rules

- Sentinel errors live in `app/errors.go`. Add new ones there, not inline.
- Handlers use `errors.Is` to map app errors to HTTP status codes in `web.Error`.
- When adding a new error condition in a service, also add its HTTP mapping in `web/respond.go`.
- Never swallow errors silently. If an error can be ignored, document why with a comment.
- Never log an error and also return it — pick one. Logging happens at the boundary (handler or worker), not deep in the call stack.

```go
// CORRECT — return the error, let the boundary log it
func (s *OrderService) Refund(...) error {
    if err := s.orders.ApplyRefund(...); err != nil {
        return fmt.Errorf("apply refund: %w", err)
    }
    return nil
}

// WRONG — logs and returns, results in duplicate log entries
func (s *OrderService) Refund(...) error {
    if err := s.orders.ApplyRefund(...); err != nil {
        slog.Error("apply refund failed", "err", err)
        return err
    }
    return nil
}
```

- Wrap errors with context using `fmt.Errorf("operation: %w", err)`. The wrapping message should describe what the code was trying to do, not what went wrong.

---

## Infrastructure guardrails

These are hard boundaries. Never cross them.

**Authentication and sessions belong in `platform/sessions/` and `web/middleware.go`.** No service or repository function checks session validity, reads a token, or calls `platform/sessions` directly.

**Authorization belongs in middleware, not handlers or services.** Permission checks are applied at route registration time in `web/router.go`. A handler that contains `if actor.Role != admin` is wrong.

Per-route admin permissions use the `deps.requirePermission(perm, next)` middleware, mounted on `adminMux` *inside* `requireStaffSession` (which puts the authenticated staff on the context). Wrap each protected route with a `platform/auth.Perm*` constant:

```go
adminMux.Handle("GET /admin/staff", deps.requirePermission(auth.PermManageStaff, http.HandlerFunc(deps.handleAdminStaffList)))
adminMux.Handle("POST /admin/staff", deps.requirePermission(auth.PermManageStaff, http.HandlerFunc(deps.handleAdminStaffInvite)))
```

The middleware resolves the staff from the context, checks `auth.HasPermission(role, perm)`, and writes `app.ErrPermissionDenied` (→ 403) on failure. Services and handlers stay permission-agnostic — they receive an already-authorized request. Roles map to permissions in one place: `platform/auth.rolePermissions`. (A UI helper like `domain.StaffRole.CanManageStaff()` may mirror a grant to hide a nav affordance, but it is *not* the gate — the middleware is; keep the two in sync.)

**Customer data scoping belongs in `store/`, enforced by function signatures.** A repository function that returns customer orders must require `customerID` as a parameter. This is not optional — it is how ownership is enforced.

**Rate limiting belongs in `platform/ratelimit/` and is applied in `web/router.go`.** No handler or service checks rate limit state.

**Metrics belong in `app/` services and `internal/platform/metrics/`.** No handler directly increments a Prometheus counter. Handlers call services; services increment metrics.

**Structured logging uses `LoggerFromContext(ctx)`, never `slog.Default()` inside a request.** The request-scoped logger carries `request_id` and actor fields. Using `slog.Default()` in a handler drops this context.

---

## Naming conventions

**Files:** `snake_case.go`. One domain area per file within a package. Don't create `utils.go` or `helpers.go` — if something needs a home, find the right package.

**Types:** `PascalCase`. Exported types have doc comments.

**Functions and methods:** `camelCase` for unexported, `PascalCase` for exported. Constructor functions are `New<Type>`. Predicate functions start with `Is` or `Has` or `Can`.

**Error variables:** `Err<Description>` — `ErrNotFound`, `ErrOrderNotRefundable`, `ErrInvalidCredentials`.

**Store methods:**
- `Get` — single record, customer-scoped
- `GetByID` — single record, unscoped (staff use; document it)
- `List` — multiple records
- `Create` — insert
- `Update<Field>` — partial update by named field
- `Delete` — soft or hard delete

**Audit action constants:** `Audit<Resource><Verb>` in `platform/audit/actions.go`:
- `AuditOrderRefunded`
- `AuditSubscriptionCancelled`
- `AuditVariantPriceUpdated`

**Permission constants:** `Perm<Resource><Action>` in `platform/auth/permissions.go`:
- `PermIssueRefunds`
- `PermManageProducts`
- `PermViewCustomers`

---

## Background job rules

- One file per job type in `internal/jobs/`.
- Job args structs are named `<JobType>Args` and implement `river.JobArgs` with a `Kind()` method returning a stable snake_case string.
- Workers are thin: open a transaction with `store.Tx`, call one service method, return.
- Workers never touch the database directly — they call `app/` services.
- All jobs must be idempotent. A job may run more than once. Design accordingly.
- Background jobs use `domain.SystemActor` for audit records — never fabricate a staff actor.
- Include `"river_job_id": job.ID` in audit entry `metadata` for all jobs that produce audit records.

---

## What to do when you are unsure

- If unsure which package a new function belongs in: ask yourself "does this make a business decision?" If yes, `app/`. "Does this execute a query?" If yes, `store/`. "Does this render HTML?" If yes, `ui/`. "Does this handle an HTTP request?" If yes, `web/`.
- If unsure whether to add an audit record: ask "would a merchant or auditor want to know this changed?" If yes, audit it.
- If unsure whether to enqueue a job in the transaction: ask "is this job meaningless if the transaction rolls back?" If yes, it goes inside the transaction with `InsertTx`.
- If you are about to add a new infrastructure concern (caching, feature flags, external API client): add it as a new sub-package under `platform/`, inject it as a dependency, never import it from `domain/` or `store/`.
- **External service integrations** (shipping label providers, tax services, email providers) always go behind an interface in `platform/`. The interface lives in `platform/<concern>/provider.go`. Concrete implementations (`shippo.go`, `easypost.go`) live alongside it. The rest of the application depends only on the interface — never on a concrete provider type. This means swapping providers is a one-line change in `cmd/server/main.go`.
- **External calls never happen inside a database transaction.** If a service method needs to call an external API and then write the result to the database, the external call happens first (outside any transaction), and the database write happens after in its own transaction. Document the failure mode in a comment if the external call can succeed while the subsequent DB write fails.

---

## Design rationale

Non-obvious decisions and constraints that aren't visible from the code alone. Most "what" and "how" questions are answered by reading `internal/`; this section captures the "why" — alternatives rejected, hidden trade-offs, prior incidents, and warnings worth keeping around.

### Domain & pricing

- **Pricing is a separate domain, not a column on Product.** Variants have no price column. Lets currency variants, quantity tiers, customer-group rates, and promotional overrides exist without touching product tables.
- **Plans are decoupled from products.** A `SubscriptionPlan` defines *how often*, not *what*. Customers can swap products without changing schedule, or change frequency without changing product.
- **Subscription intervals are day-based, not calendar-based.** "Every 14/30/60 days" is predictable; "monthly" raises 28/30/31-day ambiguity.
- **Order, payment, and fulfillment statuses are three separate enums.** A "complete" order may still have outstanding payments; a paid order may be only partially fulfilled. Collapsing them produces a confusing matrix.
- **Line item prices are denormalized at order time.** Captures what the customer actually paid even if catalog pricing later changes.

### Subscriptions

- **Subscriptions are a scheduling mechanism, not a billing engine.** Each renewal generates a standard Order + PaymentIntent — no Stripe Subscriptions/Billing objects. Reasons: (1) WooCommerce migration compatibility, since existing customers had PaymentIntents and not Billing tokens; (2) full control over retry/grace logic without being constrained by Stripe's subscription lifecycle.
- **Renewal payment-method lookup uses `ListPaymentMethods[0]` of any type, not filtered to `"card"`.** Filtering to card breaks Stripe Link customers.

### Discounts

- **Fixed-amount discounts are capped at subtotal — never go negative.** Avoids "negative subtotal" states and matches user intuition.
- **Coupon redemption uses optimistic locking (`WHERE redeemed_at IS NULL` + `UPDATE ... RETURNING`), not distributed locks.** Two concurrent checkouts both pass the "not yet redeemed" read; only one `UPDATE` returns a row. The loser sees `ErrCouponAlreadyRedeemed`. Scales without Redis.

### Wholesale & B2B

- **Wholesale and retail are separate account types, not tiers.** Different login endpoints (password vs magic link), different portals, different price lists. Prevents cross-customer data leaks and lets each evolve independently.
- **Declined wholesale applications are not deleted** — status stays `pending` so admin can revisit.
- **Product visibility has three levels: public / wholesale / restricted.** A restricted product with no group assignments is invisible to non-staff — useful for prepping items for a not-yet-onboarded wholesale client.
- **Standing orders (B2B recurring) use the same Order+PaymentIntent pattern as retail subscriptions.** No separate billing engine.
- **QuickBooks invoicing is per order, not a weekly consolidated invoice per customer** (decided 2026-08-29). A consolidated bill was the original description, and it is what a customer receiving four deliveries a week would rather open. It was rejected on reconciliation: per-order means the QB DocNumber *is* the Hiri order number, so an invoice, a payment and an order line up one-to-one, and anything that needs chasing in QuickBooks traces back to a single order rather than to a bundle somebody has to decompose first. It is also what makes the adopt-by-DocNumber idempotency probe possible at all. The weekly roll-up a customer wants is available without rebuilding any of this: QBO Statements group a customer's open invoices into one document. Revisit only if the volume of invoices per customer becomes the complaint — consolidation is a rework of the job chain, not a setting.

### Tax

- **WA sales tax is wired up but currently dormant.** Bagged coffee falls under WA's food-for-home-consumption exemption; products are flagged `tax_exempt = true`. To activate for non-exempt items: `UPDATE products SET tax_exempt = false WHERE id = ...;` (no admin UI for this yet).
- **Tax is calculated on post-discount subtotal**, with each line item's taxable amount proportionally reduced. A 10% coupon reduces the tax bill too, not just the order price.
- **Wholesale orders unconditionally skip tax**, regardless of store config — B2B customers expect this.

### Shipping

- **Shipping rates at checkout are calculated internally** (flat rate + free threshold). No external API call on the hot path.
- **Label provider calls happen before the shipment record is persisted.** If the provider succeeds but the DB write fails, the label still exists in the provider's dashboard and the order number is in the Reference field for manual lookup.

### Authentication

- **Sessions are DB-backed opaque tokens, not JWTs.** Instant revocation matters more than statelessness for a small-team ecom: staff deactivation, account takeover response, and "log out all devices" all require killing sessions immediately — impossible with JWTs without a denylist (which reintroduces the DB lookup).
- **Tokens are SHA-256 hashed at rest.** Raw token is sent to the client exactly once; only the hash is stored. A DB exfiltration doesn't expose live tokens.
- **Customer and staff auth are separate endpoints with separate session rows.** A compromised customer token cannot be presented to a staff endpoint — session lookup always scopes by `actor_type`.
- **Guest customers are persistent, not disposable.** `is_guest = true`, no password. On registration with the same email, cart/addresses/orders merge into the permanent account. Guest record is updated in place.
- **Email verification gates checkout.** Closes a class of bot signups that never pay.
- **Wholesale 2FA is architected but not enabled.** The `magic_link_tokens` table serves both retail passwordless auth and wholesale 2FA. To turn on: set `two_fa_enabled = true` on the customer; the login handler will issue a magic link after password verification. No schema change needed.

### Rate limiting

- **Token bucket, not fixed windows.** Fixed windows allow a burst at minute 0 and another at minute 1; token bucket captures burst capacity natively while catching sustained hammering.
- **Email keys are SHA-256 hashed in the rate-limit store.** No plaintext PII in the limiter.
- **Rate limiter fails open on store error.** Don't compound a Redis outage by locking out all users — log, alert, let traffic through.
- **The `Store` interface is the seam for Redis migration.** `InMemoryStore` works for single-server; `RedisStore` (with Lua atomics) is the migration path. Middleware, limits, and headers stay identical.

### Audit

- **`actor_name` is denormalized onto audit records.** Staff get deactivated and renamed; storing the name at action time keeps rows self-contained and human-readable forever.
- **`action` is a namespaced string constant, not a Postgres enum.** Enums require migrations to extend; new actions ship by adding a constant in `platform/audit/actions.go`. The namespace prefix (`order.`, `subscription.`) makes the log queryable per domain.
- **No `before` snapshot stored.** The previous state is recoverable by querying the most recent audit row before the event. Avoids doubling storage for a rarely-needed query pattern.

### Observability

- **`/metrics` listens on `127.0.0.1:9090` by default**, not the public port. Metrics expose webhook counts, queue depth, DB pool stats — leaking publicly is real information disclosure. Production exposes via reverse proxy with IP allowlist + basic auth.
- **URL paths are normalized for metrics labels.** `/orders/{id}` is the label, not `/orders/abc-123`. Without this every UUID creates a new Prometheus time series and cardinality explodes.
- **`request_id` is the bridge between metrics and logs.** Alert fires on error rate → filter Loki by the time window and `level=ERROR` → grab the `request_id` → see the full request trace.
- **Promtail label promotion: `level`, `request_id`, `actor_id` only.** Promoting `order_id` / `customer_id` would index-bloat Loki — keep those as JSON line content and filter via `| json | order_id="..."`.
- **Metrics are incremented at the service layer, not the handler.** The service is where the business event happens and has access to label values (amount, currency, plan, …).

### Testing

- **Test isolation is per-transaction rollback, not per-test truncation.** `testutil.NewTestTx` opens a tx that always rolls back. No test ordering dependencies, no shared state, no truncation scripts.
- **Coupon redemption race is the highest-priority concurrency test.** Two concurrent attempts on a single-use code — exactly one must succeed. Without this test, optimistic-locking bugs ship to production.
- **Svelte checkout endpoints have golden-file contract tests.** Response shape changes fail CI; updating the golden requires `go test -update`. Prevents silent Go/Svelte API drift.

### UI

- **Charts are pure rendering, never computation.** Callers compute aggregates, normalize magnitudes to [0, 1], and pre-format labels. The chart partial knows nothing about business types — keeps it reusable across any future report.
- **Toasts are server-driven via `HX-Trigger`.** No JS required for the happy path; survives JS failure.
- **Feedback patterns are four distinct types, never mixed:** inline validation (field-level, on blur), toasts (success/error, auto-dismiss), inline confirmation (destructive actions, no centered modals), progress indicators (multi-step). Mixing produces inconsistent UX.

### Frontend deployment

- **The Svelte checkout is single-file, `go:embed`'d into the binary.** No separate frontend deploy, no CDN required for the critical path. The production binary is fully self-contained.
