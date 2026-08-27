# Hiri — SaaS Readiness Assessment

Worked against `saas-launch-checklist.md`. Codebase audited 2026-08-22 against `main` (137d24a).

**Legend:** `[x]` verified in code · `[~]` partial / exists but unverified · `[ ]` not built · `[-]` deliberately skipped
**Owner:** `CP` control plane · `IN` Hiri instance · `OPS` fleet operations

---

## Decision: one instance per merchant

Hiri is a single-tenant application and **stays that way**. Each merchant gets their own
container and their own Postgres database. There is no `tenant_id`, no row-level
security, no pooled data.

**Database per merchant, not server per merchant.** The databases live in one shared
Postgres cluster — `CREATE DATABASE acme;` per merchant, one postmaster, one
`shared_buffers`, one host to patch and back up. A connection to `acme` cannot see
another database's tables at all; cross-database access requires `dblink` or
`postgres_fdw`, which we do not install. That is stronger isolation than RLS for less
work, and the instance sees nothing but a different `dbname` in `DATABASE_URL`.

**Why.** The retrofit was viable — `store.Tx` (`internal/store/db.go:34`) has 424 callers
and only 3 bypasses, so a single `SET LOCAL app.tenant_id` chokepoint plus RLS policies
would have worked, and the 301 sqlc queries are mechanically auditable. But physical
isolation is strictly stronger than any predicate we could enforce, it costs a container
and a database per merchant (noise against any plausible price), and it leaves the 28,864
lines in `app/` untouched. At 50 customers the fleet is small enough to manage and the
blast radius of a tenancy bug is zero rather than existential.

**Revised from the first pass of this document:** that draft recommended adding the
`tenant_id` seam now and deferring only the pooled *deployment*. Don't. Columns and RLS
policies that nothing exercises will rot silently and give false confidence. Physical
isolation is the stronger guarantee; build that and nothing else.

**Keeping the escape hatch cheap.** If pooled tenancy ever becomes worth it, the path is
RLS through the `store.Tx` chokepoint. That stays cheap only while the chokepoint holds.
Today there are exactly three `pool.Begin` calls outside it — two in
`platform/quickbooks/client.go`, one in `testutil/db.go`. **Add a lint that fails the
build on a fourth.** That single guard preserves the option indefinitely.

**We are not building RLS.** With one database per merchant it would buy nothing — a
single-tenant database needs a policy matching every row, which is the same as no policy.
The isolation control that actually does the work is role-per-database (§2). Appendix A
holds the RLS notes for the hypothetical future only.

**What this model actually costs** — named up front so none of it is a surprise:

| Cost | Detail |
|---|---|
| Fleet upgrades | One deploy becomes N. `entrypoint.sh` self-migrates on boot, but a migration that fails on instance 37 gives you a split fleet with no current way to see it. |
| Fleet backups | `ops/backup/rr-backup.sh` backs up **one** database. A shared cluster keeps this tractable — N `pg_dump`s from one host, and restoring a single merchant is a single-database restore — but the script, its alerting, and the runbook all still assume one. P1. |
| Cross-merchant analytics | "Orders processed platform-wide this month" becomes a fan-out, not a query. |
| Churn debris | Instances, databases, DNS records, and Postmark servers to reap. |
| Connection ceiling | **The real density limit.** No `MaxConns` is set, so pgxpool defaults to `max(4, NumCPU)`, plus River (`MaxWorkers: 10`, sharing the pool) and its listener — call it 8–10 backends per instance. Fifty merchants is 400–500 Postgres processes against a default `max_connections` of 100. Note this is per *container*, not per database: pooling tables under RLS would not have helped. |
| Per-merchant floor | A container plus a database in the shared cluster — mostly the container's RAM. |

## Architecture: three pieces

**Control plane (`CP`) — new, small, separate.** Owns merchant signup, tier selection,
Stripe Billing for *your* revenue, the tenant registry, provisioning, and routing.
Roughly 2k LOC. Hiri instances never learn they are part of a fleet: no plan concept, no
trial state, no suspension logic inside the app. Your platform billing also never
entangles with the merchant's own Stripe integration — two Stripe integrations, two
codebases, no shared webhook router.

**Hiri instance (`IN`) — what exists today.** A container plus a `DATABASE_URL`.
`entrypoint.sh` runs `goose up` and starts the server, so a fresh instance self-migrates
on boot with no orchestration.

**Fleet operations (`OPS`).** Provisioning, deprovisioning, rolling deploys, fleet-wide
backup, schema-version visibility.

## Onboarding sequence

