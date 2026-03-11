# WooCommerce to Hiri — Migration Plan

> **Scope:** Migrate subscription customers from WooCommerce to Hiri.
> **Source:** WooCommerce REST API v3 + WooCommerce Subscriptions plugin
> **Payment Gateway:** Stripe only (both platforms)
> **Data pulled:** 2026-03-11

---

## 1. Current State (WooCommerce)

### Subscription Totals
| Status | Count |
|--------|-------|
| Active | 68 |
| On-Hold | 103 |
| Pending Cancel | 3 |
| Cancelled | 190 |
| Expired | 33 |

**66 unique customers** with active subscriptions. 26,542 total WC customer accounts (vast majority are spam/bot registrations with no data).

### Billing Intervals in Active Subscriptions
| WC `billing_period/interval` | Count | Hiri Equivalent | Status |
|------------------------------|-------|-----------------|--------|
| `month/1` (monthly) | 42 | `every_30_days` | implemented |
| `week/3` (every 3 weeks) | 9 | `every_21_days` | implemented |
| `week/2` (every 2 weeks) | 7 | `every_14_days` | implemented |
| `week/1` (weekly) | 6 | `every_7_days` | implemented |
| `month/3` (every 3 months) | 2 | `every_90_days` | implemented |
| `month/2` (every 2 months) | 1 | **needs new plan** `every_60_days` | implemented |
| `week/4` (every 4 weeks) | 1 | **needs mapping** → `every_30_days` | use existing |

All interval enum values (`every_7_days` through `every_90_days`) already exist in `domain/subscription.go`. The `week/4` interval (28 days) can map to `every_30_days` — close enough for a single subscriber.

### Products in Active Subscriptions
| WC Product ID | Name | Weights Used | Sub Count |
|---------------|------|-------------|-----------|
| 229 | Cloud 9 Espresso | 12oz, 3lb, 5lb | 38 |
| 293 | Chop Top | 12oz, 3lb, 5lb | 5 |
| 304 | Cascadia Decaf | 12oz, 3lb | 4 |
| 7620 | 2-Stroke Blend | 12oz, 3lb, 5lb | 6 |
| 5061 | Bike Blend | 12oz, 3lb, 5lb | 7 |
| 282 | Ethiopia | 12oz, 3lb, 5lb | 4 |
| 271 | Guatemala Tikal | 12oz, 5lb | 2 |
| 4431 | White Coffee | 1lb | 1 |
| — | Wheelhouse Blend | 5lb | 1 |
| 3478 | Single Origin - Subscription | (legacy product) | 1 |

**Grind preference:** 90% Whole Bean (53), 10% Drip Ground (6).

### Fulfillment Split (Active Subs)
| Category | Count | Notes |
|----------|-------|-------|
| Local (delivery/pickup) | 47 (69%) | Free Local Delivery, Donation $5, Local Pickup |
| Shipped (USPS/UPS) | 21 (31%) | Free Shipping (5lb+), Flat Rate $6 |

**85% of active subscribers are in WA state.** The business is heavily local-delivery.

### Revenue (Active Only)
- **Average order value:** $51.37
- **Gross recurring (all intervals):** ~$3,493/billing cycle (not normalized to monthly)

### Subscription Complexity
| Pattern | Count | Notes |
|---------|-------|-------|
| Single item, qty 1 | 61 | Standard case |
| Single item, qty > 1 | 6 | Subs 13874 (x2), 13593 (x2), 13223 (x2), 11617 (x2), 7532 (x4), 6678 (x3) |
| Multi-item subscription | 1 | Sub 13117: 2-Stroke 3lb + Chop Top in one sub |
| Fixed-term (has end_date) | 9 | 3-month commitments that auto-expire |
| Indefinite (no end_date) | 59 | Renews until cancelled |
| Non-standard pricing | 2 | Sub 9507: $44.55 (custom), Sub 9466: $13.49 (legacy price) |

### Shipping Methods
| WC Method | Count | Hiri Approach |
|-----------|-------|---------------|
| Free Local Delivery - No Contact | 27 | Local delivery zone |
| Free shipping | 13 | Free for 5lb+ orders |
| Donation for Local Delivery Costs | 10 | $5 delivery fee |
| Flat Rate | 8 | $6 flat rate shipping |
| Local Pickup at 101 West Kennewick Ave. | 7 | Pickup option |
| Local Delivery | 2 | Local delivery zone |
| Local Delivery - No Contact | 1 | Local delivery zone |

