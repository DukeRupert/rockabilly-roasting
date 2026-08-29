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
- **External services:** Stripe (payments + tax), EasyPost (shipping), Cloudflare R2 (S3-compatible; one bucket holds both product images and shipping labels) served through Cloudflare Image Transformations (`/cdn-cgi/image/`, not the Cloudflare Images product), Postmark (email via `mrz1836/postmark`)

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

# Delivery route optimization — see ops/osrm/README.md
mage geocodeWarm  # warm the delivery geocode cache (--dry-run via `go run ./cmd/geocode-warm`)
mage osrm:build   # build the OSRM routing dataset — RUN ON angmar.dev, never prod
mage osrm:push    # same, then scp the dataset to prod

# One-off commands (separate main packages under cmd/) — see docs/operations.md
go run ./cmd/support-reply --to person@example.com --name Alex --dry-run  # send a templated support email
go run ./cmd/seed        # same thing mage seed runs
go run ./cmd/sentrycheck # verify Sentry wiring
go run ./cmd/geocode-warm --dry-run  # preview the geocode working set (no API key needed)
go run ./cmd/migrate     # WooCommerce subscription importer — archived; --dry-run, --mapping flags
go run ./cmd/os-migrate  # Orderspace wholesale importer — archived

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

## Key Docs

Live docs in `docs/`:
- `CLAUDE-backend.md` — backend conventions (the authoritative reference) + a "Design rationale" appendix capturing non-obvious decisions
- `admin-ui.md` / `admin-detail-pages.md` — admin visual conventions (lint-enforced) and detail-page structure
- `operations.md` — releasing to production (tag `v*`; check `git log <last-tag>..origin/main` first — merging is not releasing), one-shot tools (`cmd/support-reply`, `cmd/seed`, `cmd/sentrycheck`), and the runbook index
- `backup-restore-runbook.md` — daily backup procedure and full restore steps
- `stripe-setup.md` — Stripe wiring (keys, webhooks, tax)
- `quickbooks-go-live.md` — connecting a real QuickBooks company and the test-mode proof period; wholesale billing ships **off** and is switched on in the admin, never by a deploy
- `guide/` — end-user how-tos for admin, storefront, and wholesale

The original `lean-commerce-*.md` design specs were retired post-launch — the code is the source of truth, with non-obvious "why" decisions extracted into the Design rationale section of `CLAUDE-backend.md`. Completed migration plans live under `docs/archive/migrations/`. Run `git log --all -- docs/<filename>` to recover any retired doc.

