# Orderspace → Hiri Wholesale Migration Runbook

Durable, repeatable procedure for moving wholesale customers from Orderspace (OS) into Hiri,
a batch at a time. Use this every time you migrate a batch. The one-off architectural reasoning
lives in [`orderspace-migration-plan.md`](./orderspace-migration-plan.md); the running log of
what's been done lives in [`orderspace-migration-progress.md`](./orderspace-migration-progress.md).
This doc is the **how-to**.

> **Golden rule:** nothing in the import path sends email. The only step that contacts a customer
> is `cmd/os-welcome --send`. You can run the entire import + verify loop as many times as you like
> with zero risk of emailing anyone. Always dry-run first.

---

## 1. Decisions locked in (do not re-litigate per batch)

| Decision | Value |
|---|---|
| Payment terms | **NET 7** for every migrated customer (OS terms are *not* carried over) |
| Price list | **`Wholesale 2026`** for everyone… |
| …except | **Tailwind Concessions** stays on Tailwinds pricing — **handled manually post-migration**, not by the importer |
| Order history | **Successful orders only** (fulfilled/invoiced); cancelled + incomplete are dropped |
| Order channel + fulfillment | Imported orders land as **`channel = wholesale`** with completed history mapped to **`fulfillment_status = delivered`** (terminal — keeps them out of the admin needs-action queue). Orders still open in OS at cutover (invoiced/released/part_fulfilled) stay unfulfilled/partial and correctly show as work in the *wholesale* fulfillment queue. Fixed after Batch 1 (commit `a11c4d2`) — Batch 1 originally imported as retail/fulfilled and flooded the retail queue with 167 phantom "needs action" orders; prod data was corrected by hand 2026-07-13. |
| Addresses | **Deduplicated** — OS repeats the same address per order; we collapse to one |
| Status | Imported customers are set `wholesale_status = approved` (OS `active` → approved, `closed` → suspended, `new` → pending) |

These are encoded in the tools. If any of them change, update the tools *and* this table.

---

## 2. The three tools

All live under `cmd/`. All read `DATABASE_URL` and (except where noted) Orderspace credentials from
`.env`.

| Tool | Purpose | Writes? |
|---|---|---|
| `cmd/os-report` | Read-only census of the OS tenant: groups, price lists, terms, products, customers, and a data-hygiene list of customers needing fixes. | No |
| `cmd/os-migrate` | The importer: creates wholesale customers + successful order history, assigns Wholesale 2026 + NET 7, translates SKUs, dedups addresses. | DB only |
| `cmd/os-welcome` | Sends the migration welcome email (password-setup link). Dry-runs unless `--send`. | DB (tokens) + email when `--send` |

### Required environment

```
ORDERSPACE_CLIENT_ID, ORDERSPACE_CLIENT_SECRET   # os-report, os-migrate
DATABASE_URL                                      # all three
EMAIL_FROM, STORE_NAME, BASE_URL                  # os-welcome (have sane defaults)
POSTMARK_SERVER_TOKEN                             # os-welcome --send only
```

### Flags

```
cmd/os-report      (no flags — just prints the census)

cmd/os-migrate
  --only <ids>          comma-separated OS customer IDs (the batch). Omit = all customers.
  --dry-run             validate + report, write nothing
  --customers-only      skip order history

cmd/os-welcome
  --emails <list>       comma-separated customer emails (required)
  --send                actually send; without it, dry-runs (no tokens, no email)
  --from <addr>         override From (default $EMAIL_FROM)
  --bcc <list>          optional BCC
```

---

## 3. Prerequisite: the `Wholesale 2026` price list must exist and be correct

`os-migrate` looks the price list up by the exact name **`Wholesale 2026`** and **aborts if it's
missing**. Before any batch, confirm it exists and that its prices match the intended go-forward
(OS 2026) prices. Regular coffee at the 2026 tier is `$12.50 / $37.50 / $62.50` (12oz / 3lb / 5lb);
Cascadia Decaf is `$13.50 / $40.50 / $67.50`.

