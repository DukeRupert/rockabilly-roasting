# OrderSpace to Hiri — Wholesale Migration Plan

> **Scope:** Migrate 50 wholesale customer accounts, 845 historical orders, and active invoicing from OrderSpace into Hiri.
> **Source:** OrderSpace API v1 (OAuth2 client credentials)
> **Context:** OrderSpace handles B2B/wholesale only. Retail subscriptions are migrating separately from WooCommerce (see `woocommerce-migration-plan.md`).

---

## 1. Current State (OrderSpace)

### Data Volume Summary
| Entity | Count | Notes |
|--------|-------|-------|
| Customers | 50 | 48 active, 1 new, 1 closed |
| Products | 10 | 8 standard coffees + 1 retail 12oz bags + 1 white-label |
| Product Variants | 82 | SKU pattern: `{code}-{size}-{grind}` |
| Orders | 845 | 825 fulfilled, 17 cancelled, 2 invoiced, 1 new |
| Invoices | 200+ (paginated) | 139 paid, 61+ unpaid in first page |
| Categories | 2 | "Featured Products", "The Bunker white-label" |
| Customer Groups | 2 | "Main Products", "The Bunker Tactical" |
| Price Lists | 3 | "2024 Wholesale Price", "2025", "2026" |
| Payment Terms | 4 | Immediate, 15 days, 21 days, 30 days |
| Tax Rates | 0 | No tax configured (wholesale exempt) |
| Inventory | 0 | Empty — not tracked. All variants backorder=true |

### Order History
- **Date range:** 2024-10-14 to 2026-03-10 (~17 months)
- **Total gross revenue:** $292,496.50 (non-cancelled)
- **Currency:** USD only
- **Fulfillment:** 72% Store Pickup, 27% Free Delivery, 1% Free Shipping

### Monthly Revenue Trend
Revenue ramped from ~$1,000/mo in Oct 2024 to steady ~$25,000-$30,000/mo by late 2025:

| Month | Orders | Revenue |
|-------|--------|---------|
| 2024-10 | 3 | $1,062 |
| 2024-11 | 1 | $525 |
| 2025-02 | 8 | $2,933 |
| 2025-03 | 57 | $14,992 |
| 2025-04 | 62 | $15,122 |
| 2025-05 | 66 | $16,756 |
| 2025-06 | 62 | $16,587 |
| 2025-07 | 56 | $17,626 |
| 2025-08 | 66 | $25,504 |
| 2025-09 | 62 | $24,651 |
| 2025-10 | 67 | $25,554 |
| 2025-11 | 71 | $28,194 |
| 2025-12 | 72 | $25,797 |
| 2026-01 | 75 | $29,525 |
| 2026-02 | 72 | $29,507 |
| 2026-03 (partial) | 28 | $9,969 |

No seasonal dip. Step-up in Aug 2025 when Charis Coffee and Sip&Co. came onboard.

### Top 10 Customers by Revenue
| Company | Orders | Revenue | Avg Order | Frequency |
|---------|--------|---------|-----------|-----------|
| MOCHA EXPRESS TRICITIES | 92 | $76,514 | $797 | Every 4 days |
| Charis Coffee Company | 31 | $31,367 | $1,012 | Every 7 days |
| Cafe Magnolia | 59 | $20,933 | $355 | Every 6 days |
| Sip&Co. | 24 | $20,367 | $849 | Every 10 days |
| Steam and cream | 28 | $15,920 | $569 | Every 12 days |
| Calvary Chapel Of Tri-Cities | 44 | $14,145 | $321 | Every 8 days |
| Cafenated | 47 | $13,250 | $282 | Every 8 days |
| Angel Brook Farm | 36 | $11,953 | $332 | Every 10 days |
| Kaffrin's LLC | 17 | $11,243 | $661 | Every 20 days |
| The Village Bistro | 35 | $9,895 | $260 | Every 10 days |

### Customer Order Patterns (key accounts)
- **MOCHA EXPRESS**: Almost exclusively Cloud 9 5LB WB (1,277 units!), plus White Coffee 5LB EG and Cascadia Decaf 1LB EG
- **Charis Coffee**: Cloud 9 5LB WB (398) plus heavy retail 12oz bag purchases
- **Cafe Magnolia**: Cloud 9 5LB WB (352), small amounts of Cascadia Decaf
- **Sip&Co.**: 2 Stroke 5LB WB (314) — the primary 2 Stroke buyer, almost no Cloud 9
- **Calvary Chapel**: Mix of 2 Stroke 5LB DG (112), Cloud 9 5LB WB (107), plus retail 12oz bags (117)

