# Cutover Handoff — Rockabilly Roasting WC → Hiri

**Cutover tonight: 2026-04-23. Go-live: 2026-04-24.** Pulled forward from the original 2026-04-29 plan.

This document is the single source of truth for the migration. Read it before doing anything.

---

## TL;DR current state

- ✅ Hiri dev/prod deployment is running on Hetzner VPS (5.161.245.139), containers `rr-app` + `rr-postgres`
- ✅ 6 subscription plans created, 10% discount, active, naming normalized
- ✅ 8 products + 48 variants created (Cloud 9, 2-Stroke, Bike Blend, Chop Top, Cascadia, Ethiopia, Guatemala, Rev It Up)
- ✅ `wc-variant-mapping.json` — 21 WC variation IDs mapped, validated clean by dry run 2026-04-23
- ✅ Dry run on 2026-04-23 (against live WC API + prod DB via SSH tunnel): zero errors, 80 subs fetchable
- ❌ Stripe keys still pointing to test mode on VPS (needs swap)
- ❌ DNS still points to WC host (needs flip)
- ❌ WC subs still active — will double-bill if not stopped after migration
- ❌ Customer outreach NOT sent (timeline compressed — see "Post-launch comms" below)

---

## Cutover sequence (run in order tonight)

### 0. Pre-flight (do first)

- Verify Hiri is healthy: `ssh hiri-deploy 'docker ps'` should show both containers Up
- Verify local env: `.env` has WC + Stripe test creds; do NOT check Stripe creds into git
- Stash any WIP: `git status` should be clean or branch-isolated

### 1. Lower WC DNS TTL (optional, should have been done 48h ahead; do now anyway)

- Find current DNS provider for `rockabillyroasting.com` (unknown — user knows)
- Lower TTL on A records for `@` and `www` to minimum (60s if possible)

### 2. Swap Stripe keys on VPS

**Use a restricted key (`rk_live_…`), not a full secret key.** Scopes below are the minimum Hiri actually calls (audited 2026-04-23 against `internal/platform/payments/stripe.go`). Everything else stays at None — Hiri doesn't use Stripe Billing (own renewal engine), Stripe Tax (flat-rate in `platform/tax`), or SetupIntents (saves cards via `setup_future_usage: "off_session"` on PaymentIntents).

In Stripe dashboard (Live mode):

1. **Developers → API keys → Create restricted key** (name: `hiri-prod`). Grant Write on: Customers, Payment Intents, Payment Methods, Refunds. Grant Read on: Charges (needed for refund webhook payload resolution). Optional Read: Events, Webhook Endpoints. Everything else: None. Copy the `rk_live_…`.
2. **Developers → Webhooks → Add endpoint** `https://rockabillyroasting.com/webhooks/stripe`. Select events:
   - `payment_intent.succeeded`, `payment_intent.payment_failed`, `payment_intent.canceled`, `payment_intent.requires_action`
   - `charge.refunded`, `charge.refund.updated`
   - `payment_method.attached`, `payment_method.detached`
   - (Skip `customer.subscription.*` and `invoice.*` — Hiri has listeners but nothing on this account generates them.)
   Copy the signing secret (`whsec_…`).
3. Copy the live publishable key (`pk_live_…`) from the API keys page.

On VPS, edit `/opt/rockabilly-roasting/.env`:
- `STRIPE_SECRET_KEY` → `rk_live_…` (the restricted key)
- `STRIPE_PUBLISHABLE_KEY` → `pk_live_…`
- `STRIPE_WEBHOOK_SECRET` → `whsec_…` (the signing secret from step 2)

Restart and watch logs: `docker compose restart rr-app && docker logs -f rr-app` (watch 30s for auth errors).

### 3. Run migration (live, not dry)

From local machine, with SSH tunnel to VPS Postgres still active:

```bash
go run ./cmd/migrate --mapping=wc-variant-mapping.json 2>&1 | tee /tmp/migrate-live.log
```

Expected: 79–80 subs imported, ~77 unique customers created. Multi-item subs will split (13117, 12476, 14113 — 2 Hiri subs each, so total sub count in Hiri = original + 3).

### 4. Validate Hiri has the data

```bash
docker exec rr-postgres psql -U rr -d rr -c "SELECT count(*) FROM customers;"
docker exec rr-postgres psql -U rr -d rr -c "SELECT count(*), status FROM subscriptions GROUP BY status;"
docker exec rr-postgres psql -U rr -d rr -c "SELECT count(*) FROM addresses;"
```

Spot-check a few: pull by email, confirm variant + plan + next_order_at.

### 5. Bulk-stop WC renewals

The cleanest approach: **deactivate the Woo Subscriptions plugin** in WP admin. That halts the renewal cron without touching sub statuses, and is reversible.

Belt-and-suspenders: also bulk-set all 79 migrated subs to `on-hold` via WC REST API. Script needed (not yet written) — iterates the subs fetched during dry-run, calls `PUT /wp-json/wc/v1/subscriptions/{id}` with `{"status": "on-hold"}`.

