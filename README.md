# Rockabilly Roasting Co.

Single-merchant ecommerce platform for [Rockabilly Roasting Co.](https://rockabillyroasting.com), a coffee roaster in Kennewick, WA. Built on Hiri (Go module `github.com/dukerupert/hiri`), deployed as a single binary on a Hetzner VPS. Live since 2026-04-24.

The platform handles three audiences: retail customers (storefront + subscriptions), wholesale customers (B2B portal), and staff (admin panel for catalog, orders, fulfillment, finance).

## Quickstart

Requires Go 1.25+, Docker (for Postgres), and [Mage](https://magefile.org/).

```bash
go install github.com/magefile/mage@latest

docker compose up -d        # Postgres 17 on localhost:5433
cp .env.example .env        # fill in API keys
mage db:migrate             # run migrations
mage seed                   # create admin user — needs SEED_EMAIL, SEED_PASSWORD, SEED_NAME in .env
mage dev                    # generate templ + Tailwind, build, run
```

Run `mage -l` for the full target list. Common ones: `mage build`, `mage test`, `mage check` (lint + test, the CI gate), `mage watch` (Tailwind in watch mode).

## Architecture at a glance

A single Go binary running both the HTTP server and River background job workers. Strict layered packages with inward-only dependencies:

```
domain/   ← pure types, enums, no logic
store/    ← all SQL, takes pgx.Tx
app/      ← business logic, validation, state machines
web/      ← thin HTTP handlers + middleware
ui/       ← templ templates (storefront, admin, components)
jobs/     ← River workers (one file per job type)
platform/ ← infrastructure (auth, audit, email, payments, shipping, …)
```

See [`docs/CLAUDE-backend.md`](docs/CLAUDE-backend.md) for full backend conventions.

## Repo layout

- `cmd/server/` — main binary (HTTP + River workers in one process)
- `cmd/support-reply/`, `cmd/seed/`, `cmd/sentrycheck/`, `cmd/migrate/`, `cmd/os-migrate/` — one-shot tools, see [`docs/operations.md`](docs/operations.md)
- `internal/` — application code (layered as above)
- `db/migrations/` — Goose SQL migrations
- `magefiles/` — Mage build targets
- `ops/` — backup systemd units (`rr-backup.service` / `.timer`)
- `Rockabilly Roasting Design System/` — brand source of truth (colors, type, voice, components)

## Documentation

| Doc | What it covers |
|-----|----------------|
| [`CLAUDE.md`](CLAUDE.md) | Quick orientation — also read by AI assistants |
| [`docs/CLAUDE-backend.md`](docs/CLAUDE-backend.md) | Authoritative backend conventions + Design rationale appendix (non-obvious decisions) |
| [`docs/operations.md`](docs/operations.md) | One-shot tools (support replies, seeding, etc.) and runbook index |
| [`docs/backup-restore-runbook.md`](docs/backup-restore-runbook.md) | Daily backups + full restore procedure |
| [`docs/stripe-setup.md`](docs/stripe-setup.md) | Stripe wiring (keys, webhooks, tax) |
| [`docs/guide/`](docs/guide/) | End-user how-tos (admin, storefront, wholesale) |
| [`docs/archive/`](docs/archive/) | Completed migrations and pre-launch artifacts |
| [`Rockabilly Roasting Design System/`](Rockabilly%20Roasting%20Design%20System/) | Brand: paper-and-ink palette, typography, voice |

## Tech stack

Go 1.25 · PostgreSQL 17 (`pgx/v5`) · `templ` for HTML · htmx + Alpine.js for storefront/admin interactivity · Svelte 5 for the checkout component · Tailwind CSS v4 · River for jobs · Goose for migrations · Stripe (payments + tax) · EasyPost (shipping) · Cloudflare Images + R2 · Postmark (email) · Prometheus + Grafana · Sentry.