### Top SKUs by Quantity
| SKU | Product | Qty | Revenue |
|-----|---------|-----|---------|
| 0003-5LB-WB | Cloud 9, 5lb Whole Bean | 3,642 | $206,178 |
| 0009-C9 | Retail 12oz Cloud 9 | 493 | $5,506 |
| 0007-5LB-WB | 2 Stroke, 5lb Whole Bean | 321 | $18,458 |
| 0005-5LB-EG | White Coffee, 5lb Espresso Ground | 170 | $9,290 |
| 0003-5LB-DG | Cloud 9, 5lb Drip Ground | 149 | $8,398 |
| 0007-5LB-DG | 2 Stroke, 5lb Drip Ground | 142 | $8,145 |
| 0009-ETH | Retail 12oz Ethiopia | 142 | $1,547 |
| 0009-BB | Retail 12oz Bike Blend | 133 | $1,448 |

**26 of 82 SKUs have never been ordered** — mostly small-size non-whole-bean combinations.

### Pricing Tiers (3 price lists)
| Price List | 5lb Price | 3lb Price | 1lb Price | 12oz Price |
|------------|-----------|-----------|-----------|------------|
| Base (internal) | $47.50 | $28.50 | $9.50 | $8.00 |
| 2024 Wholesale (pl_v1xq3yj0) | $52.50 | $31.50 | $10.50 | $9.00 |
| 2025 (pl_q1m82yl5) | $57.50 | $34.50 | $11.50 | $11.00 |
| 2026 (pl_yjg926l9) | $62.50 | $37.50 | $12.50 | $11.50 |

Pricing increments consistently at $1/lb per year across all sizes.

### Price List Assignment
| Price List | Count | Notes |
|-----------|-------|-------|
| 2025 (pl_q1m82yl5) | 46 | Standard wholesale rate |
| 2024 (pl_v1xq3yj0) | 1 | **MOCHA EXPRESS** — grandfathered at 2024 rate |
| 2026 (pl_yjg926l9) | 1 | **Panadería Rodriguez** — newest substantive customer |
| None | 2 | Hanford High School, Stacks Mobile Bistro (zero-order customers) |

**Pricing anomaly:** MOCHA EXPRESS is assigned the 2024 price list but is actually being charged the *base* price ($47.50/5lb, not $52.50). They're getting even cheaper pricing than their assigned list. This appears intentional given they're the highest-volume customer by far.

### Customer Groups
| Group | Customers | Purpose |
|-------|-----------|---------|
| Main Products (cg_j9ywxr5g) | 45 | Standard wholesale access |
| The Bunker Tactical (cg_w5llg051) | 1 | Bunker Uniforms — white-label customer |
| (none) | 4 | Hanford High School, Panadería Rodriguez, Just Juice, Kool Beanz Koffee |

### Payment Terms
| Terms | Customers | Hiri Mapping |
|-------|-----------|--------------|
| 30 Days (pt_wepp165e) | 42 | Invoice due_date = order_date + 30 |
| 15 Days (pt_ynw1k75m) | 2 | The coffee pot seattle, Firefly Software |
| 21 Days (pt_dmrkvqwe) | 1 | Novel Coffee |
| None | 5 | Hanford High School, Stacks Mobile Bistro, Byte Brew, Bunker Uniforms, Kool Beanz |

### Shipping Types
| Type | Orders | Notes |
|------|--------|-------|
| Store Pickup | 608 (72%) | Local customers pick up at roastery |
| Free Delivery | 228 (27%) | Owner delivers in Tri-Cities area |
| Free Shipping | 9 (1%) | Walla Walla Tattoo Co (7), Sip&Co. (1), coffee pot seattle (1) |

### Customers with Zero Orders (6)
| Customer | Status | Created | Notes |
|----------|--------|---------|-------|
| Hanford High School | active | 2026-01-23 | No group, no price list, no payment terms |
| Stacks Mobile Bistro | new | recent | Never activated |
| Byte Brew | active | recent | No payment terms |
| Just Juice | active | recent | No group |
| Bunker Uniforms and Equipment | active | recent | White-label group, no payment terms |
| The B Spot | active | recent | Has orders but 0 visible in data |

