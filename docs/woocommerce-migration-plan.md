# WooCommerce to Hiri — Migration Plan

> **Scope:** Migrate ~80 customer records with active subscriptions from WooCommerce to Hiri.
> **Source:** WooCommerce REST API v3 + WooCommerce Subscriptions plugin
> **Payment Gateway:** Stripe only (both platforms)

---

## 1. Current State (WooCommerce)

### Subscription Counts
| Status | Count |
|--------|-------|
| Active | 21 |
| On-Hold | 25 |
| Pending Cancel | 3 |

### Billing Intervals in Active Subscriptions
| WC `billing_period/interval` | Count | Hiri Equivalent |
|------------------------------|-------|-----------------|
| `week/1` (weekly) | 4 | **needs new plan** `every_7_days` |
| `week/2` (every 2 weeks) | 2 | `every_14_days` |
| `week/3` (every 3 weeks) | 2 | `every_21_days` |
| `month/1` (monthly) | 12 | `every_30_days` |
| `month/3` (every 3 months) | 1 | **needs new plan** `every_90_days` |

### Products in Active Subscriptions
| WC Product ID | Name | Variation IDs Used | Sub Count |
|---------------|------|--------------------|-----------|
| 229 | Cloud 9 Espresso | 242 (12oz), 244 (3lb), 245 (5lb) | 14 |
| 7620 | 2-Stroke Blend | 7621 (12oz), 7622 (3lb) | 2 |
| 304 | Cascadia Decaf | 305 (12oz), 307 (3lb) | 2 |
| 5061 | Bike Blend | 5064 (5lb) | 1 |
| — | Other products | — | 2 |

### Coffee Catalog (9 products)
| WC ID | Name | Type | Attributes |
|-------|------|------|------------|
| 229 | Cloud 9 Espresso | variable | Weight (12oz/3lb/5lb), Grind (Whole Bean/Drip) |
| 7620 | 2-Stroke Blend | variable | Weight (12oz/3lb/5lb), Grind |
| 5061 | Bike Blend | variable | Weight (12oz/3lb/5lb), Grind |
| 4431 | White Coffee | variable | Weight (1lb/2lb/5lb) |
| 304 | Cascadia Decaf | variable | Weight (12oz/3lb/5lb), Grind |
| 9338 | 12oz Bag of Coffee | variable | Coffee (7 blends), Grind |
| 6532 | Sample Pack | simple | Grind |
| 3613 | Blended Origin - Subscription | variable-subscription | Blend, Weight, Grind, Frequency |
| 3478 | Single Origin - Subscription | variable-subscription | Origin, Weight, Grind, Frequency |
| 6626 | Bigger Bolder Sample Pack (12oz) | variable-subscription | Grind, Decaf Alternative, Frequency |

---

## 2. Data to Migrate

### 2a. Customers (only those with active/on-hold subscriptions)

**Source fields → Hiri `Customer` fields:**
| WC Field (from subscription billing) | Hiri Field |
|---------------------------------------|------------|
| `billing.email` | `email` |
| `billing.first_name` | `first_name` |
| `billing.last_name` | `last_name` |
| `billing.phone` | `phone` |
| `is_paying_customer` = true | `is_guest` = false |
| `meta_data._stripe_customer_id` | store in `metadata.stripe_customer_id` |

**Note:** WC customer list endpoint returns mostly spam/bot accounts with empty profiles. Real customer data lives on the subscription records themselves (billing/shipping addresses are populated there). Import customers from subscription data, not the customer endpoint.

### 2b. Addresses

**Source fields → Hiri `Address` fields:**
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
| (first address imported) | `is_default` = true |

Both billing and shipping addresses should be imported. Many customers have the same address for both.

### 2c. Products & Variants

Products should be **manually created** in the Hiri admin, not auto-migrated, because:
- There are only 9 coffee products
- Hiri's catalog model is cleaner (Product → ProductOption → ProductOptionValue → Variant)
- Pricing model is different (PriceSet/Price vs flat price on variation)
- Product descriptions/images need updating for the new storefront anyway

**What we need:** A mapping table from WC variation IDs → Hiri variant UUIDs, created after manual product setup:

```
wc_variation_id → hiri_variant_id
242             → <cloud9-12oz-uuid>
244             → <cloud9-3lb-uuid>
245             → <cloud9-5lb-uuid>
7621            → <2stroke-12oz-uuid>
7622            → <2stroke-3lb-uuid>
...
```

### 2d. Subscription Plans

Create these plans in Hiri before importing subscriptions:

| Hiri Plan Name | Interval | Discount % | Maps from WC |
|----------------|----------|------------|--------------|
| Every Week | `every_7_days` | 10 | `week/1` |
| Every 2 Weeks | `every_14_days` | 10 | `week/2` |
| Every 3 Weeks | `every_21_days` | 10 | `week/3` |
| Every Month | `every_30_days` | 10 | `month/1` |
| Every 3 Months | `every_90_days` | 10 | `month/3` |

**Action required:** Add `every_7_days` and `every_90_days` to the `SubscriptionPlan.interval` enum in `domain/subscription.go`.

### 2e. Subscriptions