Sign up → pick tier → pay → instance provisioned (under a minute, in the background) →
guided setup wizard → connect Stripe → add first product → go live.

Provisioning is a script: create database, start container, create the Postmark server,
create the mail subdomain DNS records, point `{slug}.hiri.app` at it via Caddy on-demand
TLS. The merchant never learns which deployment model you chose.

The `main.go` environment surface (50 vars) splits cleanly:

- **Platform-owned, identical everywhere** (~15) — Sentry, R2, OSRM, Google geocoding,
  Turnstile, your QuickBooks app registration, Broadwave. Template once.
- **Generated at provision time** (~8) — `DATABASE_URL`, `BASE_URL`, `ORDER_ACTION_SECRET`,
  `UNSUBSCRIBE_SECRET`, `QB_TOKEN_ENCRYPTION_KEY`, `STORE_NAME`, `MERCHANT_TIMEZONE`.
- **Only the merchant can supply** (~8) — Stripe key + webhook secret, Shippo key +
  webhook secret, QuickBooks OAuth connect, tax nexus, shipping origin. **This is the
  wizard, and it is the real onboarding cost in any architecture.**

## Email and deliverability

Merchants send from an auto-provisioned subdomain of a domain we own; custom domains are
a paid upgrade.

Do **not** pool every merchant onto one address. `platform/email/provider.go` already
notes that spam-button presses cost "sending reputation on the same domain as order
confirmations and invoices" — pooled across 50 merchants, one bad list poisons everyone's
transactional mail.

- **Subdomain per merchant** — `acme.hiri-mail.app`. We control the zone, so provisioning
  writes DKIM / SPF / Return-Path via the Cloudflare API. Zero merchant DNS.
- **Dedicated sending domain, separate from the corporate domain** — a merchant incident
  must not take down our own sales and support email.
- **Subdomain isolation is partial.** Mailbox providers inherit some signal from the
  organizational domain. Better than pooled and free, but not a reason to stop caring who
  signs up.
- **One Postmark Server per merchant** — falls out of per-instance naturally
  (`POSTMARK_SERVER_TOKEN` is already per-instance). Per-merchant stats, and a suspension
  hits one merchant instead of the platform.
- **Hard rule, and it is the tier boundary:** never let a merchant set `From:` to a domain
  we have not verified. Auto-provisioned subdomain, or the full custom-domain flow.
  Nothing in between — an unaligned `From` fails DMARC and looks like our bug.

---

## 1. Identity and Access

| Pri | Item | Owner | Status | Evidence / what's needed |
|---|---|---|---|---|
| P1 | Merchant signup with email verification | CP | `[ ]` | New. The customer-side machinery (`app/auth_email.go:90`, `jobs/email_verify_send.go`) is a reference, not a reuse — different actor, different app. |
| P1 | Merchant password reset | CP | `[ ]` | New, same reasoning. |
| P1 | Session expiry and logout | CP + IN | `[~]` | Instance side real: `expires_at`, `PruneExpired`, audited `Logout`. Control plane new. |
| P1 | Magic link or OAuth login | IN | `[x]` | `CreateMagicLinkToken` / `RedeemMagicLink` (`auth.go:480,502`). Worth mirroring in CP. |
| P1 | Owner / member roles | IN | `[x]` | Five staff roles, enforced in middleware. Ahead of the bar. |
| P1 | First admin creatable without SSH | IN | `[ ]` | Today the first staff user comes from `mage seed`. Provisioning must seed the owner from the CP signup record. |
| P2 | Invite teammates | IN | `[x]` | Staff invites (migration 058), wholesale `customer_users` (063). |
| P3 | SSO / SAML | — | `[-]` | Skip at 50. |

## 2. Isolation and Data Safety

Per-instance changes what these items *mean*. Query scoping is satisfied by construction;
the work moves to the fleet.