### Churn Risk (>60 days since last order)
| Customer | Orders | Last Order | Days Ago |
|----------|--------|------------|----------|
| Firefly Software | 1 | 2024-10-15 | 511 (dev/test account) |
| Kagen's Coffee | 3 | 2024-11-04 | 491 |
| Kool Beanz Koffee | 2 | 2025-03-28 | 347 (status: closed) |
| Tina's Tasty Treats | 1 | 2025-07-17 | 236 |
| Foodies | 3 | 2025-06-29 | 254 |
| PKA Holdings (The Last Resort) | 2 | 2025-09-19 | 172 |
| JayDay Cafe & Boba | 3 | 2025-09-20 | 171 |
| Fresh Picks | 6 | 2025-11-13 | 117 |
| Richland High School "Bomb Shelter" | 1 | 2025-12-11 | 89 |

### Invoice Analysis
- Invoices have a 1:1 relationship with orders
- **Payment methods used:** Bank transfer (45), Credit card (41), QuickBooks (21), Check (11), Cash (4), specific card types (Amex/MC/Visa)
- **Average payment delay:** 16.5 days (median 10 days)
- **Overdue invoices (>30 days):** ~14 totaling ~$4,499
  - The Village Bistro: 3 invoices, $1,526
  - Charis Coffee Company: 3 invoices, $2,328
  - The coffee pot seattle: 1 invoice, $403
  - Kaffrin's LLC: 2 invoices, $1,715
- **Invoice numbering inconsistency:** Mix of 4-digit (1163-1218), 6-digit (123313-123477), and prefixed ("INV1748") formats

### Order Metadata Usage
| Field | Usage | Examples |
|-------|-------|---------|
| customer_note | 15.6% of orders (132) | Pickup instructions, grind requests, delivery routing |
| delivery_date | 30% of orders (253) | Scheduling |
| customer_po_number | 10 orders | Mostly Kennewick School District PO numbers |
| internal_note | Never used | |
| reference | Never used | |

