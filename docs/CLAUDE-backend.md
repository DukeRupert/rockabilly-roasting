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

**Authorization belongs in middleware, not handlers or services.** Permission checks (`auth.RequirePermission(...)`) are applied at route registration time in `web/router.go`. A handler that contains `if actor.Role != admin` is wrong.

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