| Pri | Item | Owner | Status | Evidence / what's needed |
|---|---|---|---|---|
| P1 | Tenant data isolation | IN | `[x]` | Separate database per merchant. Stronger than any query predicate. |
| P1 | Role per database, `CONNECT` revoked | OPS | `[ ]` | **The control that replaces RLS.** Separate databases isolate nothing if every instance connects as the same superuser — Postgres has no cross-database `SELECT`, but it does have cross-database *connections*. Provisioning must `CREATE ROLE acme_app LOGIN`, `REVOKE CONNECT ON DATABASE acme FROM PUBLIC`, `GRANT CONNECT ON DATABASE acme TO acme_app`, so each `DATABASE_URL` carries only its own role. `entrypoint.sh` runs `goose up` on boot, so that role needs DDL on its own schema — simplest is to let it own the database, which is safe here precisely because there is no RLS for an owner to bypass. |
| P1 | Test proving tenant A can't read tenant B | OPS | `[-]` | Not applicable — no shared connection exists. Replace with a provisioning test asserting a new instance gets a distinct `DATABASE_URL` and empty schema. |
| P1 | Guard the `store.Tx` chokepoint | IN | `[ ]` | Lint failing on `pool.Begin` outside `store/db.go` + `testutil`. Preserves the pooled-tenancy escape hatch. |
| P1 | Nightly offsite backups — **fleet-wide** | OPS | `[~]` | Single-instance backup is solid: `ops/backup/rr-backup.{sh,service,timer}` → R2, 30d daily / 365d monthly, `pg_dump -Fc`, Postmark alert on failure. **It backs up one database.** Must become fleet-wide — N dumps from the shared cluster — with per-instance alerting so a single merchant's failed dump is visible. |
| P1 | Restore rehearsed, documented | OPS | `[~]` | `docs/backup-restore-runbook.md` documents procedure. Never rehearsed. Rehearse restoring *one merchant* without touching others; stamp the date. |
| P1 | Reversible migrations | IN | `[x]` | All 71 migrations carry a `-- +goose Down`. |
| P1 | Schema version visible per instance | OPS | `[ ]` | A failed migration on one instance is currently invisible. Needed before ~20 merchants. |
| P2 | Data export for customers | IN | `[ ]` | Nothing today. Per-instance makes the merchant-level version trivial — it's a `pg_dump`. |
| P2 | Account / data deletion path | IN + OPS | `[~]` | `DeleteCustomer` / `DeleteCustomerUser` exist in `store/`; no self-serve path, no retention policy. Merchant-level deletion = deprovisioning, which is also unbuilt. |

## 3. Billing

Two unrelated systems. The instance bills the merchant's customers (mature). The control
plane bills the merchant (does not exist).

| Pri | Item | Owner | Status | Evidence / what's needed |
|---|---|---|---|---|
| P1 | Platform subscription billing | CP | `[ ]` | Stripe Billing, tiers, checkout. All new. |
| P1 | Webhooks handled idempotently | CP | `[ ]` | New — but copy the instance pattern verbatim: `webhook_events` with `UNIQUE (provider, event_id)` (migration 009), `app/webhooks.go` returns nil on duplicate. Textbook already. |
| P1 | Merchant portal: card, cancel, invoices | CP | `[ ]` | Stripe Customer Portal covers this on day one. |
| P1 | Trial with clear end behavior | CP | `[ ]` | Define what expiry does: instance stops routing, data retained N days, then reaped. |
| P1 | Failed payment dunning | CP | `[ ]` | New. Instance-side dunning is strong (`subscription_dunning_test.go`, `jobs/email_subscription_past_due.go`) — reference it. |
| P2 | Grace period / suspend on non-payment | CP | `[ ]` | Suspension is a CP routing decision. **The instance must not learn about it** — no plan state in Hiri. |
| P2 | Receipts emailed automatically | CP | `[ ]` | Stripe handles this. |
| P3 | Usage / metered billing | — | `[-]` | Skip. |

## 4. Operations and Observability

