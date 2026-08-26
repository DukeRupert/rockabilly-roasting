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

### `cmd/geocode-warm` — warm the delivery address geocode cache

Geocodes every address that has appeared on a local-delivery order, so the first route plan reads from cache instead of firing a burst of billable Google lookups. The second thing it does matters more: it reports the addresses Google could not pin precisely, which are the ones that would send a driver to the wrong building. Safe to re-run — cached addresses cost nothing.

```bash
go run ./cmd/geocode-warm --dry-run    # show the working set and projected lookups; no API key needed
mage geocodeWarm                       # warm the cache for real
go run ./cmd/geocode-warm --limit 50   # cap the working set
```

Needs `DATABASE_URL` and `GOOGLE_GEOCODING_API_KEY` (except `--dry-run`). Exits non-zero if any address failed to geocode — those are stops that cannot be routed until a human fixes the address.

---

## Releasing to production

Production deploys on a pushed tag matching `v*`. `.github/workflows/deploy-prod.yml` builds the image, pushes it to DockerHub as both `:<tag>` and `:latest`, and runs `docker compose pull && up -d` on the VPS over SSH.

That workflow also declares `workflow_dispatch`, so a manual run from the Actions tab is a second path to production — and not a passive one. It builds the ref you dispatch (`main` unless you pick another), pushes it as `:latest`, and deploys it. That is an unannotated release of trunk, not a restart of the current one, and it overwrites the `:latest` that had been pointing at the last release. `deploy-dev.yml` only ever pushes `:dev-<sha>`, so this workflow is the only writer of `:latest`. Use a tag unless you are deliberately deploying something that is not a release.

### 1. Check what you are about to ship

**Merging is not releasing.** A PR merged to `main` sits there until someone cuts a tag, and more than one merge can accumulate between tags.

```bash
git fetch --tags origin
LAST=$(git tag -l --sort=-v:refname 'v*' | head -1)   # -l is load-bearing: without it 'v*' is read as a
                                                      # tag to create, LAST comes back empty, and both
                                                      # commands below silently report an empty release
git log --oneline "$LAST"..origin/main                          # every commit in this release
git diff --name-only --diff-filter=A "$LAST"..origin/main -- db/migrations/   # migrations that will run
```

This is not a formality. `v1.111.0` was cut believing it carried one PR's route fix; it carried two PRs and migration 072, because #11 had been merged an hour earlier and never tagged. Read the commit list before writing the tag message — the tag message should describe the release, not the last PR.

### 2. Cut the tag

Annotated, with a subject line and a prose body describing the release; `git show v1.111.0` is the shape. Run this line on its own — with no `-m` it opens an editor, so pasting it as part of a block feeds the following lines to the editor as keystrokes:

```bash
git tag -a v<x.y.z> origin/main
```

Tagging `origin/main` explicitly is deliberate: it is the ref step 1 inspected, so the tag cannot land on a stale local `main` or on whatever branch you happen to be standing on. It needs no checkout and is unaffected by uncommitted work.

### 3. Push it and watch the deploy

```bash
TAG=v<x.y.z>
git push origin "$TAG"

# Wait for the run belonging to THIS tag. `gh run list --limit 1` straight after
# the push often still returns the previous release — watching that one reports a
# stale green for a deploy that has not started.
RUN=""
for _ in $(seq 24); do
  RUN=$(gh run list -w "Deploy Prod" -b "$TAG" --limit 1 --json databaseId --jq '.[0].databaseId')
  [ -n "$RUN" ] && break
  sleep 5
done
if [ -n "$RUN" ]; then
  gh run watch "$RUN" --exit-status
else
  echo "no Deploy Prod run for $TAG after 2min — check the Actions tab"
fi
```

### 4. Confirm it is actually live

```bash
curl -s https://rockabillyroasting.com/version
# {"version":"v1.111.0","commit":"7c0bcb5...","go":"go1.25.14"}
```

**A green workflow run is not this check.** The SSH step ends in `docker compose up -d` with no `--wait`, so it returns as soon as the containers are *started*. A container that then dies in `entrypoint.sh` — a failed migration, say — does so after the run has already gone green. Nor is a 200 from the storefront enough: a new container that fails can leave the previous one serving.

`/version` settles it because `entrypoint.sh` runs `goose ... up` and only then `exec ./server`, under `set -e`. A server answering with your commit is therefore proof that your migrations ran.

Version by what the release contains, not by how much work it was: new user-visible capability is a minor bump, a fix to shipped behaviour is a patch.

---

## Operational runbooks

| Runbook | Covers |
|---------|--------|
| [`../ops/osrm/README.md`](../ops/osrm/README.md) | OSRM routing dataset: build on angmar, serve on prod, quarterly refresh, version pinning, why port 5000 is never published |
| [`backup-restore-runbook.md`](backup-restore-runbook.md) | Daily `pg_dump` to Cloudflare R2 (live since 2026-04-24), full restore procedure, verification steps |
| [`orderspace-migration-runbook.md`](orderspace-migration-runbook.md) | Batched wholesale migration: `cmd/os-report` census, `cmd/os-migrate` importer, `cmd/os-welcome` invites, per-batch procedure, rehearsing on a prod copy, SKU map, verification queries |
| [`stripe-setup.md`](stripe-setup.md) | Stripe API keys, webhook endpoints, Stripe Tax configuration |
| [`order-reminders.md`](order-reminders.md) | Weekly wholesale order reminder: eligibility rules, schedule config, admin preview (the one-off notice composer moved to Announcements), decommissioning the standalone `rr` service |

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
