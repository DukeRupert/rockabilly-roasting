# WooCommerce Migration — Action Items

> Current status from dry run on **2026-04-23**. 80 subscriptions fetched (69 active + 11 recent on-hold) and validated against `wc-variant-mapping.json`. **Zero mapping errors.** Cutover scheduled for Wed 2026-04-29.

---

## 1. Subscription Plans — ✅ Done

All 6 plans created in Hiri admin, active, 10% discount. Orphan `E'` plan deleted; casing normalized to "Every N Days".

| Plan Name | Interval | Discount % | Status |
|-----------|----------|------------|--------|
| Every 7 Days | `every_7_days` | 10 | ✅ |
| Every 14 Days | `every_14_days` | 10 | ✅ |
| Every 21 Days | `every_21_days` | 10 | ✅ |
| Every 30 Days | `every_30_days` | 10 | ✅ |
| Every 60 Days | `every_60_days` | 10 | ✅ |
| Every 90 Days | `every_90_days` | 10 | ✅ |

---

## 2. Products, Variants & Mapping File — ✅ Done

All 21 WC variation IDs mapped in `wc-variant-mapping.json`. Validated by dry run — no unresolved variants, no grind errors.

Resolved ambiguities from the original plan:
- **Wheelhouse Blend (5064)** → Bike Blend 5lb WB (confirmed: same product, legacy label)
- **White Coffee (4432)** → Rev It Up 12oz WB (closest weight; 4432 doesn't appear in any current sub so low impact)
- **Chop Top (294/296/297)** and **Cascadia (305/307)** have proper grind maps (Whole Bean / Drip Ground)
- **Other WC variations** carry no `grind-type` meta on their line items — single-UUID mapping is correct for those (dry run confirmed)

---

## 3. Multi-Item Subscriptions — Need Customer Confirmation

Three multi-item subs will each split into multiple Hiri subscriptions. Batched renewals should consolidate them into one order per day, but confirm the customer expects multiple subscription records.

| Sub ID | Items | Status |
|--------|-------|--------|
| 13117 | 2-Stroke Blend 3lb + Chop Top | Documented, verified unchanged |
| 12476 | Bike Blend 12oz + 2-Stroke Blend 12oz | Was 3 items in March; customer dropped Cloud 9 — verify current state with customer |
| **14113** | 2-Stroke Blend 12oz + Cloud 9 Espresso 12oz | **New since last dry run** — needs customer outreach |

---

## 4. Subscriptions Missing `next_payment_date` — Resolved

Two of the original three were fixed in WC since 2026-03-16. The last (8324, Meghann Barker) turned out to be dormant — a full walk of her order history on 2026-04-23 showed no "Order created to record renewal" entries after 2024. WC had her as "active" but the renewal cron wasn't firing. Set to on-hold in WC before cutover.

| Sub ID | Status |
|--------|--------|
| 13819 | ✅ Resolved (has date in WC) |
| 13644 | ✅ Resolved (has date in WC) |
| 8324 | ✅ Set to on-hold in WC 2026-04-23 (dormant since 2024) — imports as on-hold, no override needed |

---

## 5. Non-Standard Pricing Customers — Still Need Outreach

Grandfathered pricing won't carry over. Full audit on 2026-04-23 found **three** customers (the original plan listed two). All three pay below the current 10%-off-retail subscriber rate on their product; no customers are paying above retail.

| Sub ID | Customer | Item | Old | New | Δ | Tenure / LTV | First Hiri renewal |
|--------|----------|------|-----|-----|---|--------------|--------------------|
| 9507 | Audrey Alexander | Cloud 9 Espresso 3lb | $44.55 | $58.32 | +$13.77 (+31%) | 3+ yrs / $1,604 | 2026-05-22 |
| 9466 | Ivan Amaya (on-hold) | Cloud 9 Espresso 12oz | $13.49 | $16.20 | +$2.71 (+20%) | 3+ yrs / $1,349+ | whenever he restarts |
| **8895** | **Marisa Wachter** | **Bike Blend 3lb** | **$45.00** | **$58.32** | **+$13.32 (+30%)** | **5.5 yrs / $2,571** | 2026-05-24 |

**Notes:**
- Marisa's next WC renewal fires **tomorrow (2026-04-24)** at the old $45 rate — no intervention needed on that one. The price change hits her May 24 renewal on Hiri.
- Ivan is on-hold because his 2026-04-05 renewal failed. His outreach email should also prompt him to update his payment method.
- Marisa is the highest-stakes relationship (longest tenure, highest LTV). Consider calling (509-386-0990) instead of emailing.

**Action:** contact all three before 2026-04-29. If any cancels, cancel the WC sub *before* cutover so it isn't imported.

---

## Dry Run Command Reference

```bash
# Validate mapping against live WC data (read-only, safe to re-run)
go run ./cmd/migrate --dry-run --mapping=wc-variant-mapping.json

# Cutover day — live run (2026-04-29)
go run ./cmd/migrate --mapping=wc-variant-mapping.json
```

Requires `DATABASE_URL` pointing at the production Hiri DB (via SSH tunnel to `rr-postgres` on the VPS for local execution) plus `WC_CONSUMER_KEY` / `WC_CONSUMER_SECRET` / `WC_BASE_URL` from `.env`.
