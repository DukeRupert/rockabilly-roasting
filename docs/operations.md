# Operations

One-shot commands and operational procedures. Each tool lives under `cmd/` and is run with `go run ./cmd/<name>`. Most read configuration from `.env` (loaded via `godotenv`).

---

## Active tools

### `cmd/support-reply` — send a templated support email

Replies to common support tickets from a saved email template via Postmark, without going through the admin UI. Useful for recurring scenarios like "I can't log in" where the answer is the same every time.

**Usage:**

```bash
# Preview only (no send)
go run ./cmd/support-reply --to person@example.com --name Alex --dry-run

# Send for real (requires POSTMARK_SERVER_TOKEN in .env)
go run ./cmd/support-reply --to person@example.com --name Alex

# Override From / subject / template
go run ./cmd/support-reply \
  --to person@example.com \
  --name Alex \
  --subject "Re: Can't login to my account" \
  --from "Logan <logan@rockabillyroasting.com>" \
  --template account_not_migrated
```

**Flags:**

| Flag | Default | Notes |
|------|---------|-------|
| `--to` | _(required)_ | Recipient email |
| `--name` | _(empty)_ | Recipient first name — used in greeting |
| `--template` | `account_not_migrated` | Template name (matches files in `internal/emailtemplates/`) |
| `--subject` | `New website, same shop` | Subject line |
| `--from` | `Rockabilly Roasting Co. <support@rockabillyroasting.com>` | From address |
| `--bcc` | `info@rockabillyroasting.com` | Comma-separated. Pass `--bcc ""` to disable |
| `--dry-run` | `false` | Render and print; do not send |

**Available templates:**

- `account_not_migrated` — for customers whose old (inactive) account didn't carry over from WooCommerce. Explains it's a tech change (new website), not a physical move; instructs them to place an order to auto-create an account; offers to dig up old order history on request.

**Adding a new template:**

1. Create `internal/emailtemplates/html/<name>.html` and `internal/emailtemplates/text/<name>.txt`. Use `magic_link.*` as the paper-and-ink reference.
2. Add a `<Name>Data` struct in `internal/emailtemplates/renderer.go`.
3. Add a case to the `buildTemplateData` switch in `cmd/support-reply/main.go`.

---

### `cmd/seed` — create initial admin staff user

Reads `SEED_EMAIL`, `SEED_PASSWORD`, `SEED_NAME` from environment and creates an admin-role staff user. Run once per environment.

```bash
mage seed   # equivalent to `go run ./cmd/seed`
```

---

### `cmd/sentrycheck` — verify Sentry wiring

Sends a test exception to Sentry. Run after rotating the Sentry DSN or when investigating missing events.

```bash
SENTRY_DSN=... go run ./cmd/sentrycheck
```

---

## Operational runbooks

| Runbook | Covers |
|---------|--------|
| [`backup-restore-runbook.md`](backup-restore-runbook.md) | Daily `pg_dump` to Cloudflare R2 (live since 2026-04-24), full restore procedure, verification steps |
| [`orderspace-migration-runbook.md`](orderspace-migration-runbook.md) | Batched wholesale migration: `cmd/os-report` census, `cmd/os-migrate` importer, `cmd/os-welcome` invites, per-batch procedure, rehearsing on a prod copy, SKU map, verification queries |
| [`stripe-setup.md`](stripe-setup.md) | Stripe API keys, webhook endpoints, Stripe Tax configuration |
| [`order-reminders.md`](order-reminders.md) | Weekly wholesale order reminder: eligibility rules, schedule config, admin preview + one-off notice, decommissioning the standalone `rr` service |

The backup is driven by `ops/rr-backup.timer` + `ops/rr-backup.service` on the Hetzner VPS. Configuration is in `ops/backup.env.example`.

---

## Wholesale migration tools (active)

The OrderSpace → Hiri wholesale migration is ongoing, batched. Full procedure in
[`orderspace-migration-runbook.md`](orderspace-migration-runbook.md).

- `cmd/os-report` — read-only census of the OrderSpace tenant.
- `cmd/os-migrate` — importer (`--only`, `--dry-run`, `--customers-only`). Assigns Wholesale 2026 + NET 7.
- `cmd/os-welcome` — sends migration welcome emails (`--emails`, `--send`). Dry-runs by default.

## Archived migration tools

Ran during the WooCommerce → Hiri retail cutover (2026-04-24). Kept for reference, not part of regular operations.

- `cmd/migrate` — WooCommerce subscription importer. Mage target `mage wcMigrate` supports `--dry-run` and `--mapping=path/to/mapping.json`.

Run notes and decisions for those migrations live in [`archive/migrations/`](archive/migrations/).