**Source fields → Hiri `Subscription` fields:**
| WC Field | Hiri Field |
|----------|------------|
| `customer_id` | `customer_id` (via mapping) |
| `billing_period` + `billing_interval` | `plan_id` (via plan mapping above) |
| line_items[0].`variation_id` | `variant_id` (via variant mapping) |
| `status` = "active" | `status` = `active` |
| `status` = "on-hold" | `status` = `paused` |
| `last_payment_date_gmt` | `current_period_start` |
| `next_payment_date_gmt` | `current_period_end` / `next_order_at` |
| `end_date_gmt` (if set) | track in `metadata.fixed_term_end` |
| `shipping` address | `shipping_address_id` (via address mapping) |
| `date_created_gmt` | `created_at` |

### 2f. Stripe Payment Method Migration

Every subscription carries:
- `_stripe_customer_id` (e.g., `cus_RfbwnWpHpp2bC8`)
- `_stripe_source_id` (e.g., `pm_1T8ua9EgyB3rE2FgVq50yzBO`)

Since both WooCommerce and Hiri use the same Stripe account, we can reference these directly:
1. Store `stripe_customer_id` on the Hiri Customer record
2. Store `stripe_payment_method_id` on the Hiri Subscription record
3. When the renewal job runs, charge using `stripe.PaymentIntents.Create()` with the existing customer + payment method

**Issue:** One subscription (ID 13458) has an empty `_stripe_customer_id`. Needs manual investigation.

---

## 3. Migration Strategy

### Phase 1 — Pre-Migration Setup
1. Add `every_7_days` and `every_90_days` to SubscriptionPlan interval enum
2. Manually create all coffee products in Hiri admin with correct variants/pricing
3. Create subscription plans in Hiri
4. Build the WC→Hiri ID mapping tables (variants, plans)
5. Add `stripe_customer_id` field to Customer domain (or use metadata)
6. Add `stripe_payment_method_id` field to Subscription domain (or use metadata)

### Phase 2 — Write Migration Script
A CLI tool (`cmd/migrate/main.go`) that:
1. Reads all active + on-hold subscriptions from WC API
2. For each subscription:
   a. Creates or finds Customer by email (dedup)
   b. Creates billing + shipping Addresses
   c. Creates Subscription with correct plan, variant, and schedule
   d. Records audit trail entries
3. Produces a report: imported count, skipped count, errors

### Phase 3 — Cutover
This is the critical part — we must avoid double-charging subscribers.

**Recommended approach: "Drain and Switch"**

1. **T-7 days:** Run migration script in dry-run mode, verify all mappings
2. **T-1 day:** Run migration script for real, importing all subscriptions into Hiri with `next_order_at` set to their WC `next_payment_date_gmt`
3. **T-0 (cutover day):**
   - Put WC subscriptions on hold (bulk update via API: set all active → on-hold)
   - Enable Hiri subscription renewal job
   - Switch DNS to point to Hiri
4. **T+1 to T+7:** Monitor — verify renewals are charging correctly
5. **T+14:** Disable WC site entirely

**Why drain-and-switch works:**
- Each subscription has a known `next_payment_date_gmt` — we import that exact date
- WC is put on hold *before* any next payment fires
- Hiri picks up from exactly where WC left off
- No gap, no double-charge

### Phase 4 — Cleanup
1. Cancel all WC subscriptions (not just on-hold — fully cancel)
2. Archive WC order history export (CSV) for reference
3. Remove migration CLI tool from codebase

---

## 4. Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Double-charge during cutover | Customer charged twice | Put WC subs on-hold before Hiri goes live |
| Missing Stripe payment method | Renewal fails | Pre-validate all `_stripe_source_id` values via Stripe API |
| Customer creates new WC sub between migration and cutover | Sub not in Hiri | Keep WC in read-only mode during cutover window |
| On-hold subs with old next_payment dates | Immediate charge on reactivation | Set `next_order_at` to future date for on-hold imports |
| Product/variant mapping error | Wrong product shipped | Manual QA of all 21 active subs post-import |

---

## 5. What NOT to Migrate

- **Order history** — Not worth the complexity. Historical orders stay in WC. Hiri starts clean.
- **Cancelled subscriptions** — No value in importing dead records.
- **Non-subscriber customers** — They'll create new accounts on Hiri when they next order.
- **WC customer passwords** — Customers will need to set new passwords on Hiri (password reset flow on first login).
- **Cart/session data** — Ephemeral, not worth migrating.

---

## 6. Resolved Decisions

1. **Discount percentage** — Retail 12oz = $18.00, subscription 12oz = $16.20 → **10% subscriber discount**. Hiri plans will use `discount_pct: 10`.

2. **On-hold subscriptions** — Only migrate on-hold subs where `next_payment_date_gmt` is within the last 60 days. Older ones are abandoned and not worth importing.

3. **Shipping methods** — Shipping rules will be restructured for Hiri. WC shipping method info (Free Local Delivery, $5 Donation, Free Shipping for 5lb+) will be preserved as metadata on migrated subscriptions for future reference.

4. **The "12oz Bag of Coffee" product (WC ID 9338)** — Eliminate it. Each blend is already its own product with a 12oz variant, making this redundant.

5. **Fixed-term subscriptions** — Added `ends_at *time.Time` field to the Subscription domain type. WC `end_date_gmt` maps directly to this field. Null = open-ended.
