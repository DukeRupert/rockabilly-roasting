# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Hiri is a green-field single-merchant ecommerce platform written in Go. It deploys as a **single binary** — HTTP server and River job workers run in one process. The project is currently in the architecture/design phase with comprehensive design docs in `docs/`.

## Tech Stack

- **Go 1.25+**, PostgreSQL via `pgx/v5`, `templ` for server-rendered HTML
- **Job queue:** River (`riverqueue/river`) — transactional job enqueueing
- **Checkout:** Svelte component compiled to JS bundle, embedded via `go:embed`
- **Metrics:** Prometheus, **Logging:** `log/slog` (JSON), **Errors:** Sentry
- **Migrations:** Goose + plain SQL in `db/migrations/`
- **Testing:** `testify/assert` + `uber/mock`
- **External services:** Stripe (payments + tax), Shippo/EasyPost (shipping), AWS S3 (assets), go-mail (email)

## Build & Run Commands

```bash
# Build the server
go build ./cmd/server

# Run tests
go test ./...

# Run a single test
go test ./internal/app/ -run TestOrderRefund

# Generate templ templates
templ generate

# Build checkout Svelte bundle
cd ui/checkout && npm run build

# Run migrations
goose -dir db/migrations postgres "$DATABASE_URL" up

# Generate mocks
go generate ./...
```

## Architecture — Strict Layered Packages

All application code lives under `internal/`. Dependencies flow **inward only** — violating these import rules is a compile error:

```
domain/   → nothing from this project (pure types, enums, constants)
store/    → domain/
app/      → store/, platform/audit, platform/metrics, domain/
web/      → app/, platform/*, domain/, ui/
ui/       → domain/ only
jobs/     → app/, platform/*, domain/
platform/ → domain/ only (audit sub-package)
```

**Package responsibilities:**
- `domain/` — shared vocabulary types, status enums as typed strings, no logic
- `store/` — all SQL, one file per domain area, every function takes `pgx.Tx` (callers control transactions)
- `app/` — all business logic, validation, state machines. Sentinel errors in `app/errors.go`
- `web/` — thin HTTP handlers: parse request → call service → render response. Route registration + middleware in `router.go`
- `ui/` — templ templates organized as `layouts/`, `storefront/`, `admin/`, `components/`
- `jobs/` — River workers, one file per job type. Workers are thin: open tx, call service, return
- `platform/` — infrastructure sub-packages: `audit/`, `metrics/`, `sessions/`, `auth/`, `ratelimit/`, `shipping/`, `logging/`

Entry point: `cmd/server/main.go` wires all dependencies.

## Critical Patterns

### Transaction + Audit + Job Atomicity
Every data-modifying service method must:
1. Accept `pgx.Tx` (never open its own transaction)
2. Call `audit.Record(ctx, tx, ...)` in the **same** transaction
3. Enqueue River jobs with `river.InsertTx(ctx, tx, ...)` in the **same** transaction
4. Increment Prometheus metrics **after** the transaction commits

Handlers use `store.Tx(ctx, db, func(tx pgx.Tx) error { ... })` for transaction scope.

### Customer Data Scoping
Customer-facing store methods require `customerID` as a parameter — this is how ownership is enforced at the type level:
- `Get(ctx, tx, id, customerID)` — customer-scoped (storefront)
- `GetByID(ctx, tx, id)` — unscoped (staff only)

### Authorization
- **Customers:** ownership via query scoping (required `customerID` param)
- **Staff:** coarse RBAC (5 roles: admin, fulfillment, finance, catalog, support)
- Permission checks in middleware (`web/router.go`), never in handlers or services

### Error Handling
- Sentinel errors in `app/errors.go`, checked with `errors.Is()`
- HTTP mapping in `web/respond.go`
- Never log-and-return — pick one. Logging happens at the boundary
- Wrap with context: `fmt.Errorf("operation: %w", err)`

### External Service Calls
- Always behind an interface in `platform/<concern>/provider.go`
- **Never inside a database transaction** — call external API first, then write result in its own tx
- Concrete implementations (`shippo.go`, `easypost.go`) live alongside the interface

### Background Jobs
- Job args: `<Type>Args` implementing `river.JobArgs` with `Kind()` returning snake_case
- Must be idempotent — jobs may run more than once
- Use `domain.SystemActor` for audit records, include `"river_job_id"` in metadata

## Naming Conventions

- **Files:** `snake_case.go`, one domain area per file. No `utils.go` or `helpers.go`
- **Store methods:** `Get` (customer-scoped), `GetByID` (staff), `List`, `Create`, `Update<Field>`, `Delete`
- **Errors:** `Err<Description>` (e.g., `ErrOrderNotRefundable`)
- **Audit actions:** `Audit<Resource><Verb>` in `platform/audit/actions.go`
- **Permissions:** `Perm<Resource><Action>` in `platform/auth/permissions.go`
- **Status types:** typed strings with named constants, never bare strings

## Key Design Docs

Detailed specifications live in `docs/`:
- `CLAUDE-backend.md` — full backend conventions (the authoritative reference)
- `lean-commerce-package-structure.md` — directory tree and import graph
- `lean-commerce-domain-model.md` — 6 domains: Catalog, Pricing, Customer, Order, Fulfillment, Subscription
- `lean-commerce-auth.md` — session strategy, customer vs staff auth flows
- `lean-commerce-infrastructure.md` — 8 infrastructure concerns
- `UI_DIRECTION.md` — design system, color palette, component specs
