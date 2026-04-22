# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Hiri is a single-merchant ecommerce platform written in Go. It deploys as a **single binary** — HTTP server and River job workers run in one process. Implementation is well underway; comprehensive design docs live in `docs/`.

**Naming:** The platform codename is **Hiri** (Go module `github.com/dukerupert/hiri`). The client/storefront brand is **Rockabilly Roasting Co.** — the storefront has been reskinned, but the admin panel still says "Hiri" internally (it's the platform name, not customer-facing).

## Tech Stack

- **Go 1.25+**, PostgreSQL via `pgx/v5`, `templ` for server-rendered HTML
- **Job queue:** River (`riverqueue/river`) — transactional job enqueueing
- **Checkout:** Svelte component compiled to JS bundle, embedded via `go:embed`
- **Metrics:** Prometheus, **Logging:** `log/slog` (JSON), **Errors:** Sentry
- **Migrations:** Goose + plain SQL in `db/migrations/`
- **Testing:** `testify` (assert + require) with testcontainers-go for Postgres
- **External services:** Stripe (payments + tax), EasyPost (shipping), Cloudflare Images (product media), Cloudflare R2 (shipping labels, S3-compatible), Postmark (email via `mrz1836/postmark`)

## Local Development Setup

```bash
docker compose up -d          # Postgres 17 on localhost:5433
cp .env.example .env          # fill in API keys
mage db:migrate               # run migrations
mage seed                     # create admin user (set SEED_EMAIL, SEED_PASSWORD, SEED_NAME)
mage dev                      # generate templ + CSS, build, and run server
```

## Build & Run Commands

This project uses [Mage](https://magefile.org/) as a Go-native task runner. Install with `go install github.com/magefile/mage@latest`. Targets are defined in `magefiles/mage.go`.

```bash
mage              # default: generate + build + run (same as mage dev)
mage dev          # generate templ + CSS, build, and run server
mage build        # compile server binary only
mage templ        # generate templ templates
mage css          # compile Tailwind CSS (minified)
mage watch        # run Tailwind CSS in watch mode
mage checkout     # build Svelte checkout bundle
mage seed         # create admin staff user (set SEED_EMAIL, SEED_PASSWORD, SEED_NAME)
mage test         # run all tests
mage testVerbose  # run tests with verbose output
mage lint         # run static analysis (go vet)
mage check        # lint + test (CI gate)
mage generate     # run go generate ./...
mage clean        # remove build artifacts

# Database migrations (require DATABASE_URL)
mage db:migrate   # run pending migrations
mage db:rollback  # roll back last migration
mage db:status    # show migration status
mage db:create <name>  # create new migration file

# Data-migration / one-off commands (separate main packages under cmd/)
mage wcMigrate    # WooCommerce subscription importer (cmd/migrate) — supports --dry-run, --mapping=path/to/mapping.json
go run ./cmd/os-migrate  # Orderspace migration (no mage target)
go run ./cmd/seed        # same thing mage seed runs

# Run a single test (use go test directly)
go test ./internal/app/ -run TestOrderRefund

# List all targets
mage -l
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
- `platform/` — infrastructure sub-packages: `audit/`, `auth/`, `email/`, `logging/`, `media/`, `metrics/`, `payments/`, `quickbooks/`, `ratelimit/`, `sessions/`, `shipping/`, `tax/`
- `emailtemplates/` — templ-based email templates
- `testutil/` — shared test helpers

Entry point: `cmd/server/main.go` wires all dependencies. Uses `godotenv` to load `.env` if present.

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
- External services with pluggable implementations (email, payments, shipping, QB OAuth) expose an interface in `platform/<concern>/provider.go`. Concrete implementations (`postmark.go`, `stripe.go`, `easypost.go`) live alongside the interface.
- Internal platform concerns with a single implementation (`audit`, `metrics`, `sessions`, `media`, `tax`, `logging`, `help`, `ratelimit`, `auth`) expose concrete types directly — no `provider.go` needed. If a second implementation becomes necessary, extract the interface at that point.
- **Never inside a database transaction** — call external API first, then write result in its own tx. The RenewalService two-phase pattern (read tx → external call → write tx) is the template.

### Background Jobs
- Job args: `<Type>Args` implementing `river.JobArgs` with `Kind()` returning snake_case
- Must be idempotent — jobs may run more than once
- Use `domain.SystemActor` for audit records, include `"river_job_id"` in metadata

### htmx Partial Rendering (Admin Pages)
Every admin HTML page uses htmx for in-page navigation. The pattern:
- Split each page template into `<Name>Content` (content partial) and `<Name>` (wraps content in `@layouts.Admin`)
- GET handlers check `IsHTMX(r)` and render the `Content` variant for htmx requests, full page for normal requests
- POST handlers use `http.Redirect()` as-is — htmx follows redirects naturally
- `hx-boost="true"` on `<body>` auto-upgrades links/forms; sidebar stays, content area swaps

## Naming Conventions

- **Files:** `snake_case.go`, one domain area per file. No `utils.go` or `helpers.go`
- **Store methods:** `Get` (customer-scoped), `GetByID` (staff), `List`, `Create`, `Update<Field>`, `Delete`
- **Errors:** `Err<Description>` (e.g., `ErrOrderNotRefundable`)
- **Audit actions:** `Audit<Resource><Verb>` in `platform/audit/actions.go`
- **Permissions:** `Perm<Resource><Action>` in `platform/auth/permissions.go`
- **Status types:** typed strings with named constants, never bare strings

## Testing

Tests use **testcontainers** — Docker must be running. The test infrastructure:
- `testutil.SetupTestDB()` spins up a Postgres container and runs all migrations (used in `TestMain`)
- `testutil.NewTestTx(t, pool)` creates a transaction that rolls back on cleanup — each test gets perfect isolation without re-running migrations
- `testutil/fixtures.go` has factory functions for test data
- `testutil/assertions.go` has domain-specific assertion helpers

```bash
mage test                                    # all tests
go test ./internal/app/ -run TestOrderRefund # single test
mage testVerbose                             # verbose output
```

## Key Design Docs

Detailed specifications live in `docs/`:
- `CLAUDE-backend.md` — full backend conventions (the authoritative reference)
- `lean-commerce-package-structure.md` — directory tree and import graph
- `lean-commerce-domain-model.md` — 6 domains: Catalog, Pricing, Customer, Order, Fulfillment, Subscription
- `lean-commerce-auth.md` — session strategy, customer vs staff auth flows
- `lean-commerce-infrastructure.md` — 8 infrastructure concerns
- `lean-commerce-subscriptions.md` — subscription lifecycle and renewal
- `lean-commerce-b2b.md` — wholesale/B2B customer workflows
- `lean-commerce-discounts.md` — discount and coupon system
- `lean-commerce-tax.md` — tax calculation via Stripe Tax
- `lean-commerce-shipping.md` — shipping label and rate workflows
- `lean-commerce-ui-plan.md` — UI plan and component specs
- `rockabilly-brand-guide-v3.html` — full brand guide (colors, typography, tokens)
- `rockabilly-brand-voice.md` — voice and copy guidelines

## Design Context

### Users
- **Retail customers:** Coffee drinkers browsing and buying online — one-time purchases and subscriptions. They arrive from search, social, or word-of-mouth. Context is casual (phone on the couch, quick desktop order at work). Job: find good coffee, buy it fast, maybe subscribe.
- **Wholesale customers (B2B):** Cafes, restaurants, and offices placing bulk orders through a dedicated portal. Context is task-oriented — they know what they want and reorder regularly. Job: restock efficiently, manage account.
- **Staff (admin panel):** Small team managing catalog, orders, fulfillment, and subscriptions. The admin panel uses "Hiri" branding and a separate light-mode design — it is not customer-facing.

### Brand Personality
**Warm. Knowledgeable. Unpretentious.**

The rockabilly aesthetic is texture, not costume — it shows up in product names (Chop Top, Bike Blend), visual treatment (flame stripes, Edison-bulb glow), and sharp typographic choices, not in overwrought copy. Craft comes first, cool comes second. The interface should feel like walking into the shop: confident, specific, unhurried.

Emotional goals: trust, quiet confidence, warmth without gushing. Never fake urgency, never over-sell. See `docs/rockabilly-brand-voice.md` for the full voice guide.

### Aesthetic Direction
- **Theme:** Dark-mode storefront only (dusk charcoal surfaces, warm amber accents, neon-glow shadows). Admin stays light.
- **Typography:** Bebas Neue (display/hero, ALL CAPS), Boogaloo (product titles/prices), Playfair Display (pull quotes), Barlow (everything else — body, labels, UI).
- **Color semantics:** Red = primary action (buy). Teal = subscription/recurring. Amber = featured/highlight/warmth. Never swap these.
- **Textures:** Halftone dots, crosshatch grid overlays at low opacity. Edison-bulb radial glow on hero sections.
- **Shapes:** `rounded-sm` everywhere. Hard ink-drop shadows (letterpress feel). Flame-stripe gradient dividers.
- **Motion:** Subtle — card lift on hover (3px), amber underline slide on nav links, marquee ticker for promos, button press translateY. No gratuitous animation.
- **Anti-references:** Generic SaaS gradients, emoji-heavy copy, "artisanal journey" language, anything that could belong to any other coffee brand.

### Design Principles
1. **Substance over style** — Lead with the product (origin, roast, flavor). Visual personality supports the coffee, never competes with it.
2. **Specificity builds trust** — Use real names, real details, real delivery days. Never use filler copy or placeholder enthusiasm.
3. **Fast and focused** — Pages load fast, layouts are scannable, actions are obvious. One primary CTA per view. Don't make the customer think.
4. **Consistent visual language** — Color semantics, type roles, spacing, and component patterns stay locked. Every page should feel like the same shop.
5. **Accessible by default** — WCAG 2.1 AA compliance. Sufficient contrast on dark surfaces, keyboard-navigable, semantic HTML, screen-reader-friendly.