```sql
SELECT v.sku, max(p.amount) FILTER (WHERE p.price_list_id IS NOT NULL) AS wh2026_cents
FROM variants v
JOIN price_sets ps ON ps.variant_id = v.id
JOIN prices p ON p.price_set_id = ps.id
WHERE v.sku LIKE 'C9-%'
GROUP BY v.sku ORDER BY v.sku;
```

> History: in June 2026 this list was briefly mis-keyed (decaf prices on every coffee, $67.50
> across the board). If 5lb regular coffee shows $67.50 instead of $62.50, **stop** — the list is
> wrong and every migrated customer would be overcharged.

---

## 4. Picking a batch

Run the census and select **clean** customers:

```bash
go run ./cmd/os-report
```

A good batch customer:
- Is in a real customer group with a current price list and a real email (the census prints a
  **"Cleanup needed"** list — exclude everyone on it; those need fixing in OS first).
- Is **not** Tailwind Concessions (handled manually) and **not** a Bunker / white-label account
  (deferred — those products don't map cleanly).
- Has **recent order history** so you can validate prices against real invoices.

Mix value tiers but keep the **highest-value accounts out of the first batch** (blast radius). The
validated pilot batch (8 accounts) was Wandering Bean, Richland Baptist Church, Healthy Vibes,
Novel Coffee, The Coffee Pot Seattle, Yellow Cafe, Steam and cream, Caterpillar Cafe.

### Resolving company names → OS IDs + emails

You feed OS customer IDs to `os-migrate --only` and emails to `os-welcome --emails`, so you need
both for each account in the batch. **`os-report` does not give you these for clean customers** — it
only prints `id | company | email` for the **Cleanup-needed** list. For everyone else you must hit
the OS `/customers` endpoint directly. Get an OAuth token (client-credentials, same creds the tools
use) and page through `/v1/customers`:

```bash
CID=$(grep '^ORDERSPACE_CLIENT_ID=' .env | cut -d= -f2- | tr -d '"')
CSEC=$(grep '^ORDERSPACE_CLIENT_SECRET=' .env | cut -d= -f2- | tr -d '"')
TOKEN=$(curl -s -X POST https://identity.orderspace.com/oauth/token \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode "client_id=$CID" --data-urlencode "client_secret=$CSEC" \
  --data-urlencode 'grant_type=client_credentials' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["access_token"])')
# RR has ~54 customers, so one page of 100 covers it; paginate with starting_after=<last id> if not.
curl -s 'https://api.orderspace.com/v1/customers?limit=100' -H "Authorization: Bearer $TOKEN" \
  | python3 -c '
import sys,json
for c in json.load(sys.stdin)["customers"]:
    bs=c.get("buyers") or []
    em=next((b["email_address"] for b in bs if b.get("email_address")), (c.get("email_addresses") or {}).get("orders",""))
    print(c["id"], "|", c["company_name"], "|", em)'
```

Two field gotchas, both confirmed in rehearsal:
- The buyer email key is **`email_address`** (not `email`); the account-level fallback is
  `email_addresses.orders`.
- The importer **stores emails lowercased**. `os-welcome` lowercases its `--emails` input too, so
  mixed-case OS emails still resolve — but a hand-written **case-sensitive** SQL `WHERE email IN (...)`
  verify query will false-miss. Use `lower(c.email) IN (...)` when spot-checking (§8).

---

## 5. Per-batch procedure

Run top to bottom. **Always rehearse against a prod copy first (§6).**

1. **Census + pick** the batch (§4). Record the OS IDs and emails — `os-report` only prints these
   for Cleanup-needed accounts, so resolve clean accounts via the `/customers` recipe in §4.

2. **Dry-run the import:**
   ```bash
   go run ./cmd/os-migrate --only "cu_aaa,cu_bbb,..." --dry-run
   ```
   Check the status breakdowns and that the customer count matches your batch.

3. **Import for real:**
   ```bash
   go run ./cmd/os-migrate --only "cu_aaa,cu_bbb,..."
   ```
   Expected: `Customers created` = batch size, `Line items created` > 0, **`Warnings` near 0**.
   A spike in `no SKU mapping` / `not in catalog` warnings means a catalog change broke the SKU
   map — fix `cmd/os-migrate/skumap.go` (§7) before continuing.

4. **Verify** (§8): pricing/terms/addresses assigned, line items matched, prices reconcile.

5. **Dry-run the welcome emails:**
   ```bash
   go run ./cmd/os-welcome --emails "a@x.com,b@y.com,..."
   ```
   Confirm every address resolves to an approved wholesale account (mismatches are printed as
   `SKIP`), and eyeball the rendered sample.

6. **Send the welcome emails:**
   ```bash
   go run ./cmd/os-welcome --emails "a@x.com,b@y.com,..." --send
   ```
   Each customer gets a 72h `/wholesale/setup` link. Re-running is safe (mints a fresh token);
   don't send twice in a row without reason.

7. **Spot-check** one account: it logs in via the setup link, sees Wholesale 2026 prices, and its
   order history is present.

8. **Log the batch** in the table at the bottom of this doc and in
   [`orderspace-migration-progress.md`](./orderspace-migration-progress.md).

9. **Tailwind Concessions only:** if in this batch, manually set its pricing in admin afterward
   (it imports on Wholesale 2026 like everyone else).

---

## 6. Rehearsing against a copy of production (do this before every real batch)

The import is idempotent-ish but not reversible in place; rehearse on a throwaway first. This also
catches catalog drift (new/renamed products break the SKU map).

```bash
# 1. Pull a fresh read-only dump from prod (does not touch the backup rotation)
ssh rr-deploy 'docker exec rr-postgres pg_dump -Fc -U rr -d rr' > /tmp/rr-fresh.dump

# 2. Stand up a throwaway PG17 container (isolated port)
docker rm -f rr-migrate-practice 2>/dev/null || true
docker run -d --name rr-migrate-practice -e POSTGRES_PASSWORD=practice -p 5455:5432 postgres:17-alpine
# wait for readiness
until docker exec rr-migrate-practice pg_isready -U postgres >/dev/null 2>&1; do sleep 0.5; done

# 3. Restore
docker cp /tmp/rr-fresh.dump rr-migrate-practice:/tmp/fresh.dump
docker exec rr-migrate-practice pg_restore --create --no-owner --no-privileges -U postgres -d postgres /tmp/fresh.dump

# 4. Point the tools at it and run the batch (real run is fine — it's a throwaway)
export DATABASE_URL="postgres://postgres:practice@localhost:5455/rr?sslmode=disable"
go run ./cmd/os-migrate --only "cu_aaa,cu_bbb"
go run ./cmd/os-welcome --emails "a@x.com,b@y.com"   # dry run — never --send against practice

# 5. To re-run from clean: drop + restore again
docker exec rr-migrate-practice psql -U postgres -d postgres -c "DROP DATABASE IF EXISTS rr;"
docker exec rr-migrate-practice pg_restore --create --no-owner --no-privileges -U postgres -d postgres /tmp/fresh.dump

# 6. Tear down when done
docker rm -f rr-migrate-practice
```

Backup/restore mechanics and prod access are documented in
[`backup-restore-runbook.md`](./backup-restore-runbook.md).

---

## 7. SKU translation reference

OS and Hiri use different SKU schemes. `cmd/os-migrate/skumap.go` (`translateSKU`) maps OS order-line
SKUs to Hiri variant SKUs; the candidate is confirmed against the catalog, so an unmappable line is
**skipped with the original OS SKU/name preserved in line metadata** (never silently mis-attached).

| OS | Hiri | Notes |
|---|---|---|
| `0001`–`0008` | `ETH, CT, C9, GUA, RIU, CAS, 2ST, BB` | product code → prefix |
| `0005` White Coffee | `RIU` (Rev It Up) | product was rebranded |
| `0009-<flavor>` | `<code>-12O-WB` | retail 12oz bags → whole-bean 12oz |
| `0010-*` | — | Bunker white-label; skipped |
| size `1LB`→`12O`, `3LB`→`3LB`, `5LB`→`5LB` | | 1lb bag retired for 12oz |
| `0005` `1LB-EG` | `RIU-1LB-ESP` | Rev It Up kept a 1lb espresso |
| grind `DG`→`DRI`, `EG`→`ESP`, `WB`→`WB` | | Hiri `FP` has no OS source |

**Assumptions (low risk — history is reference data, original SKU preserved):** retail bags default
to whole-bean grind; non-RIU `1LB` orders attach to the `12O` variant.

**When the catalog changes** (new coffee, renamed product, new size), the symptom is a jump in
import warnings. Fix the maps in `skumap.go`, rebuild, re-rehearse.

---

## 8. Verification queries

Run after each import (point `psql` at the target DB).

```sql
-- Pricing, terms, address count, order count for the batch
SELECT c.company_name, c.wholesale_status, pl.name AS price_list, c.payment_terms_days AS net,
       (SELECT count(*) FROM addresses a WHERE a.customer_id = c.id) AS addrs,
       (SELECT count(*) FROM orders o WHERE o.customer_id = c.id)    AS orders
FROM customers c
LEFT JOIN price_lists pl ON pl.id = c.price_list_id
WHERE c.account_type = 'wholesale'
  AND lower(c.email) IN ('a@x.com','b@y.com')   -- the batch; lower() — emails are stored lowercased
ORDER BY orders DESC;
-- Expect: every row price_list='Wholesale 2026', net=7, addrs small (no bloat).

-- Any imported order left with zero line items? (should be 0)
SELECT count(*) FROM orders o
WHERE o.number LIKE 'OS-%'
  AND NOT EXISTS (SELECT 1 FROM line_items li WHERE li.order_id = o.id);

-- Channel + fulfillment sanity (see §1): every imported order must be wholesale,
-- and only orders genuinely open in OS at cutover may be non-terminal.
-- First count should be 0; eyeball the second — non-delivered rows should match
-- the dry-run's invoiced/released/part_fulfilled counts, and will appear in the
-- admin *wholesale* needs-action queue as real work.
SELECT count(*) FROM orders WHERE number LIKE 'OS-%' AND channel <> 'wholesale';
SELECT fulfillment_status, count(*) FROM orders
WHERE number LIKE 'OS-%' GROUP BY 1 ORDER BY 2 DESC;

-- Spot-check a line item maps to the right variant and preserves the OS price
SELECT o.number, v.sku, li.quantity, li.unit_price AS paid_cents,
       li.metadata->>'orderspace_sku' AS os_sku
FROM orders o
JOIN line_items li ON li.order_id = o.id
JOIN variants v ON v.id = li.variant_id
WHERE o.number = 'OS-####';
```

**Price parity caveat:** OS prices changed over time, so a customer's *historical* line items carry
the price they paid *then* (preserved as-is). The migration's promise is the *go-forward* price:
once on Wholesale 2026, new orders price at the 2026 tier. Don't expect old line items to equal
current list prices.

---

## 9. Known gotchas

- **Pre-existing email.** If a batch email already exists in Hiri (e.g. a retail account), the
  importer **upgrades it to wholesale** (sets type, status, price list, NET 7) rather than creating
  a duplicate. Confirm that's intended for any real collision.
- **Closed/new OS customers** map to `suspended`/`pending`. They won't be emailable by `os-welcome`
  (which only sends to approved accounts) — usually you just exclude them from the batch.
- **Order numbers** are namespaced `OS-<number>` to avoid colliding with native Hiri orders.
- **Welcome link validity** is 72h. Re-run `os-welcome --send` to reissue for anyone who let it
  lapse.

---

## 10. Proposed batch schedule

Three batches, **two weeks from the initial batch to the final batch**. Dates are targets; the
**gates** govern whether to proceed — don't advance on the calendar alone. Account membership is
indicative — **confirm against the latest `os-report` census** before each batch, since the roster
and data-hygiene list can shift.

| Batch | Target | Size | Who | Why this grouping |
|---|---|---|---|---|
| **1 — Pilot** | Mon **Jun 23** | 8 | Validated pilot accounts | Prove the pipeline on clean, low-to-mid-value, recent-order accounts where a mistake is cheap. |
| **2 — Main** | Mon **Jun 30** | ~29 | All remaining clean accounts with order history, incl. the high-value ones | The bulk. Run once the pilot has soaked for a week with no pricing/login complaints. |
| **3 — Final** | Mon **Jul 7** | ~tail | Cleanup-needed (post-fix), zero-history, Tailwind, Bunker, internal | The special cases — each needs something done first. |

### Batch 1 — Pilot (8)
Wandering Bean, Richland Baptist Church, Healthy Vibes, Novel Coffee, The Coffee Pot Seattle,
Yellow Cafe, Steam and cream, Caterpillar Cafe.
**Gate to start:** `Wholesale 2026` price list verified correct (§3); rehearsed on a prod copy (§6).

### Batch 2 — Main bulk (~29)
The rest of the clean, history-bearing accounts. Includes the whales held out of the pilot —
Charis Coffee, Sip&Co., Cafe Magnolia, Cafenated, Angel Brook Farm, Calvary Chapel of Tri-Cities,
Kaffrin's, The Village Bistro, Faith Tri-Cities, Mama's Java — plus the mid/low tier: Caffeine Bar,
New Vintage Church, Rise & Shine Bake Shop, Walla Walla Tattoo Co, SUB Coffee Shop (WSU), Amendment
XXI Bar, Tri-Cities Food CoOp, Ice Harbor Brewing, Tri-Tech Skills Center, Fresh Picks, Sip&Dip,
CKJT Architects, Kennewick School District, Port of Benton, Jae's Coffee Co., PKA Holdings (Last
Resort), Richland High School (Bomb Shelter), JayDay Cafe & Boba, Foodies, Tina's Tasty Treats.
Consider running it in two sittings within the week (high-value first) if you'd rather watch a
smaller blast radius at a time.
**Gate to start:** pilot accounts have logged in and ordered without pricing/support issues.