The brand is defined by the **Rockabilly Roasting Design System/** folder at the repo root (README, `colors_and_type.css`, `assets/`, `preview/`, `ui_kits/website/`, `SKILL.md`). That folder is the source of truth for color, type, shadows, spacing, iconography, and voice.

## Design Context

### Users
- **Retail customers:** Coffee drinkers browsing and buying online — one-time purchases and subscriptions. They arrive from search, social, or word-of-mouth. Context is casual (phone on the couch, quick desktop order at work). Job: find good coffee, buy it fast, maybe subscribe.
- **Wholesale customers (B2B):** Cafes, restaurants, and offices placing bulk orders through a dedicated portal. Context is task-oriented — they know what they want and reorder regularly. Job: restock efficiently, manage account.
- **Staff (admin panel):** Small team managing catalog, orders, fulfillment, and subscriptions. The admin panel shares the paper-and-ink palette but uses a quieter, information-dense treatment and still says "Hiri" internally — it is not customer-facing. Details in *Admin panel (Hiri)* below.

### Brand Personality
**Confident. Warm. Unpretentious.**

Rockabilly is texture, not costume. It shows up in product names, typographic choices, and the visual language (stamp shadows, bone paper, candle amber) — not in overwrought copy. Craft comes first, cool comes second. The voice is the guy behind the counter who remembers your order: plainspoken, a little swaggering, first-name energy. "We" for the shop, "you" for the customer.

**Lean into:** *roasted, small-batch, rebel, classic, fire, spark, kick, brew, ride, iron, grit, honest, straight-up, hand-packed, fresh, the shop, the crew, the roast, the grind.*
**Avoid:** *artisanal, curated, bespoke, journey, elevated, experience, unlock, ecosystem, synergize, disrupt.*

Emotional goals: trust, quiet confidence, warmth without gushing. Never fake urgency. Exclamation points earned (one per page, tops). Em-dashes liberally. **No emoji, ever** — use unicode stars/dingbats (`★ ✦ ✧ ❖ ◆`) or brand illustrations instead.

### Aesthetic Direction
- **Theme — paper and ink.** Storefront, marketing, *and admin* all run on warm bone paper (`--paper` `#F6EFE1`), tattoo black strokes (`--ink` `#0E0D0C`), and candle amber accents (`--amber` `#F2A03D`). Never pure `#fff` or `#000`. The admin uses the same palette with a deliberately quieter treatment — see *Admin panel (Hiri)* below.
- **Color semantics (locked):** `--rust` `#B4351D` = primary CTAs, links, and "open" signs. `--amber` `#F2A03D` = flame / highlight / "NEW · LIMITED · LIVE" callouts. `--ink` / `--paper` = text and surface. Never use neon gradients, pastels, purple, cyan, or "tech blue."
- **Typography (Google Fonts stand-ins — flag for print/licensed work):**
  - **Alfa Slab One** — display signage / billboards. ALL CAPS, tracking 0.04–0.08em.
  - **DM Serif Display** — heritage serif for editorial H1 moments. Never share a headline with the slab display.
  - **Oswald** — industrial condensed for H2/H3, UI, and body. The workhorse.
  - **Yellowtail** — brush script *garnish only*, one phrase per surface.
  - **Special Elite** — typewriter/carbon-paper feel for price tags, mono, captions.
  - Rules: never stack more than 2 families per composition (plus script as garnish). All-caps needs generous tracking. Body line-height 1.55–1.6.
- **Shapes — square by default.** `border-radius: 0` for most elements; `2–4px` on buttons/badges; `999px` only for chip tags. The shield/badge curve is reserved for the brand mark.
- **Borders & rules:** 2–3px solid ink strokes. No 1px hairlines on primary elements. Double rules (outer + inner, 4–6px gap) on menu panels and printed-feel collateral.
- **Shadows — stamp, not drop.** Hard offset `4px 4px 0 0 var(--ink)` is the signature. No soft blurred drops on primary surfaces (floating menus/modals excepted, at low intensity).
- **Textures:** Warm paper grain at low opacity (0.04–0.08), halftone dots, pinstripes, diner checkerboard — as accents, not wallpaper. Product photography warm-cast (amber/sepia), never cool/blue. The only acceptable gradient is an amber → rust radial glow behind a logo or centerpiece.
- **Motion — analog restraint.** The signature interaction is the **stamp**: hover lifts `translate(-1px,-1px)` and grows shadow to `5px 5px`; press collapses to `translate(2px,2px)` with shadow flattening to `0 0`. Fades 200ms linear. Never parallax, never neon pulses, never scroll-tied particles.
- **Layout:** Wide newspaper-style gutters, not edge-to-edge. Strong grid, but sticker/stamp elements may tilt `-2°` to `-6°` with hard shadows to feel pasted on. Tilts are intentional, never random.
- **Anti-references:** Generic SaaS gradients, glass-morphism, iOS soft drops, pastel UIs, emoji decoration, "artisanal journey" copy, purple/cyan tech palettes.

### Admin Panel (Hiri)
The admin is a working tool for staff, not a brand showcase. It shares the storefront's paper-and-ink palette but turns the ornamental dial *way* down.

- **Same palette, less load.** Uses the same `--color-rr-*` tokens (paper, ink, rust, amber, cream-hi `#FFFBF1` surfaces, paper-warm `#ECE0C6` raised rows). No textures, no halftones, no candle gradients, no tilts.
- **Fonts:** Oswald body, Alfa Slab One for page titles via `.admin-page-title`, Special Elite for mono/captions. **No DM Serif, no Yellowtail in admin** — those are storefront-only.
- **Density over drama.** Compact padding (buttons `0.5rem 0.75rem`, badges `0.2rem 0.6rem`, group labels `0.6rem` font-size). Information density wins over whitespace.
- **Shadows go soft.** Admin trades the storefront stamp shadow for hairline outlines + soft `0 1px 2px rgba(0,0,0,0.05)` drops on buttons. The hard `4px 4px 0` stamp is reserved for marketing surfaces — using it in a 500-row order list would be visual noise.
- **Status badges are pragmatic, not branded.** Admin breaks the locked rust/amber-only rule with a full semantic palette: `badge-green` (paid/active), `badge-amber` (pending), `badge-slate` (neutral status), `badge-teal` (info), `badge-red` (failure), `badge-grey` (archived), `badge-pastdue`, `badge-partial`, `badge-blue`, `badge-indigo`, `badge-neutral`. Staff need to scan dozens of rows — restricting them to two colors would hurt the job.
- **Row affordances.** Clickable table rows (`row-link`) get an inset 4px amber bar on hover; "stale/waiting" rows get a *persistent* inset rust bar — the paper-and-ink equivalent of a sticky-note flag. Honors `prefers-reduced-motion`.
- **Active nav.** Ink text + raised paper background; the active row is resolved from the page's `ActivePath` via `resolveActiveNav` (sub-pages light their parent). The sidebar is a flat seven-item list — Dashboard → Orders → Fulfillment → Catalog → Customers → Subscriptions → Discounts — with no group labels. Second-level pages (Categories/Attributes, Groups/Price Lists/Wholesale, Plans, and the retail/wholesale channels of Orders & Fulfillment) are reached through in-page **section tabs** / a **channel toggle** at the top of each parent (`section_nav.templ`), not their own sidebar row. Settings, Audit log, and Help live in the user menu. Mobile drawer and desktop rail share one `adminNav` component.
- **Motion is utility, not theatre.** Sidebar collapse, dropdown toggles, toast slide-in (`translateX(100%) → 0`, 200ms). No stamp lift-and-press in admin. The storefront delights; the admin gets out of the way.
- **Internal name.** The admin still says "Hiri" in nav/topbar — that's the platform codename and it's never customer-facing. Don't rename it without checking. Storefront and emails always say "Rockabilly Roasting Co."
- **Enforced.** `docs/admin-ui.md` is the authoritative reference — the allowed `rr-*` token list, the banned storefront classes (paper-and-ink colors, brand fonts, stamp shadows, heavy borders), and the rationale for each. `mage checkAdminUI` (wired into `mage check`) fails the build if any admin templ reaches for a banned class. **Read `docs/admin-ui.md` before adding new admin UI.** For the structure of a detail (`/admin/<thing>/{id}`) page specifically — main column vs rail, where each class of action belongs, activity timelines — read `docs/admin-detail-pages.md`.

### Design Principles
1. **Paper, not white. Ink, not black.** Every surface is warm; every stroke is weighted. Pure `#fff`/`#000` is a bug.
2. **Stamp the press.** Hard offset shadows and the lift-and-press interaction are load-bearing brand signals. Don't soften them into drop-shadow UIs.
3. **Substance over style.** Lead with the product — origin, roast, flavor, who roasted it. Rockabilly texture supports the coffee, never competes with it.
4. **Specificity builds trust.** Real names, real dates, real prices. No placeholder enthusiasm. One primary CTA per view.
5. **Accessible by default.** WCAG 2.1 AA on paper surfaces (ink-on-bone has ample contrast), keyboard-navigable, semantic HTML, screen-reader-friendly. Stamp interactions respect `prefers-reduced-motion`.