### WC Subscription Product Types (3 legacy products)
| WC ID | Name | Status | Notes |
|-------|------|--------|-------|
| 3478 | Single Origin - Subscription | hidden | Legacy, frequency baked into variations |
| 3613 | Blended Origin - Subscription | hidden | Legacy, frequency baked into variations |
| 6626 | Bigger Bolder Sample Pack | hidden | Rotating sample pack with decaf swap option |

These are `variable-subscription` product types unique to WooCommerce Subscriptions. In Hiri, subscriptions are decoupled from products — any subscribable product + plan combination replaces these. **No action needed for migration; existing active subs reference the underlying coffee products by variation_id.**

### Coffee Catalog (products to recreate in Hiri)
| WC ID | Name | Type | Attributes | Subscription? |
|-------|------|------|------------|---------------|
| 229 | Cloud 9 Espresso | variable | Weight (12oz/3lb/5lb), Grind | Yes |
| 7620 | 2-Stroke Blend | variable | Weight (12oz/3lb/5lb), Grind | Yes |
| 5061 | Bike Blend | variable | Weight (12oz/3lb/5lb), Grind | Yes |
| 282 | Ethiopia | variable | Weight (12oz/3lb/5lb), Grind | Yes |
| 271 | Guatemala Tikal | variable | Weight (12oz/3lb/5lb), Grind | Yes |
| 293 | Chop Top | variable | Weight (12oz/3lb/5lb), Grind | Yes |
| 304 | Cascadia Decaf | variable | Weight (12oz/3lb/5lb), Grind | Yes |
| 4431 | White Coffee | variable | Weight (1lb/2lb/5lb) | Yes |
| 6532 | Sample Pack | simple | Grind | No |

Non-coffee products (mugs, shirts, grinders, chocolates, etc.) are one-time purchase only — no subscription migration needed.

---

## 2. Gap Analysis — Features Needed Before Migration

### 2a. Already Implemented (No Work Needed)

These features are fully built and match WC requirements:

- **Subscription domain types** — `Subscription`, `SubscriptionPlan`, `SubscriptionOrder` structs with all fields
- **Subscription interval enum** — All needed values exist: `every_7_days` through `every_90_days`
- **Quantity support** — `subscription.quantity` field (1–10), applied in renewal pricing
- **Fixed-term support** — `subscription.ends_at` nullable field for auto-expiring subs
- **Store layer** — Full CRUD for plans, subscriptions, and subscription_orders
- **App layer** — Create, pause, resume, cancel, mark past_due, advance period, link order
- **Renewal service** — Both single and batched renewals with Stripe PaymentIntent
- **Renewal scheduler** — Groups by `(customer_id, shipping_address_id)`, enqueues batch jobs
- **River job workers** — `SubscriptionRenewalWorker`, `BatchRenewalWorker`, `RenewalSchedulerWorker`
- **State machine** — active/paused/past_due/cancelled/expired with transition guards
- **Audit integration** — Audit records for all subscription lifecycle events
- **Metrics** — Renewal success/failure counters
- **Database migrations** — Tables 008, 017, 018, 032 covering all schema

### 2b. Needs Implementation Before Migration

| Feature | Priority | Effort | Status |
|---------|----------|--------|--------|
| **HTTP handlers for subscription admin** | P0 | Medium | DONE — `web/admin_subscriptions.go`, `web/admin_plans.go` |
| **Migration CLI tool** | P0 | Medium | DONE — `cmd/migrate/main.go` with --dry-run and --mapping support |
| **Multi-item subscription support** | P1 | Small | DONE — migration tool splits multi-item WC subs into separate Hiri subs |
| **Non-standard pricing on individual subs** | P1 | Small | 2 subs have custom prices ($44.55, $13.49) — contact customers before migration |
| **Shipping method on subscription** | P1 | Medium | Hiri needs shipping calculator for renewal fulfillment routing |
| **Customer self-service portal** | P2 | Large | Subscribers need to view/pause/cancel their own subscriptions |
| **Subscription checkout flow** | P2 | Large | New subscriber signups (not a day-1 blocker) |