| Pri | Item | Owner | Status | Evidence / what's needed |
|---|---|---|---|---|
| P1 | **Tests enforced in CI** | OPS | `[ ]` | Neither `deploy-dev.yml` nor `deploy-prod.yml` runs tests — both go straight to build-and-ship. `mage check` exists and is never gated. 592 tests across 111 files, unenforced. Cheapest high-value fix on this page. |
| P1 | Error tracking alerting a phone | OPS | `[~]` | Sentry wired (`platform/sentry`, `cmd/sentrycheck`). Verify the rule actually pages. Tag events with merchant slug. |
| P1 | External uptime monitor | OPS | `[ ]` | `GET /health` exists (`router.go:117`). Nothing outside the VPS polls it. Per-instance checks once the fleet exists. |
| P1 | Structured logs with request + merchant ID | IN | `[~]` | `requestIDMiddleware` (`web/middleware.go:27`) and `logging.FieldRequestID`, tagged onto the Sentry scope. Add merchant slug as a static field at startup — trivial per-instance. |
| P1 | Deploy < 10 min with known rollback | OPS | `[~]` | Tag `v*` → build → `docker compose pull && up -d`. Fast for one host. **Rollback undocumented and untested.** Rolling fleet deploy unbuilt. |
| P1 | Secrets out of repo, rotated once | OPS | `[~]` | GH Actions secrets, `backup.env` 0600. **The publicly-leaked `bw_1ae…` Broadwave key is still unrotated** — this row is open on that alone. Per-instance secret storage also needs a plan. |
| P1 | Set `MaxConns` explicitly | IN | `[ ]` | Nothing sets it today; the CPU-derived pgxpool default is wrong for a dense fleet. Pin it low (~4) so each merchant's footprint is predictable, and size `max_connections` on the shared cluster against the fleet target. |
| P1 | Provisioning script | OPS | `[ ]` | DB, container, DNS, TLS, Postmark server, owner seed. The core new artifact. |
| P1 | Deprovisioning script | OPS | `[ ]` | Reap DB, container, DNS, Postmark server. Needed before the first churn, not after. |
| P2 | Status page | CP | `[ ]` | None. |
| P2 | Rate limiting | IN | `[~]` | Broad coverage in `router.go`. **But `docs/security/rate-limiting-TODO.md` records a confirmed unfixed prod defect: every request resolves to the same client IP, so all per-IP limits share one global bucket.** Effectively decorative. Fix before merchants depend on it. |
| P2 | Feature flags | IN | `[~]` | Ad-hoc booleans on the `store_settings` singleton. Per-instance makes per-merchant enablement free — flip a var. Good enough at 50. |
| P2 | Dashboards: error rate, latency, queue depth | OPS | `[~]` | Prometheus + `docs/grafana-dashboard.json`. Needs a per-instance label and a fleet rollup view. |
| P2 | Job retries + dead-letter visibility | IN | `[~]` | River retries for free; no admin surface for discarded jobs. A failed job is invisible today. |
| P3 | Horizontal scaling | — | `[-]` | Skip. Single binary per merchant is correct. |

## 5. Customer-Facing Essentials

| Pri | Item | Owner | Status | Evidence / what's needed |
|---|---|---|---|---|
| P1 | Lifecycle emails | IN | `[x]` | ~20 templated jobs in `internal/jobs/`. Strong. **Known gap: shipped-email stays silent for orders shipped without a label.** |
| P1 | Add `ReplyTo` to `email.Message` | IN | `[ ]` | `platform/email/provider.go` has `From`, `To`, `Bcc`, `Headers` — no `ReplyTo`. Needed so replies reach the merchant, not us. Small, and it closes most of the free-tier branding gap. |
| P1 | Setup wizard reaching first sale | IN | `[ ]` | Stripe connect, email sender, shipping origin, tax, first product. The single largest new build inside the instance. |
| P1 | Obvious support channel | CP | `[~]` | `platform/help` + `cmd/support-reply` serve the merchant's customers. Merchants need their own channel to us. |
| P1 | Empty states designed | IN | `[~]` | Matters much more now — every new merchant starts empty. Not audited. |
| P1 | Admin read-only view of any account | IN + CP | `[ ]` | No impersonation anywhere. Debugging a support email means SQL today. Per-instance means we also need a documented, audited way *in*. |
| P2 | In-app changelog | CP | `[ ]` | Announcements (migration 071) is merchant→customer. Right machinery, wrong direction. |
| P2 | Help docs for top 5 tasks | IN | `[x]` | `docs/guide/{admin,storefront,wholesale}` — 11 admin docs. Ahead of the bar. |
| P3 | Public API | — | `[-]` | Skip. |
| P3 | White-labeling | IN | `[x]` | Already exists (migration 052, `app/whitelabel.go`) as a merchant feature. |

## 6. Legal and Trust

| Pri | Item | Owner | Status | Evidence / what's needed |
|---|---|---|---|---|
| P1 | Terms of service — **merchant-facing** | CP | `[ ]` | `ui/storefront/legal.templ` is Rockabilly's terms with *its* customers. A different document. |
| P1 | Privacy policy + DPA | CP | `[ ]` | `ui/storefront/privacy.templ`, same problem. Once we hold other merchants' customer data we are a processor: sub-processor list (Stripe, Postmark, Shippo, Cloudflare, Hetzner, Sentry) and a DPA. Per-instance makes the isolation story easy to state truthfully. |
| P1 | HTTPS everywhere, HSTS on | OPS | `[~]` | Per `docs/security/csp-audit.md`, headers live in the VPS Caddyfile. **That file is not in this repo** — unversioned and not restorable from a clone. Vendor it into `ops/`; it becomes the fleet template. |
| P2 | CSRF, CSP, dependency scan | IN + OPS | `[ ]` | **CSRF:** no middleware; defense is `SameSite=Strict` on session cookies (`web/auth.go:168`, `customer_auth.go:308`), `Lax` on cart/OAuth. Defensible, undocumented — write it down or add middleware. **CSP:** report-only draft, never deployed; three blockers documented (Alpine `unsafe-eval`, ~48 inline handlers, heavy inline styles). **Dependency scan:** none — no Dependabot, no CodeQL, no `govulncheck`. |
| P2 | Cookie / consent notice | IN | `[ ]` | Fine US-only; not fine the first time a merchant sells into the EU. |