### Cancelled Orders (17)
- MOCHA EXPRESS: 4 (including $1,482 order)
- The Village Bistro: 3 (order corrections — cancel + reorder)
- Firefly Software: 2 (test orders from Oct 2024)
- Fresh Picks: 2
- Others: 1 each (coffee pot seattle, New Vintage Church, Port of Benton, Healthy Vibes, Kagen's Coffee, Kool Beanz)

### Stuck Orders (2 from early days)
- **Order #8** — Kagen's Coffee, 2024-11-04, $525.00, status "invoiced" since Nov 2024
- **Order #6** — Kagen's Coffee, 2024-10-18, $577.50, status "invoiced" since Oct 2024

These need manual resolution before migration.

---

## 2. Data Quality Issues

### Critical — Must Fix Before Migration

| Customer | Issue | Fix |
|----------|-------|-----|
| Steam and cream | postal_code = "Washington " (not a zip code) | Correct to actual zip — Grandview, WA = 98930 |
| Richland Baptist Church (addr 2) | line2="Richland, WA 99354-5700", city="Richland, WA", state="WA" — jumbled | Restructure: line2="", city="Richland", postal_code="99354" |
| Healthy Vibes | line1="808  dalton St", line2="808 Dalton St, Richland" — duplicated | Remove line2, clean line1 |
| Faith Tri-Cities | phone "50995475773" — 11 digits (extra '9') | Correct to 10 digits |
| Novel Coffee | postal_code "99301" (Pasco) for Richland address on George Washington Way | Verify — should be 99352 or 99354 |
| Cafe Magnolia | email domain "thexafemagnolia.com" | Verify — likely "thecafemagnolia.com" |

### Moderate — Clean During Migration

| Issue | Affected | Action |
|-------|----------|--------|
| Mixed-case email addresses | 12 customers | Normalize to lowercase |
| Inconsistent phone formatting | 13 customers | Strip to digits, format as (XXX) XXX-XXXX |
| Trailing/leading spaces in address fields | ~14 customers | Trim whitespace |
| Amendment XXI | dispatches email is empty string | Use orders email as fallback |
| Yellow Cafe | buyer name "Zimri Barker" but emails maria@/info@ | Verify correct contact |
| Kagen's Coffee | All emails route to talia@rockabillyroasting.com | Internal/test account? |
| Firefly Software | Address company="Kagen Coffee & Crepes", internal note="The Big Kahuna" | Dev/test account — exclude from migration? |
| "Nucleur Fallout" | White-label variant name typo | Correct to "Nuclear Fallout" |
| Foodies (addr 2) | Same physical address as addr 1, different casing | Dedup to single address |
| Mixed invoice numbering | 4-digit, 6-digit, "INV" prefix | Normalize during import |

### Tax Number Formats (10 customers)
Mixed formats need normalization:
- WA state UBI: `A` + 8 digits (6 customers)
- Federal EIN: `XX-XXXXXXX` (3 customers: Cafenated, Jae's Coffee, Richland Baptist)
- Unknown: 882012023 (Cafe Magnolia — 9 digits)

---

## 3. Data to Migrate

### 3a. Customers → Hiri Wholesale Customers

All 50 OrderSpace customers are wholesale accounts. Map to Hiri `Customer` with `account_type = wholesale`.

| OrderSpace Field | Hiri Field | Notes |
|------------------|------------|-------|
| `buyers[0].email_address` | `email` | Normalize lowercase, trim |
| `buyers[0].name` | `first_name` + `last_name` | Split on last space. Some have single names ("Adrian", "Baily") — use as first_name, leave last_name empty |
| `phone` | `phone` | Normalize format |
| `company_name` | `company_name` | Direct map |
| `status` = "active" | `wholesale_status` = `approved` | |
| `status` = "new" | `wholesale_status` = `pending` | |
| `status` = "closed" | `wholesale_status` = `suspended` | |
| `internal_note` | `wholesale_notes` | Only 1 customer has this |
| `id` | `metadata.orderspace_id` | For cross-reference and idempotent re-runs |
| `created_at` | `created_at` | Preserve original date |
| `customer_group_id` | `customer_group_id` | Via group mapping |
| `price_list_id` | `metadata.price_list` | See pricing section |
| `payment_terms_id` | `metadata.payment_terms_days` | Store as integer (15/21/30) |
| `tax_number` | `metadata.tax_number` | Mixed formats, store as-is |
| `email_addresses.orders` | `metadata.notification_emails.orders` | OS has per-type emails; Hiri has one |
| `email_addresses.dispatches` | `metadata.notification_emails.dispatches` | |
| `email_addresses.invoices` | `metadata.notification_emails.invoices` | |
| `reference` | `metadata.orderspace_reference` | Only 1 customer has this |

**Special handling:**
- `account_type` = `wholesale` for all records
- Passwords not in OrderSpace — customers use password reset on first Hiri login
- Deduplication: Check for email collisions with WooCommerce retail customers. If a customer exists in both systems, merge into single account with `account_type = wholesale`
- **Exclude Firefly Software** — dev/test account (address is "Kagen Coffee & Crepes", note says "The Big Kahuna")
- **Exclude Kagen's Coffee** — same entity as Firefly Software, internal/test account, emails route to staff
- **Exclude Kool Beanz Koffee** — status "closed", no activity since March 2025

### 3b. Addresses

| OrderSpace Field | Hiri Field | Notes |
|------------------|------------|-------|
| `addresses[].company_name` | `company` | Direct map |
| `addresses[].contact_name` | `first_name` + `last_name` | Split on last space |
| `addresses[].line1` | `line1` | Trim whitespace |
| `addresses[].line2` | `line2` | Trim, clean duplicates |
| `addresses[].city` | `city` | Trim whitespace |
| `addresses[].state` | `state` | Direct map |
| `addresses[].postal_code` | `postal_code` | Validate, fix known issues |
| `addresses[].country` | `country_code` | Direct map |
| (first address) | `is_default` = true | |

**Multi-address customers:**
- MOCHA EXPRESS TRICITIES: 4 addresses (home base + 3 stand locations)
- Foodies: 2 addresses (dedup to 1 — same physical location)
- Richland Baptist Church: 2 addresses (second one needs data cleanup)

### 3c. Products & Variants

Products should be **manually created** in Hiri admin (same approach as WooCommerce migration):
- Only 10 products, 82 variants
- Hiri's catalog model is richer (Product → ProductOption → ProductOptionValue → Variant)
- Pricing needs restructuring for Hiri's PriceSet/Price model
- Product images need downloading from CloudFront and re-uploading to R2
- All product image URLs are on `d3uzb2xkdr3e0f.cloudfront.net/rockabillyroasting/images/products/`

**Product catalog:**

| OS Code | Product Name | Options | Variants | Category | Images |
|---------|-------------|---------|----------|----------|--------|
| 0001 | Ethiopia | Size (5lb/3lb/1lb) × Grind (WB/DG/EG) | 9 | Featured Products | 1 |
| 0002 | Chop Top | Size × Grind | 9 | Featured Products | 1 |
| 0003 | Cloud 9 | Size × Grind | 9 | Featured Products | 1 |
| 0004 | Guatemala Tikal | Size × Grind | 9 | Featured Products | 1 |
| 0005 | White Coffee | Size × Grind | 9 | Featured Products | 1 |
| 0006 | Cascadia Decaf | Size × Grind | 9 | Featured Products | 1 |
| 0007 | 2 Stroke | Size × Grind | 9 | Featured Products | 1 |
| 0008 | Bike Blend | Size × Grind | 9 | Featured Products | 1 |
| 0009 | Retail 12oz Bags | Type (7 coffee varieties) | 7 | Featured Products | 7 |
| 0010 | The Bunker White-label | Label (Tactical Fuel / Nuclear Fallout / Kaboom Brew) | 3 | The Bunker white-label | 1 |

**SKU convention:** Preserve existing pattern (`{code}-{size}-{grind}`) in Hiri variants.

**Product visibility:**
- Products 0001-0008: `visibility = wholesale`
- Product 0009 (Retail 12oz): `visibility = public` (also sold on retail storefront)
- Product 0010 (Bunker White-label): `visibility = restricted` (The Bunker group only)

**Data gaps in product data:**
- **All weights are 0.0 kg** — needs manual entry for shipping calculations
- **All barcodes are empty** — no UPC/EAN data
- **All RRP (retail price) is 0.0** — not used in OrderSpace
- **All minimum/multiple are null** — no MOQ enforced
- **"Nucleur Fallout"** typo in white-label variant — correct to "Nuclear Fallout"

**Legacy SKUs in order history (3):**
- `0002-5LB`, `0003-3LB`, `0001-5LB` — from first test order (Oct 2024, Firefly Software) before grind was added to SKU scheme. No impact on migration since we're not importing order history.

### 3d. Customer Groups

| OrderSpace Group | Hiri CustomerGroup | Members |
|------------------|-------------------|---------|
| Main Products (cg_j9ywxr5g) | "Wholesale" | 45 customers |
| The Bunker Tactical (cg_w5llg051) | "The Bunker" | 1 customer (Bunker Uniforms) |
| (none) | "Wholesale" | 3 remaining customers (Hanford High School, Panadería Rodriguez, Just Juice) |
| MOCHA EXPRESS (special) | "MOCHA EXPRESS" | 1 customer — dedicated group for grandfathered pricing |

### 3e. Pricing

OrderSpace assigns price lists per-customer. Hiri prices are scoped via CustomerGroup or PriceList.

**Current pricing structure:**

| Price List | Assigned To | 5lb | 3lb | 1lb | 12oz | White-label |
|------------|-------------|-----|-----|-----|------|-------------|
| Base | Internal only | $47.50 | $28.50 | $9.50 | $8.00 | $8.00 |
| 2024 Wholesale | MOCHA EXPRESS (1 customer) | $52.50 | $31.50 | $10.50 | $9.00 | — |
| 2025 | Standard wholesale (46 customers) | $57.50 | $34.50 | $11.50 | $11.00 | — |
| 2026 | Panadería Rodriguez (1 customer) | $62.50 | $37.50 | $12.50 | $11.50 | — |
| White-label | Bunker only | — | — | — | — | $8.00 |

**Pricing anomaly:** MOCHA EXPRESS is assigned the 2024 list ($52.50/5lb) but their orders are actually charged at the base price ($47.50/5lb). They're paying less than their assigned list. 101 price mismatches found across early orders (Feb-March 2025) — prices aligned with lists after mid-March 2025 for most customers.

**Hiri mapping (DECIDED):**
1. Create Hiri PriceSet per product variant
2. **"2026" prices = the default wholesale rate** — what new customers get. Price with `customer_group_id` = Wholesale group
3. **"2025" prices = legacy grandfathered rate** — separate PriceList for existing customers until owner migrates them to 2026
4. **MOCHA EXPRESS** → Dedicated "MOCHA EXPRESS" customer group with $47.50/5lb pricing. Owner can adjust or move them to standard pricing later
5. **Bunker white-label** → Dedicated "The Bunker" customer group, $8.00/unit. Their own price list
6. Owner migrates legacy 2025 customers to 2026 pricing at their own pace by changing group/price list assignment

### 3f. Order History

**Recommendation: Do NOT migrate fulfilled order history.**
- 825 fulfilled orders — archive to CSV
- OrderSpace API available for reference for 12 months post-migration
- Hiri starts with clean ledger

**Migrate only:**
- **Order #1842** (status: new) — Faith Tri-Cities, 2026-03-10, $34.50
- **Order #8** (status: invoiced) — Kagen's Coffee, 2024-11-04, $525.00 — resolve first
- **Order #6** (status: invoiced) — Kagen's Coffee, 2024-10-18, $577.50 — resolve first

### 3g. Invoices

**Migrate unpaid invoices only** (~61+ representing outstanding receivables).

| OrderSpace Field | Hiri Field | Notes |
|------------------|------------|-------|
| `number` | `number` | Normalize mixed formats |
| `invoice_date` | `created_at` | Direct map |
| `orders[0].id` | `order_id` | 1:1 relationship |
| `paid` = false | `status` = `sent` | |
| `due_date` | `due_date` | Direct map |
| `gross_total` | `total` | **Convert dollars to cents (×100)** |
| `net_total` | `subtotal` | **Convert dollars to cents** |
| `invoice_lines[]` | `InvoiceLine` records | Convert prices to cents |
| `payments[]` | `InvoicePayment` records | For partial payments |
| `comments` | `notes` | Always empty in practice |

**Critical: Hiri stores monetary values as int (cents). OrderSpace uses float (dollars). Multiply by 100 and round.**

**Paid invoices:** Archive to CSV. Do not import.

---

## 4. Hiri Domain Model Changes Required

### New Fields on Customer
| Field | Type | Purpose |
|-------|------|---------|
| `PaymentTermsDays` | `*int` | Net 7/15/21/30. Used to auto-compute `Invoice.DueDate` |
| `BillingMethod` | `BillingMethod` enum | `manual` (default) / `ach` / `credit_card`. Controls whether QuickBooks auto-bills via ACH |

```go
type BillingMethod string

const (
    BillingMethodManual     BillingMethod = "manual"
    BillingMethodACH        BillingMethod = "ach"
    BillingMethodCreditCard BillingMethod = "credit_card"
)
```

**Migration default:** All customers get `BillingMethod = manual`. Owner opts in customers to ACH individually after verifying QuickBooks has their payment details.

### New Fields on Order
| Field | Type | Purpose |
|-------|------|---------|
| `ShippingMethod` | `ShippingMethod` enum | `pickup` / `local_delivery` / `shipped` |
| `RequestedDeliveryDate` | `*time.Time` | Customer-requested delivery date |

```go
type ShippingMethod string

const (
    ShippingMethodPickup        ShippingMethod = "pickup"
    ShippingMethodLocalDelivery ShippingMethod = "local_delivery"
    ShippingMethodShipped       ShippingMethod = "shipped"
)
```

### Metadata Storage (no schema change needed)
| OrderSpace Concept | Metadata Key | Notes |
|--------------------|-------------|-------|
| Per-type notification emails | `Customer.Metadata.notification_emails` | `{"orders": "...", "dispatches": "...", "invoices": "..."}` |
| Tax registration number | `Customer.Metadata.tax_number` | Mixed formats (WA UBI, EIN), stored as-is |
| OrderSpace customer ID | `Customer.Metadata.orderspace_id` | For cross-reference and idempotent re-runs |

### Not Needed (skip)
| OrderSpace Concept | Reason |
|--------------------|--------|
| Variant backorder flag | Use `InventoryItem.TrackInventory = false` |
| Variant RRP | Not used in OrderSpace |
| Invoice proforma/deposit | Not used in OrderSpace |
| Line item invoiced/paid/dispatched counts | Derived from related Invoice/Fulfillment records |
| Order created_by | Hiri tracks via session/auth |
| Invoice payment_terms text | Derived from `Customer.PaymentTermsDays` |

---

## 5. Migration Strategy

### Phase 1 — Pre-Migration Setup

1. **Fix data quality issues** — Contact client about the 6 critical address/email issues listed in Section 2
2. **Resolve stuck orders** — Orders #6 and #8 (Kagen's Coffee, "invoiced" since Oct/Nov 2024)
3. **Manual product setup** — Create all 10 products in Hiri admin with correct variants, options, SKUs, and images
4. **Enter product weights** — All currently 0.0 kg in OrderSpace
5. **Price configuration** — Set up PriceSets with wholesale pricing for 2025/2026
6. **Customer groups** — Create "Wholesale" and "The Bunker" groups
7. **Build mapping tables:**
   - OrderSpace SKU → Hiri variant UUID
   - OrderSpace customer_group_id → Hiri CustomerGroup UUID
   - OrderSpace payment_terms_id → days integer (15/21/30)
   - OrderSpace customer_id → Hiri Customer UUID (populated during import)

### Phase 2 — Write Migration Script

A CLI tool (`cmd/migrate-orderspace/main.go`) that:

1. **Authenticates** with OrderSpace API (client credentials, refresh token every 25 min)
2. **Fetches** all customers, unpaid invoices, and non-fulfilled orders (paginated, rate-limited)
3. **For each customer:**
   a. Normalize email (lowercase, trim)
   b. Check for existing Hiri customer by email (dedup with WooCommerce imports)
   c. Create `Customer` with:
      - `account_type = wholesale`, mapped `wholesale_status`
      - `PaymentTermsDays` from OS payment terms (7/15/21/30)
      - `BillingMethod = manual` (owner opts in to ACH individually)
   d. Create `Address` records (clean data first)
   e. Assign to `CustomerGroup` (Wholesale / MOCHA EXPRESS / The Bunker)
   f. Store in Metadata: OrderSpace ID, notification emails, tax number
   g. Record audit trail
4. **For each unpaid invoice:**
   a. Create corresponding Order if needed (with line items)
   b. Create `Invoice` with line items and due date
   c. Record any partial payments as `InvoicePayment` records
5. **Produces report:** imported/skipped/error counts, data quality warnings

**Script requirements:**
- Rate limiting: max 60 requests/minute (dataset is ~10 API pages, well within limits)
- Pagination: cursor-based, max 200 per page
- Idempotency: Use `metadata.orderspace_id` for dedup on re-runs
- Token refresh: Refresh OAuth token every 25 minutes (30-min expiry)
- Dry-run mode: Log what would happen without writing to database

### Phase 3 — Validation

| Check | Expected |
|-------|----------|
| Wholesale customer count | 46 (50 minus Firefly Software, Kool Beanz, Kagen's Coffee, and closed account) |
| Every OS buyer email exists in Hiri | 46/46 match |
| Every customer has ≥1 address | 46/46 |
| MOCHA EXPRESS has 4 addresses | Verified |
| Unpaid invoice total matches | OS total = Hiri total (in cents) |
| White-label products visible only to Bunker | Product group visibility check |
| Retail 12oz bags visible to all wholesale | Product 0009 visibility = public |
| Price spot-check: MOCHA EXPRESS | $47.50/5lb (dedicated group) |
| Price spot-check: standard wholesale | 2026 rate ($62.50/5lb) for new, 2025 rate ($57.50/5lb) for legacy |
| Price spot-check: Bunker white-label | $8.00/unit |
| All customers have `BillingMethod = manual` | No automated ACH until owner opts in |
| All customers have `PaymentTermsDays` set | 7/15/21/30 per OS payment terms (30 default) |
| Customer metadata populated | notification_emails, tax_number, orderspace_id |

### Phase 4 — Cutover

**Approach: "Parallel Run, Then Switch"**

OrderSpace is manual order management (no automated billing), making cutover straightforward:

1. **T-7 days:** Run migration script in dry-run mode, verify all mappings
2. **T-3 days:** Run migration script for real
3. **T-1 day:** Re-sync any new orders/invoices created in OS since migration
4. **T-0 (cutover day):**
   - Send email to all 47 wholesale customers with:
     - New Hiri wholesale portal URL
     - Password reset link
     - Brief "what's changing" guide
   - Clear communication: "All new orders go through Hiri starting [date]"
5. **T+1 to T+14:** Run OS and Hiri in parallel — OS accessible for reference
6. **T+30:** Export full OS archive, close OrderSpace account

### Phase 5 — Post-Migration Cleanup

1. Export full OrderSpace data (orders, invoices, dispatches) to CSV archive
2. Store archive in S3 alongside other historical data
3. Remove migration CLI tool from codebase
4. Close OrderSpace account after 30-day parallel period

---

## 6. Coordination with WooCommerce Migration

Both migrations feed into the same Hiri instance.

| Concern | Resolution |
|---------|-----------|
| **Email collisions** | Customer in both systems → one Hiri account with `account_type = wholesale` and retail subscription attached |
| **Product overlap** | Retail 12oz bags (OS code 0009) = WC "12oz Bag of Coffee" (ID 9338). Create once in Hiri, link from both migrations |
| **Variant sizes** | WC has 12oz/3lb/5lb. OS has 1lb/3lb/5lb. Union = 12oz/1lb/3lb/5lb for applicable products |
| **Migration order** | OrderSpace first (simpler, no billing automation), then WooCommerce (subscriptions need more care) |
| **Stripe** | OS does not use Stripe — no payment method concerns. WC migration handles Stripe customer/payment method linking |
| **QuickBooks** | OS payment descriptions reference QB payment IDs. Hiri has QB sync fields (qb_customer_id, qb_invoice_id). Consider linking during migration if QB customer IDs are available |

---

## 7. Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Customer email dedup failures | Duplicate accounts | Normalize email (lowercase, trim) before matching |
| Unpaid invoices go stale | Lost revenue | Flag invoices >60 days overdue for manual review |
| Customer doesn't set up password | Can't access Hiri portal | Send reminder emails at T+3 and T+7 |
| API token expires mid-run | Migration script fails | Refresh token every 25 min (30-min expiry) |
| Rate limiting (60 req/min) | Slow migration | Dataset is ~10 API pages — well within limits |
| White-label product access leak | Wrong products visible | Product group visibility + manual QA |
| Parallel orders during cutover | Order in wrong system | Clear cutover date communication |
| MOCHA EXPRESS pricing loss | Largest customer pays more | Verify base-price override is preserved in Hiri |
| Address data quality | Invalid addresses | Fix 6 critical issues pre-migration |
| Monetary conversion rounding | Cent mismatch | Round to nearest cent, verify totals match |
| Invoice number collisions | Duplicate invoice numbers | Normalize all to consistent format before import |
| Single-name buyers | Empty last_name | Allow empty last_name, use full name as first_name |

---

## 8. What NOT to Migrate

- **Fulfilled order history** (825 orders) — Archive to CSV, start clean
- **Paid invoices** (139+) — Archive to CSV
- **Cancelled orders** (17) — No value
- **Dispatches** — Fulfillment history stays in archive
- **Inventory levels** — Not tracked (all backorder=true, roast-to-order)
- **OrderSpace webhooks** — Not applicable
- **Firefly Software account** — Dev/test account
- **Kagen's Coffee account** — Same entity as Firefly Software (same address), test/internal account
- **Kool Beanz Koffee account** — Closed, no activity since March 2025

---

## 9. OrderSpace API Reference

**Authentication:**
```
POST https://identity.orderspace.com/oauth/token
Content-Type: application/json
{"grant_type":"client_credentials","client_id":"...","client_secret":"..."}
```
Token valid for 30 minutes.

**Base URL:** `https://api.orderspace.com/v1/`
**Rate limit:** 60 requests/minute
**Pagination:** cursor-based (`starting_after`), max 200 per page, `has_more` boolean

**Endpoints used:**
| Endpoint | Records | Pages |
|----------|---------|-------|
| `GET /customers` | 50 | 1 |
| `GET /orders` | 845 | 5 |
| `GET /invoices` | 200+ | 2+ |
| `GET /products` | 10 | 1 |
| `GET /categories` | 2 | 1 |
| `GET /customer_groups` | 2 | 1 |
| `GET /price_lists` | 3 | 1 |
| `GET /payment_terms` | 4 | 1 |

---

## 10. Resolved Decisions

1. **MOCHA EXPRESS pricing:** Create a dedicated "MOCHA EXPRESS" customer group with $47.50/5lb pricing. Preserves their current rate. Owner can adjust or move them to standard pricing later.

2. **Base price / price list meaning:** 2026 prices ($62.50/5lb) are the current standard rate for new customers. 2025 prices ($57.50/5lb) are a legacy grandfathered rate. Owner will migrate legacy customers to 2026 at their own pace.

3. **Wholesale MOQ:** Build the MOQ capability (fields exist on Variant: `wholesale_min_qty`, `wholesale_multiple`) but leave all values unset at migration time, matching current OrderSpace behavior. Owner configures minimums later — will be needed for free delivery/shipping thresholds.

4. **Payment terms & billing method:** Add `PaymentTermsDays *int` and `BillingMethod` enum (`manual`/`ach`/`credit_card`) to Customer domain. All migrated customers get `BillingMethod = manual`. Owner opts customers into ACH individually after verifying QuickBooks has their payment details. This prevents accidental automated charges.

5. **Shipping type / delivery date:** Add first-class fields to Order domain: `ShippingMethod` enum (`pickup`/`local_delivery`/`shipped`) and `RequestedDeliveryDate *time.Time`. Central to daily operations — must be queryable and filterable in admin.

6. **Notification emails:** Store in `Customer.Metadata.notification_emails` (`{"orders": "...", "dispatches": "...", "invoices": "..."}`). Promote to proper fields when a notification preferences system is built.

7. **The Bunker white-label pricing:** Dedicated "The Bunker" customer group with $8.00/unit price list. White-label products (`visibility = restricted`) only visible to this group.

8. **12oz retail bags on wholesale portal:** Yes — wholesale customers can order retail 12oz bags for resale. Product 0009 stays `visibility = public`.

9. **Ungrouped customers:** Assign Hanford High School, Panadería Rodriguez, and Just Juice to the "Wholesale" group. Kool Beanz excluded (closed).

10. **Panadería Rodriguez on 2026 prices:** Intentional — they're the newest customer, onboarded at current rates. 2026 is the standard going forward.

11. **Kagen's Coffee / Firefly Software:** Both are test/internal accounts (same address, emails route to staff). Exclude both from migration.

12. **Tax numbers:** Store as-is in `Customer.Metadata.tax_number`. No normalization needed.