### 2c. Feature Details

#### Multi-Item Subscription (Sub 13117)
**Current state:** One WC subscription with 2 line items (2-Stroke 3lb + Chop Top).
**Hiri model:** Each subscription owns exactly one `variant_id`.
**Recommendation:** Split into 2 Hiri subscriptions for the same customer on the same plan. The batched renewal system will automatically consolidate them into one order. This is the cleanest approach — no schema changes needed.

#### Non-Standard Pricing
Two subscriptions have prices that don't match `retail_price * 0.90`:
- Sub 9507: Cloud 9 3lb at $44.55 (should be $58.32 at 10% off, or $64.80 retail)
- Sub 9466: Cloud 9 12oz at $13.49 (should be $16.20 at 10% off, or $18.00 retail)

**Options:**
1. **Grandfather via metadata** — Store `override_price` in subscription metadata, have renewal service check it before applying plan discount
2. **Honor at migration, normalize later** — Import at WC price, contact customers to explain price normalization after migration
3. **Custom plan** — Create a 25% or 31% discount plan just for these two subs

**Recommendation:** Option 2. Contact these 2 customers before migration. The prices appear to be legacy/grandfathered rates. Going forward, all subscribers get the standard plan discount.

#### Shipping Method Awareness
WooCommerce subscriptions carry their shipping method (Free Local Delivery, Flat Rate, Pickup, etc.). Hiri subscriptions currently only store `shipping_address_id` — they don't know *how* to ship.

**What's needed:**
- The shipping method for subscription renewals should be determined by the shipping address and order value (same rules as one-time orders)
- 69% of subscriptions are local delivery (Tri-Cities area) — Hiri needs local delivery zone logic
- 5lb+ orders ship free; under 5lb is $6 flat rate

**Recommendation:** Shipping calculation at renewal time should use the same rules as checkout. No subscription-level shipping method field needed — just ensure the shipping calculator handles all cases:
- Local delivery zone (free or $5 donation based on admin config)
- Local pickup (free)
- Standard shipping: free for 5lb+, $6 flat rate otherwise

---

## 3. Data to Migrate

### 3a. Customers (only those with active/on-hold subscriptions)