### Batch 3 — Final / special cases (~tail)
Each sub-group needs a precondition met first:
- **Cleanup-needed (after Marisa fixes them in OS):** ExpressUp Coffee, Port of Benton Admin,
  Hanford High School, Panadería y Antojitos, Just Juice, Kool Beanz Koffee, Byte Brew, and MOCHA
  EXPRESS (move off the legacy 2024 list). See the census "Cleanup needed" list for the live set.
- **Zero order history (clean, low risk):** Stacks Mobile Bistro, The B Spot — accounts only.
- **Tailwind Concessions:** import on Wholesale 2026, then **manually switch to Tailwinds pricing**
  in admin (§1, §5 step 9).
- **Bunker (Bunker Uniforms + white-label):** only if white-label onboarding is ready; otherwise
  **explicitly defer beyond this plan**.
- **Exclude:** BASELING Sports Cards (census suggests deleting in OS) and internal/test accounts
  (Firefly Software, and confirm whether Kagen's `talia@rockabillyroasting.com` is internal).
**Gate to start:** Marisa has completed the OS cleanup pass; white-label decision made for Bunker.

> **Why two weeks:** a week to let the pilot prove pricing/login/ordering end-to-end, a week to move
> the bulk while watching for issues, then a final sweep for the accounts that needed prep. If the
> pilot surfaces a problem, fix it and re-rehearse — slip the schedule rather than push a bad batch.

---

## 11. Final cutover (after all batches are migrated)

From [`orderspace-migration-progress.md`](./orderspace-migration-progress.md), in order:
freeze OS ordering → final delta import for anything placed during the freeze → flip
`wholesale.rockabillyroasting.com` DNS to Hiri → QB reconciliation → watch metrics/Sentry for two
weeks, keeping read-only OS access for 30 days for rollback.

---

## Batch log

Update each row as a batch completes (target dates from §10).

| Target | Batch (count) | Notes | Imported | Welcomed | By |
|---|---|---|---|---|---|
| Jun 23 → done Jul 7 | 1 — Pilot (8) | Wandering Bean, Richland Baptist, Healthy Vibes, Novel Coffee, Coffee Pot Seattle, Yellow Cafe, Steam and cream, Caterpillar Cafe | ✅ 2026-07-07 (169 orders, 298 lines, 0 warnings) | ✅ 2026-07-07 (8/8 sent) | Logan + Claude |
| Jun 30 | 2 — Main (~29) | remaining clean accounts w/ history (see §10) | — | — | — |
| Jul 7 | 3 — Final (~tail) | cleanup-needed + zero-history + Tailwind + Bunker (see §10) | — | — | — |