## 7. Launch Mechanics

All process, all open. One asset already exists: the Orderspace→Hiri migration ran as
three batches of 8/31/12 with a delta sweep between waves
(`docs/orderspace-migration-runbook.md`). That is a cohort playbook — reuse its shape.

---

## Go / No-Go

| Gate | Status |
|---|---|
| All P1 closed | **No** — control plane unbuilt, fleet ops unbuilt, 6 instance-side P1s open |
| All P2 closed | **No** |
| Last restore rehearsal | *never* |
| Last deploy + rollback test | *rollback never tested* |

---

## Order of work

**Tier 0 — cheap, valuable regardless, do now.** None of this depends on the SaaS decision.

1. Gate both workflows on `mage check`.
2. Rotate the leaked Broadwave key (open since v1.91.0).
3. Fix client-IP resolution — rate limiting is currently decorative in prod.
4. External uptime monitor on `/health`; confirm Sentry pages a phone.
5. Rehearse a restore; stamp the date in the runbook.
6. Test a rollback once; write down the command.
7. Vendor the production Caddyfile into `ops/`.
8. Add `govulncheck` + Dependabot.
9. Lint against `pool.Begin` outside the chokepoint.

**Tier 1 — make one instance sellable.** Still single-merchant work; every item ships value
to Rockabilly immediately.

10. Setup wizard (Stripe connect, sender, shipping origin, tax, first product).
11. `ReplyTo` on `email.Message`.
12. Admin read-only "view as customer," audited.
13. Dead-letter / failed-job visibility.
14. Merchant slug as a static log + Sentry field.
15. Empty-state pass across admin and storefront.
16. Write down the CSRF posture.

**Tier 2 — the fleet.** Nothing here touches `app/`.

17. Pin `MaxConns`; size `max_connections` on the shared cluster for the fleet target.
18. Provisioning script, end to end: `CREATE DATABASE`, `CREATE ROLE` + `REVOKE CONNECT`,
    container, DNS, TLS, Postmark server, owner seed, mail subdomain.
19. Deprovisioning script — drop database and role, container, DNS, Postmark server.
20. Fleet-wide backup + single-merchant restore, rehearsed.
21. Schema-version-per-instance visibility.
22. Rolling deploy with a failure path.

**Tier 3 — the control plane.** Signup, tiers, Stripe Billing, dunning, tenant registry,
routing and suspension, merchant ToS/privacy/DPA, status page.

---

## Appendix A — RLS notes (not part of the plan)

Kept only for the hypothetical future in which pooled tenancy becomes worth it. Nothing
here is a build item; see the Decision section for why.

Three things fail in ways that look fine at the time:

- **Table owners bypass RLS.** The role that ran the migrations owns the tables; if the
  app connects as that role, every policy is inert and everything looks correct. Requires
  `ALTER TABLE … FORCE ROW LEVEL SECURITY` *and* a non-owner application role. Superusers
  and any role with `BYPASSRLS` ignore policies unconditionally. Note this directly
  conflicts with letting the app role own its database — fine today, a thing to undo then.
- **A new table without a policy is open, not denied.** Needs a CI test asserting every
  tenant-scoped table has RLS enabled, or some future migration quietly punches a hole.
- **Prefer role-per-tenant over a `SET LOCAL` GUC.** Policies keyed on `current_user`
  cannot be forgotten by a path that skips the chokepoint, and a compromised instance
  cannot reassign itself. Transaction-mode connection poolers also leak session-level
  `SET` across clients; `SET LOCAL` inside a transaction is the only safe form.

**A different axis, also not planned.** RLS could enforce *customer* isolation within a
single merchant, which Hiri currently enforces by convention — the required `customerID`
parameter (`Get(ctx, tx, id, customerID)` vs `GetByID`). Making that a database guarantee
would mean a per-request `SET LOCAL app.customer_id` on every path. Recorded because it
is the one version of RLS that would add something here; not recommended, since the
existing seam is enforced by function signature and the change touches every request.
