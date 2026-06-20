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

Resolve company names to OS customer IDs (the census prints IDs; or query the OS `/customers`
endpoint). You'll feed the IDs to `os-migrate --only` and the emails to `os-welcome --emails`.

---

## 5. Per-batch procedure

Run top to bottom. **Always rehearse against a prod copy first (§6).**

1. **Census + pick** the batch (§4). Record the OS IDs and emails.

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
  AND c.email IN ('a@x.com','b@y.com')   -- the batch
ORDER BY orders DESC;
-- Expect: every row price_list='Wholesale 2026', net=7, addrs small (no bloat).

-- Any imported order left with zero line items? (should be 0)
SELECT count(*) FROM orders o
WHERE o.number LIKE 'OS-%'
  AND NOT EXISTS (SELECT 1 FROM line_items li WHERE li.order_id = o.id);

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

## 10. Final cutover (after all batches are migrated)

From [`orderspace-migration-progress.md`](./orderspace-migration-progress.md), in order:
freeze OS ordering → final delta import for anything placed during the freeze → flip
`wholesale.rockabillyroasting.com` DNS to Hiri → QB reconciliation → watch metrics/Sentry for two
weeks, keeping read-only OS access for 30 days for rollback.

---

## Batch log

Append a row per batch.

| Date | Batch (count) | OS IDs / notes | Imported | Welcomed | By |
|---|---|---|---|---|---|
| _pending_ | Pilot (8) | Wandering Bean, Richland Baptist, Healthy Vibes, Novel Coffee, Coffee Pot Seattle, Yellow Cafe, Steam and cream, Caterpillar Cafe | validated on prod copy only | — | — |