**Source:** Subscription billing data (NOT the customer endpoint — it's 99% spam).

| WC Field (from subscription) | Hiri Field |
|-------------------------------|------------|
| `billing.email` | `email` |
| `billing.first_name` | `first_name` |
| `billing.last_name` | `last_name` |
| `billing.phone` | `phone` |
| `is_paying_customer` = true | `is_guest` = false |
| `meta_data._stripe_customer_id` | `metadata.stripe_customer_id` |

**Dedup by email.** Multiple subscriptions may reference the same customer.

**Estimated count:** ~66 active + ~30–40 recent on-hold = ~80–100 unique customers.

### 3b. Addresses

| WC Field | Hiri Field |
|----------|------------|
| `shipping.first_name` | `first_name` |
| `shipping.last_name` | `last_name` |
| `shipping.company` | `company` |
| `shipping.address_1` | `line1` |
| `shipping.address_2` | `line2` |
| `shipping.city` | `city` |
| `shipping.state` | `state` |
| `shipping.postcode` | `postal_code` |
| `shipping.country` | `country_code` |

Import both billing and shipping addresses. Set the first as `is_default = true`.

### 3c. Products & Variants

Products should be **manually created** in Hiri admin:
- Only 9 coffee products (8 variable + 1 simple)
- Pricing model differs (PriceSet/Price vs flat variation price)
- Descriptions/images need refresh for new storefront
- Mark all coffee products as `subscribable = true`

**Variant mapping table** (create after manual product setup):
```
wc_variation_id → hiri_variant_id
242  (Cloud 9 12oz WB)   → <uuid>
243  (Cloud 9 12oz Drip)  → <uuid>
244  (Cloud 9 3lb WB)     → <uuid>
245  (Cloud 9 5lb WB)     → <uuid>
7621 (2-Stroke 12oz)      → <uuid>
7622 (2-Stroke 3lb)       → <uuid>
5062 (Bike Blend 12oz)    → <uuid>
5063 (Bike Blend 3lb)     → <uuid>
5064 (Bike Blend 5lb)     → <uuid>
272  (Guatemala 12oz)     → <uuid>
273  (Guatemala 3lb)      → <uuid>
274  (Guatemala 5lb)      → <uuid>
283  (Ethiopia 12oz)      → <uuid>
284  (Ethiopia 3lb)       → <uuid>
285  (Ethiopia 5lb)       → <uuid>
294  (Chop Top 12oz)      → <uuid>
295  (Chop Top 3lb)       → <uuid>
296  (Chop Top 5lb)       → <uuid>
305  (Decaf 12oz)         → <uuid>
306  (Decaf 3lb)          → <uuid>
307  (Decaf 5lb)          → <uuid>
4432 (White Coffee 1lb)   → <uuid>
```

Note: Each WC variation encodes both weight AND grind. Hiri variants should do the same (each weight+grind combo is a distinct variant).

### 3d. Subscription Plans

| Hiri Plan Name | Interval | Discount % | Maps from WC |
|----------------|----------|------------|--------------|
| Every Week | `every_7_days` | 10 | `week/1` |
| Every 2 Weeks | `every_14_days` | 10 | `week/2` |
| Every 3 Weeks | `every_21_days` | 10 | `week/3` |
| Every Month | `every_30_days` | 10 | `month/1`, `week/4` |
| Every 2 Months | `every_60_days` | 10 | `month/2` |
| Every 3 Months | `every_90_days` | 10 | `month/3` |

All enum values already exist in `domain/subscription.go`.

### 3e. Subscriptions

| WC Field | Hiri Field |
|----------|------------|
| `customer_id` | `customer_id` (via email-based mapping) |
| `billing_period` + `billing_interval` | `plan_id` (via plan mapping) |
| line_items[0].`variation_id` | `variant_id` (via variant mapping) |
| line_items[0].`quantity` | `quantity` |
| `status` = "active" | `status` = `active` |
| `status` = "on-hold" | `status` = `paused` |
| `date_paid_gmt` | `current_period_start` |
| `next_payment_date_gmt` | `current_period_end` / `next_order_at` |
| `end_date_gmt` (if set) | `ends_at` |
| `shipping` address | `shipping_address_id` (via address mapping) |
| `date_created_gmt` | `created_at` |

**Special cases:**
- Sub 13117 (multi-item): Split into 2 Hiri subscriptions, one per line item
- Sub 9507 and 9466 (non-standard pricing): Handle per decision in section 2c
- Subs using legacy subscription products (3478, 3613, 6626): Map variation to the underlying coffee product variant

### 3f. Stripe Payment Method Migration

Every WC subscription carries:
- `_stripe_customer_id` (e.g., `cus_RfbwnWpHpp2bC8`)
- `_stripe_source_id` (e.g., `pm_1T8ua9EgyB3rE2FgVq50yzBO`)

Since both platforms use the **same Stripe account**, no re-tokenization needed:
1. Store `stripe_customer_id` on the Hiri Customer record
2. Store `stripe_payment_method_id` on the Hiri Subscription (or use the customer's default)
3. Renewals use `stripe.PaymentIntents.Create()` with `off_session: true`

**Pre-migration validation:** Call `stripe.PaymentMethods.Get()` for every `_stripe_source_id` to verify they're still valid. Flag any expired cards for proactive customer outreach.

---

## 4. Migration Strategy

### Phase 1 — Pre-Migration Setup

- [ ] Add `WC_CONSUMER_KEY`, `WC_CONSUMER_SECRET`, `WC_BASE_URL` to `.env` (see `.env.example`)
- [ ] Manually create all 9 coffee products in Hiri admin with correct variants and pricing
- [ ] Mark all coffee products as `subscribable = true`
- [ ] Create all 6 subscription plans (Every Week through Every 3 Months, all 10% discount)
- [ ] Build variant mapping JSON file (WC variation ID → Hiri variant UUID) — see section 3c for full list
- [ ] Set up shipping rules: local delivery zone, pickup, flat rate, free threshold
- [ ] Contact 2 non-standard-pricing customers (subs 9507 and 9466) about price normalization

### Phase 2 — Run Migration Script (DONE)

The CLI tool at `cmd/migrate/main.go` is implemented. Run with:

```bash
# Dry run — validate all mappings, report issues, create nothing
go run ./cmd/migrate --dry-run

# Dry run with variant mapping validation
go run ./cmd/migrate --dry-run --mapping=variant-mapping.json

# Migrate a single customer (test with yourself first)
go run ./cmd/migrate --mapping=variant-mapping.json --email=you@example.com

# Migrate a batch of customers from a file (one email per line)
go run ./cmd/migrate --mapping=variant-mapping.json --emails-file=batch1.txt

# Migrate all customers
go run ./cmd/migrate --mapping=variant-mapping.json

# Only import on-hold subs from last 90 days (default: 60)
go run ./cmd/migrate --mapping=variant-mapping.json --on-hold-days=90
```

**Flags:**
- `--dry-run` — validate mappings without importing
- `--mapping=file.json` — variant mapping file (required for import)
- `--email=user@example.com` — migrate only this customer's subscriptions
- `--emails-file=file.txt` — migrate only customers listed in file (one email per line, `#` comments supported)
- `--on-hold-days=60` — only import on-hold subs with next_payment_date within N days

**Recommended rollout:**
1. `--email=your@email.com` — migrate yourself, verify in admin
2. `--emails-file=batch1.txt` — migrate ~5 customers, monitor for a few days
3. No filter — migrate all remaining customers

**What it does:**
1. Fetches all active + recent on-hold subscriptions from WC API (paginated)
2. Filters by email if `--email` or `--emails-file` is set
3. For each subscription: finds/creates customer by email, creates address, creates subscription
4. Splits multi-item WC subs into separate Hiri subscriptions
5. Preserves WC metadata (sub ID, product name, shipping method, Stripe payment method)
6. Produces a report: customers created/found, subscriptions created/skipped, warnings, errors

**Requires:** `WC_CONSUMER_KEY`, `WC_CONSUMER_SECRET`, `DATABASE_URL` in `.env`

### Phase 3 — Cutover ("Drain and Switch")

1. **T-7 days:** Run migration in `--dry-run` mode, fix any mapping issues
2. **T-3 days:** Contact the 2 non-standard-pricing customers about price normalization
3. **T-1 day:** Run migration `--import`, creating all subscriptions in Hiri with `next_order_at` set to WC `next_payment_date_gmt`
4. **T-0 (cutover day):**
   - Put all WC active subscriptions on hold (bulk API: set active → on-hold)
   - Enable Hiri renewal scheduler (River periodic job)
   - Switch DNS to point to Hiri
5. **T+1 to T+7:** Monitor renewals — verify charges, correct amounts, correct products
6. **T+14:** Disable WC site entirely

**Why this works:**
- Each subscription has a known `next_payment_date_gmt` — we import that exact date
- WC is put on hold *before* any next payment fires
- Hiri picks up from exactly where WC left off
- No gap, no double-charge
- Weekly subs (6 active) are the tightest window — cutover should happen right after their latest renewal

### Phase 4 — Cleanup
1. Cancel all WC subscriptions (full cancel, not just hold)
2. Archive WC order history export (CSV) for reference
3. Remove migration CLI tool from codebase

---

## 5. Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Double-charge during cutover | Customer charged twice | Put WC subs on-hold before Hiri goes live; cutover window < 24 hours |
| Missing/expired Stripe payment method | First renewal fails | Pre-validate all `_stripe_source_id` via Stripe API; proactive customer outreach for expired cards |
| Customer creates new WC sub between migration and cutover | Sub not in Hiri | Freeze new WC subscription signups during cutover window |
| On-hold subs with stale next_payment dates | Immediate charge on reactivation | Only migrate on-hold subs with `next_payment_date_gmt` within last 60 days; set future `next_order_at` for imported paused subs |
| Product/variant mapping error | Wrong product shipped | Manual QA of all 68 active subs post-import; dry-run validation |
| Multi-item sub (13117) split incorrectly | Customer gets 2 separate orders | Verify both child subs share same plan and address so batching consolidates them |
| Non-standard pricing subs charged wrong amount | Revenue discrepancy | Handle manually — 2 subs total, contact customers beforehand |
| Local delivery subs ship via USPS | Wrong fulfillment method | Ensure shipping calculator correctly identifies local delivery zone addresses |
| 4-week sub mapped to 30-day plan | 2-day drift per month | Acceptable — inform the one affected customer |
| Weekly subs renew during cutover gap | Missed renewal | Time cutover right after weekly renewal day |

---

## 6. What NOT to Migrate

- **Order history** — Historical orders stay in WC. Hiri starts clean.
- **Cancelled subscriptions (190)** — Dead records, no value.
- **Expired subscriptions (33)** — Already terminal.
- **Old on-hold subscriptions** — On-hold with `next_payment_date` > 60 days ago = abandoned.
- **Non-subscriber customers** — They'll create new accounts on Hiri when they next order.
- **WC customer passwords** — Customers set new passwords via Hiri password reset.
- **Cart/session data** — Ephemeral.
- **WC "variable-subscription" products** (3478, 3613, 6626) — These are WC-specific product types. The underlying subscriptions reference real coffee product variations which will be mapped.
- **Non-coffee products** — Mugs, shirts, grinders, chocolates, pour-overs. Recreate manually in Hiri when needed for the storefront.

---

## 7. Resolved Decisions

1. **Discount percentage** — Retail 12oz = $18.00, subscription 12oz = $16.20 → **10% subscriber discount**. All plans use `discount_pct: 10`.

2. **On-hold subscriptions** — Only migrate on-hold subs where `next_payment_date_gmt` is within the last 60 days. Older ones are abandoned.

3. **Shipping methods** — No subscription-level shipping method field. Shipping is calculated at renewal time using the same rules as checkout (local zone, pickup, flat rate, free threshold).

4. **The "12oz Bag of Coffee" product (WC ID 9338)** — Eliminate. Each blend is already its own product with a 12oz variant.

5. **Fixed-term subscriptions** — `ends_at` field already implemented. WC `end_date_gmt` maps directly. 9 active subs have end dates.

6. **Multi-item subscription (13117)** — Split into 2 Hiri subscriptions on the same plan. Batched renewals will consolidate them into one order.

7. **Non-standard pricing (9507, 9466)** — Contact customers before migration. Normalize to standard 10% discount pricing post-migration.

8. **4-week interval (1 sub)** — Map to `every_30_days`. Acceptable 2-day drift.

9. **Quantity > 1 subscriptions (6 subs)** — Hiri's `subscription.quantity` field (1–10) handles this directly. No special handling needed.

10. **Legacy subscription products** — WC variation IDs in subs from products 3478, 3613, 6626 will be mapped to the corresponding standard coffee product variants in Hiri.

---

## 8. Post-Migration Feature Priorities

Features needed after migration to deliver a complete subscriber experience:

| Feature | Priority | Notes |
|---------|----------|-------|
| Subscription admin pages | P0 | Staff must manage subs from day 1 |
| Email notifications (renewal, failed payment, expiring) | P0 | Customers expect these |
| Customer self-service portal (view/pause/cancel) | P1 | Reduces support burden |
| Subscription checkout flow (new signups) | P1 | Can't grow subscriber base without it |
| Product swap on active subscription | P2 | "I want Cloud 9 instead of 2-Stroke" |
| Frequency change on active subscription | P2 | "Switch from monthly to every 2 weeks" |
| Skip next delivery | P2 | One-period pause then auto-resume |
| Failed payment retry emails | P2 | Dunning flow to recover past_due subs |

---

## Appendix A: Active Subscription Detail

68 active subscriptions as of 2026-03-11. Re-query with:

```bash
curl -s "${WC_BASE_URL}/wp-json/wc/v1/subscriptions?\
consumer_key=${WC_CONSUMER_KEY}&\
consumer_secret=${WC_CONSUMER_SECRET}&\
per_page=100&status=active"
```

## Appendix B: WC Shipping Methods → Hiri Shipping Rules

| WC Shipping Method | Count | Hiri Rule |
|--------------------|-------|-----------|
| Free Local Delivery - No Contact | 27 | Address in local delivery zone → free delivery |
| Free shipping | 13 | Order total >= 5lbs → free USPS/UPS |
| Donation for Local Delivery Costs | 10 | Address in local delivery zone → $5 delivery fee |
| Flat Rate | 8 | Under 5lbs, outside local zone → $6 flat rate |
| Local Pickup at 101 West Kennewick Ave. | 7 | Pickup option (always free) |
| Local Delivery | 2 | Address in local delivery zone → free delivery |
| Local Delivery - No Contact | 1 | Address in local delivery zone → free delivery |