**Verify:** confirm no new Stripe charges are created from WC after this step. The WC integration type was confirmed via metadata (uses `_stripe_source_id` / `_stripe_payment_method` — off-session charging on WC's cron, **not** Stripe Billing subscriptions). So stopping the cron stops the charges; no Stripe-side subscription cancellation needed.

### 6. Flip DNS

- A record for `@` → `5.161.245.139`
- A record (or CNAME to apex) for `www` → `5.161.245.139`
- Leave MX / SPF / DKIM / TXT alone
- Caddy on the VPS auto-provisions TLS via Let's Encrypt on first request. Confirm Caddyfile includes both `rockabillyroasting.com` and `www.rockabillyroasting.com`.

### 7. Monitor

- `docker logs -f rr-app` for errors
- Stripe dashboard → Events for incoming webhooks
- Try a real customer flow: log in, view subscriptions, place a test order from another browser
- Watch support inbox for confused customers

---

## Post-launch comms (owed but not yet done)

The original 2026-04-29 plan had customer emails going out 2026-04-28. Pulled-forward cutover means these will likely go out *after* launch instead of before. Drafts are in `migration-emails.md`.

| Sub | Customer | Type | Priority |
|-----|----------|------|----------|
| 9507 | Audrey Alexander | Price +$13.77 (3+ yr, $1,604 LTV) | HIGH — first Hiri renewal 2026-05-22 |
| **8895** | **Marisa Wachter** | **Price +$13.32 (5.5 yr, $2,571 LTV)** | **HIGHEST — 2026-04-24 WC renewal fires at old rate, first Hiri renewal 2026-05-24. Phone call recommended: 509-386-0990** |
| 9466 | Ivan Amaya | Price +$2.71 + card update (on-hold) | Lower |
| 13117 | Tessa Robinson | Multi-item confirmation (on-hold) | Low |
| 12476 | James Mitchell | Multi-item confirmation | Low |
| 14113 | Diana Villela | Multi-item confirmation (new sub) | Low |

Meghann Barker (8324) was set to **on-hold in WC before cutover** (2026-04-23). Order history showed no renewal activity since 2024 despite "active" status — effectively dormant. She'll import as on-hold; no outreach needed, no override applied.

---

## Key files

- `wc-variant-mapping.json` — validated variant mapping (21 WC IDs → Hiri UUIDs)
- `docs/woocommerce-migration-action-items.md` — full status of all pre-cutover items
- `migration-emails.md` — customer email drafts
- `cmd/migrate/main.go` — the importer (entry point)
- `.env` — WC + Stripe creds (local; VPS has its own at `/opt/rockabilly-roasting/.env`)

---

## VPS quick-reference

- **Host:** Hetzner CPX21, `5.161.245.139`, SSH profile `hiri-deploy`
- **Containers:** `rr-app` (app, port 3000 on host → 8080 in container), `rr-postgres` (Postgres 17, internal only)
- **Postgres access:**
  - DB: `rr`, user: `rr`, password: `docker exec rr-postgres env | grep POSTGRES_PASSWORD`
  - Container IP (for SSH tunneling to Postgres): `172.18.0.2`
  - Tunnel: `ssh -L 5433:172.18.0.2:5432 hiri-deploy`
  - Temp local DATABASE_URL: `postgres://rr:<PASSWORD>@localhost:5433/rr?sslmode=disable`
- **Working dir on VPS:** `/opt/rockabilly-roasting/`
- **Logs:** `docker logs -f rr-app` on VPS

---

## Rollback options (worst case)

- **If Hiri is broken post-migration:** reactivate Woo Subscriptions plugin, set migrated WC subs back to `active`, revert DNS, restore Stripe test keys. WC data was never deleted; it's all still there.
- **If Stripe is broken:** revert the three env keys on VPS, restart `rr-app`. The migration itself doesn't touch Stripe.
- **Migration is idempotent on re-run?** No — it creates new customer/sub records each run. If you re-run, either truncate Hiri subs first or diff carefully. Test this assumption before re-running.

---

## Known discrepancies from original plan

- Multi-item subs: original plan listed 13117 and 12476 (3 items). Dry run on 2026-04-23 found 13117 (unchanged), 12476 (reduced from 3 → 2 items, customer dropped Cloud 9), and a new one **14113** (Diana Villela, 2 items).
- Grandfathered pricing: original plan listed 2 customers (Audrey 9507, Ivan 9466). Full audit on 2026-04-23 found a third — **Marisa Wachter (8895)**, Bike Blend 3lb at $45 vs $58.32, 5.5 years tenure.
- Missing `next_payment_date`: original plan listed 3 (13819, 13644, 8324). Two resolved in WC since March. 8324 (Meghann Barker) turned out to be dormant — order history showed no renewals since 2024 despite "active" status. Set to on-hold in WC on 2026-04-23 before cutover; imports as on-hold.
