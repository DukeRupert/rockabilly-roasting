# Orderspace → Hiri Migration Plan

**Status:** Boundary contracts settled. Ready for implementation.
**Last updated:** 2026-04-27
**Owner:** Logan

This is the working plan for migrating Rockabilly Roasting's wholesale operations from Orderspace into Hiri. It supersedes the archived `cmd/os-migrate` work, which only covered customers + addresses + orders without the pricing layer.

The OS census tool used to produce the data findings below lives at `cmd/os-report/` and is read-only. Run with `go run ./cmd/os-report` (uses `ORDERSPACE_CLIENT_ID` / `ORDERSPACE_CLIENT_SECRET` from `.env`).

---

## What OS data looks like in production

| Resource | Count | Notes |
|---|---|---|
| Customer groups | 2 | "Main Products" (46), "The Bunker Tactical" (1), 7 unassigned |
| Price lists | 4 | 2024 Wholesale (legacy), 2025, 2026, Tailwinds. All USD. |
| Payment terms | 4 | Immediate (0), 15, 21, 30 days |
| Categories | 2 | Featured Products, The Bunker white-label |
| Products | 10 | ~30 variants total |
| Variant × price-list overrides | 316 | **Zero have volume tiers** — RR doesn't use tiered pricing in OS |
| Customers | 54 | 53 of 54 have a `price_list_id` directly assigned |

**Key insight:** Pricing is driven by `customer.price_list_id`, not by group. Groups exist mostly for **category visibility** (white-label gating), not pricing.

**Visibility model in OS (UI-set, not API-exposed):**
- "Featured Products" category → visible to everyone (Main Products group)
- "The Bunker white-label" category → visible only to "The Bunker Tactical" group

**Sample variant pricing structure** (`variant.price_list_prices`):
```
SKU 0009-C9
  Base unit_price       $12.50
  2024 Wholesale Price  $9.00
  2025                  $11.00
  2026                  $12.50
  Tailwinds             $12.50
```

---

## Feature parity matrix (V3, confirmed)

| Capability | OS | Hiri today | Status |
|---|---|---|---|
| Wholesale customer model | full | matches | OK |
| Customer groups | id + name | `CustomerGroup` + `customer_group_memberships` (m:m) | OK |
| Price lists | per-customer assignment | `PriceList` + `Price` schema present | schema OK, not wired |
| Volume tiers | not used in OS | schema fields exist | SKIP — RR has zero tiers |
| Per-customer catalog | category × group visibility | `ProductVisibility` + `product_group_visibility` table | infra OK, not wired into wholesale checkout |
| MOQ / multiples | per-variant `minimum` / `multiple` | `WholesaleMinQty` / `WholesaleMultiple` + `ValidateWholesaleCart` | OK |
| Payment terms | resource w/ `days_due` | `PaymentTermsDays` int on customer | OK; needs translation table at import |
| Invoices | native | `Invoice` + `InvoiceLine` + `InvoicePayment` | OK |
| Order metadata | po_number, reference, internal_note, delivery_date | po_number, internal_note, requested_delivery_date present; **customer_reference missing** | minor schema gap |
| Tax exemption | `tax_number` | `tax_exempt` bool + `tax_exempt_reason` + checkout honors it | OK |
| QuickBooks sync | via Xero/Unleashed | native QB OAuth + customer + invoice + payment | Hiri stronger |
| Multi-currency | yes (per list) | USD only | not needed for RR |
| Discount codes | native | retail-only in Hiri | not needed for migration |
| Min spend per customer | `minimum_spend` field | not implemented | LOW — confirm with merchant if used |

---

## Confirmed gaps (severity-ranked)

| # | Gap | Severity | Where it bites |
|---|---|---|---|
| G1 | `cart.AddItem` snapshots base price (`internal/app/cart.go:67`); orders + invoices inherit it | BLOCKER | Wrong prices charged from minute 1 |
| G2 | Volume tier resolution not implemented | SKIP | Not used in OS, not needed for migration |
| G3 | `QuickOrderCatalog` ignores `product_group_visibility` (`internal/app/wholesale.go:297`) | HIGH | Bunker white-label products visible to all wholesale customers |
| G4 | Group-based pricing not wired | MED | Group is not the pricing axis in RR's data; price-list is. Still nice for fallback. |
| G5 | Order missing `customer_reference` field | LOW | Demoted; add column, optional UI |
| G6 | Wholesale bypasses discount/coupon system | LOW | Not used in OS |
| G7 | No customer-level `minimum_spend` | LOW | Confirm with merchant; likely unused |
| **NEW** | `Customer.PriceListID` field missing from schema | BLOCKER | Primary pricing axis in OS data |

---

## Implementation contracts

Five boundary decisions were settled in a Design-mode review. Each contract below is self-contained — no further architectural questions should remain.

### Decision 1: `Customer.PriceListID` placement

Plain nullable FK directly on the `Customer` domain type. Mirrors the existing pattern used by `CustomerGroupID *uuid.UUID`, `PaymentTermsDays *int`, `QBCustomerID *string`. No join table — there is no per-assignment lifecycle or effective-dates requirement.

**Domain change** (`internal/domain/customer.go`, after `QBSyncedAt`):
```go
// PriceListID is the price list explicitly assigned to this customer.
// When set, PricingService.ResolveForCustomer uses this list's prices
// in preference to the base price. Nil means base pricing applies.
PriceListID *uuid.UUID
```

**Migration** (`db/migrations/039_customer_price_list_id.sql`):
```sql
-- +goose Up
ALTER TABLE customers
    ADD COLUMN price_list_id uuid REFERENCES price_lists(id) ON DELETE SET NULL;
CREATE INDEX idx_customers_price_list_id ON customers (price_list_id)
    WHERE price_list_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_customers_price_list_id;
ALTER TABLE customers DROP COLUMN IF EXISTS price_list_id;
```
- `ON DELETE SET NULL` — silent fallback to base if list deleted.
- Partial index mirrors `idx_customers_qb_id` from migration 031.

**Store changes:**
- `CustomerStore.GetByID` — sqlcgen regenerates; add `PriceListID` mapping in `customerFromRow`.
- `CustomerStore.List` (hand-written) — add `price_list_id` to SELECT + scan.
- `CustomerStore.ListWholesaleWithQBCustomerID` — same.
- New: `CustomerStore.UpdatePriceList(ctx, tx, id, priceListID *uuid.UUID) error` — matches `UpdateCustomerGroup` / `UpdatePaymentTerms` shape. Nil clears.

**Audit + Permission:**
- New constant `AuditCustomerPriceListUpdated = "customer.price_list_updated"` in `platform/audit/actions.go`.
- No new permission. `PermEditCustomers` (admin-only) covers it — same gate as tax exemption / customer group / payment terms.

**Service method** — `CustomerService.UpdatePriceList(ctx, tx, id, priceListID, actor)`:
1. `s.customers.UpdatePriceList(...)`
2. `s.audit.Record(...)` with `AuditCustomerPriceListUpdated`, `After: {"price_list_id": priceListID}`
3. No River job. Metric increment in handler post-commit.

**Out of scope:** importer (sets via direct SQL), Stripe, QuickBooks, retail cart, subscription renewal, sqlcgen source files (regenerated, not hand-edited).

### Decision 2: `PricingService.ResolveForCustomer` location

Method on the existing `PricingService` — not a new top-level boundary. `PricingService` already owns base + group resolution; adding customer-aware resolution is a natural extension.

**Dependency change** — minimal interface in `internal/app/pricing.go`:
```go
type customerPricingReader interface {
    GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Customer, error)
}

type PricingService struct {
    pricing   *store.PricingStore
    customers customerPricingReader
}

func NewPricingService(pricing *store.PricingStore, customers customerPricingReader) *PricingService
```
`*store.CustomerStore` already satisfies this. **One call site to update**: `cmd/server/main.go`.

**New method:**
```go
func (s *PricingService) ResolveForCustomer(
    ctx context.Context, tx pgx.Tx,
    variantID uuid.UUID,
    customerID uuid.UUID,
    currencyCode string,
) (cents int64, err error)
```
`currencyCode` parameter matches every other pricing method — no hardcoded "USD".

**Sentinel errors (reused, no new ones):**
- `ErrPriceNotFound` (`errors.go:87`) — neither list nor base price exists.
- `ErrCustomerNotFound` (`errors.go:19`) — customerID unknown.

**Algorithm:**
1. `s.customers.GetByID(ctx, tx, customerID)` → `pgx.ErrNoRows` ⇒ `ErrCustomerNotFound`.
2. If `customer.PriceListID == nil` → `GetBasePrice`; `ErrNoRows` ⇒ `ErrPriceNotFound`. Return.
3. Else `GetPriceListPrice(variantID, *PriceListID, currencyCode)` → on row, return.
4. On `ErrNoRows` from step 3, fall through to `GetBasePrice`. Still missing ⇒ `ErrPriceNotFound`.

**Missing-variant-in-list semantics:** fall back to base price (do not error). Operational reasoning: new SKUs added after a price list was created shouldn't block checkout just because the list wasn't updated.

**New store method:**
```go
func (s *PricingStore) GetPriceListPrice(
    ctx context.Context, tx pgx.Tx,
    variantID, priceListID uuid.UUID, currencyCode string,
) (*domain.Price, error)
```
Naming matches `GetBasePrice` / `GetGroupPrice`. New sqlc query in `db/queries/pricing.sql` filtering `price_list_id = $3 AND customer_group_id IS NULL AND min_quantity IS NULL LIMIT 1`.

**Schema verification:** `prices.price_list_id` and `idx_prices_price_list_id` already exist (migration `005_pricing.sql:24,31`). **No new migration for D2.** Composite index deferred — single-column sufficient at single-merchant scale.

### Decision 3: Cart price = hint; handler is the resolution boundary

The cart-stored `unit_price` is the contract that flows to the line item. `PlaceWholesaleOrder` trusts the prices passed in via params — it does **not** re-resolve internally and does **not** gain a `PricingService` dependency. The web handler is responsible for calling `ResolveForCustomer` (or its batch sibling) before constructing `PlaceWholesaleOrderParams`. This mirrors the existing retail `PlaceOrder` flow.

**`CartService` change** — add a sibling method, keep retail `AddItem` unchanged:
```go
func (s *CartService) AddItemForCustomer(
    ctx context.Context, tx pgx.Tx,
    cartID, variantID uuid.UUID,
    quantity int,
    customerID uuid.UUID,
    currencyCode string,
) (*domain.CartItem, error)
```
Internally calls `s.pricing.ResolveForCustomer(...)` then `s.carts.UpsertCartItem(...)`. No new struct injection — `CartService` already holds `*PricingService`.

Updated docstring on `domain.CartItem.UnitPrice`:
> Resolved effective price in cents at the time the item was added. For wholesale carts this reflects the customer's assigned price list; for retail, the base price. This value is the contract — `PlaceOrder` / `PlaceWholesaleOrder` writes it verbatim to the line item.

**`PlaceWholesaleOrder`** — no signature change, no behavior change, no `PricingService` dependency. Trusts `params.Items[i].UnitPrice`.

**Web call sites:**

| Site | Change |
|---|---|
| `handleWholesaleBulkAdd` | Switch `CartService.AddItem` → `AddItemForCustomer`, pass `customer.ID, "USD"` |
| `handleWholesaleCheckoutPage` | No change |
| `handleWholesaleCheckoutConfirm` | New staleness check — see below |
| Quick-order single-item add | Same as `handleWholesaleBulkAdd` |
| `QuickOrderCatalog` rendering | Switch to `ResolveForCustomerBatch` (D5) |

**Staleness contract — Option 2 (reject at confirm):**

Before constructing `PlaceWholesaleOrderParams`, `handleWholesaleCheckoutConfirm` calls `pricing.ResolveForCustomerBatch(ctx, tx, variantIDs, customerID, "USD")`. For any item where the resolved price differs from `cart_items.unit_price`:
1. Update the cart row with the fresh price (so the next page load shows truth — without this the customer is stuck in a reject loop).
2. Return HTTP 409, render the cart page with banner: *"Some prices changed since you added items to your cart. Please review your cart before continuing."*

If all match: proceed to `PlaceWholesaleOrder` unchanged. Wholesale-only — retail keeps silent semantics.

**Invariants:**
- Every wholesale line item: `unit_price > 0`, `subtotal = unit_price * quantity`, `total = subtotal`.
- For wholesale orders, `line_item.unit_price == ResolveForCustomer(variant, customer, currency)` at the time the cart item was created (not at order placement).
- No wholesale line item has `unit_price == base_price` when the customer has a non-null `price_list_id` containing an entry for that variant. (Core migration correctness test.)

**Out of scope:** retail `PlaceOrder` and retail `AddItem`, subscription renewal pricing, Stripe payment-intent calc (downstream of cart subtotal), audit log shape.

### Decision 4: Visibility filter call site

Handler builds `groupIDs []uuid.UUID` from `Customer.CustomerGroupID` (single nullable FK) and passes it positionally to `QuickOrderCatalog`. No middleware change. No `CustomerGroupStore.ListByCustomer` call — pricing already uses the FK shortcut elsewhere; mirror that.

**Critical:** `VisibilityContext.IsWholesale: true` is mandatory in the filter. Without it the SQL collapses to `visibility = 'public'` and hides every wholesale-tier product.

**`WholesaleService.QuickOrderCatalog` signature** (combined with D5):
```go
func (s *WholesaleService) QuickOrderCatalog(
    ctx context.Context, tx pgx.Tx,
    groupIDs []uuid.UUID,        // D4
    customerID uuid.UUID,         // D5
    pricing *PricingService,
    currencyCode string,
) ([]QuickOrderProduct, error)
```
Empty/nil `groupIDs` is safe — SQL falls through to `visibility IN ('public', 'wholesale')`.

**Internal change** — extend the existing `store.ListProducts` call:
```go
store.ProductFilter{
    Status: ptrTo(domain.ProductStatusActive),
    Visibility: &store.VisibilityContext{
        IsWholesale: true,
        GroupIDs:    groupIDs,
    },
}
```

**Handler-side wiring (`handleWholesaleQuickOrder`):**
```go
var groupIDs []uuid.UUID
if customer.CustomerGroupID != nil {
    groupIDs = []uuid.UUID{*customer.CustomerGroupID}
}
```

**Edge cases (no defensive code needed):**
- No group → nil slice → safe SQL branch.
- Group deleted → schema's `ON DELETE SET NULL` clears FK; auth middleware reloads customer per request.
- Staff impersonation → does not exist in this codebase.

**Out of scope:** admin catalog handler (no `Visibility` filter; staff sees everything — keep as-is), `CatalogService.ListProducts` (pass-through), storefront handlers (already pass `IsWholesale: false`).

### Decision 5: Batched pricing lookup

Separate `ResolveForCustomerBatch` method on `PricingService`. Not variadic — single-item and batch have different contracts (single fails fast on missing; batch returns a map and falls back).

```go
func (s *PricingService) ResolveForCustomerBatch(
    ctx context.Context, tx pgx.Tx,
    customerID uuid.UUID,
    variantIDs []uuid.UUID,
    currencyCode string,
) (map[uuid.UUID]int, error)
```

**Return semantics:** `map[uuid.UUID]int` keyed by variantID. Unpriced variants are **omitted** — caller's `priceMap[v.ID]` zero-values silently. This matches the existing `ListBasePricesByProduct` consumer pattern at `wholesale.go:371`. No error on missing prices — render-time call shouldn't blow up the page.

**Algorithm:**
1. `s.customers.GetByID(ctx, tx, customerID)` — single lookup, not N.
2. If `customer.PriceListID == nil` → delegate to `ListBasePricesByVariants`. Return.
3. Else:
   a. `ListPriceListPricesByVariants(...)` → `listPrices`.
   b. Collect variantIDs missing from `listPrices`.
   c. If any missing: `ListBasePricesByVariants(missing, currencyCode)` → `basePrices`.
   d. Merge: start with `basePrices`, overlay with `listPrices`. Return.

Two queries in the worst case, one when fallback isn't needed. Single SQL query with `LEFT JOIN / COALESCE` is feasible but the two-query split is simpler to test and adequate at single-merchant scale.

**New store methods:**
```go
func (s *PricingStore) ListBasePricesByVariants(
    ctx context.Context, tx pgx.Tx,
    variantIDs []uuid.UUID, currencyCode string,
) (map[uuid.UUID]int, error)

func (s *PricingStore) ListPriceListPricesByVariants(
    ctx context.Context, tx pgx.Tx,
    variantIDs []uuid.UUID, priceListID uuid.UUID, currencyCode string,
) (map[uuid.UUID]int, error)
```
Naming: "by variants" suffix because they take variantIDs directly (vs. the existing `ListBasePricesByProduct` which scopes through `variants.product_id`). Both filter `customer_group_id IS NULL AND min_quantity IS NULL`. New sqlc queries.

**Schema:** existing `idx_prices_price_list_id` + unique index on `price_sets.variant_id` are sufficient at single-merchant scale (hundreds of variants × tens of price lists). **No new migration for D5.**

**Refactor inside `QuickOrderCatalog`** — two-pass shape:
1. First pass: collect all variant IDs across products.
2. Single batch call: `priceMap := pricing.ResolveForCustomerBatch(ctx, tx, customerID, allVariantIDs, "USD")`.
3. Stitch loop: `unitPrice := priceMap[v.ID]` — line 371 unchanged in shape, only source differs.

**Handler diff:**
```go
products, txErr = d.WholesaleService.QuickOrderCatalog(
    ctx, tx, groupIDs, customer.ID, d.PricingService, "USD",
)
```

**Out of scope:** `ResolveForCustomer` (D2), cart pricing (D3), visibility (D4), retained `ListBasePricesByProduct` (still used by retail/admin).

### Test boundary (cumulative across all five decisions)

**New testutil fixtures** (`internal/testutil/`):
- `CreatePriceList(t, tx, opts...) *domain.PriceList` + `WithPriceListStatus(...)`
- `CreatePriceListPrice(t, tx, priceListID, variantID, amountCents) *domain.Price`
- `WithPriceList(id uuid.UUID) CustomerOption`
- `AssertResolvedPrice(t, wantCents, got int64)`
- `CreateCustomerGroup(t, tx) *domain.CustomerGroup`
- `WithProductVisibility(v domain.ProductVisibility) ProductOption`
- `AddProductGroupVisibility(t, tx, productID, groupID)`

**New test cases:**
- D2: `TestResolveForCustomer_PriceListOverridesBase`, `TestResolveForCustomer_BaseWhenNoList`
- D3: `TestAddItemForCustomer_UsesPriceListPrice`, `TestAddItemForCustomer_FallsBackToBasePrice`, `TestPlaceWholesaleOrder_DenormalizesCartPrice`, `TestCheckoutConfirm_RejectsStalePrices`
- D4: `TestQuickOrderCatalog_FiltersByGroup`, `TestQuickOrderCatalog_NoGroupShowsPublicAndWholesale`
- D5: `TestResolveForCustomerBatch_PriceListOverridesBase`, `TestResolveForCustomerBatch_NoListUsesBase`, `TestResolveForCustomerBatch_VariantWithoutBasePrice`

---

## Schema + code work

Per the contracts above. Each item maps to one or more decisions.

| # | Item | Decisions | Size |
|---|---|---|---|
| W1 | Migration `039_customer_price_list_id.sql` (FK + partial index) | D1 | XS |
| W2 | Migration: add `orders.customer_reference` (nullable text) | — | XS |
| W3a | `Customer.PriceListID` field + `customerFromRow` mapping; `CustomerStore.UpdatePriceList` | D1 | XS |
| W3b | `CustomerService.UpdatePriceList` + `AuditCustomerPriceListUpdated` constant | D1 | XS |
| W4a | `PricingStore.GetPriceListPrice` (single) + new sqlc query | D2 | XS |
| W4b | `PricingStore.ListBasePricesByVariants` + `ListPriceListPricesByVariants` (batch) + sqlc queries | D5 | S |
| W4c | `PricingService.ResolveForCustomer` + `ResolveForCustomerBatch` + `customerPricingReader` interface; constructor change in `cmd/server/main.go` | D2, D5 | S |
| W5 | `CartService.AddItemForCustomer` (sibling to `AddItem`) | D3 | XS |
| W6 | Wire wholesale handlers: `handleWholesaleBulkAdd` + quick-order single-add → `AddItemForCustomer`; `QuickOrderCatalog` signature + handler call site | D3, D4, D5 | S |
| W7 | `handleWholesaleCheckoutConfirm` staleness check + cart-row update + 409 + cart-page banner | D3 | S |
| W8 | `QuickOrderCatalog` internal: two-pass refactor (collect variantIDs → batch resolve → stitch); `Visibility: &VisibilityContext{IsWholesale: true, GroupIDs}` filter wiring | D4, D5 | S |
| W9 | testutil fixtures + assertions (see contract test boundary section) | — | S |
| W10 | Test cases (see contract test boundary section) — pricing matrix, cart, checkout staleness, visibility, batch | — | M |
| W11 | Admin UI: assign price list to customer; manage variant×price-list overrides; assign customer to groups | — | M–L |

**Total: ~1.5 weeks** focused work. W11 is the biggest single piece. W4c blocks W5/W6/W7/W8 (everything depends on the resolver existing).

**Suggested order:** W1 → W3a → W3b → W4a → W4b → W4c → W5 → W6 → W8 → W7 → W9 → W10 → W11.

---

## Importer work (`cmd/os-migrate` updates)

| # | Item | Size |
|---|---|---|
| I1 | Import OS price lists → Hiri `PriceList` (no tiers, no group binding) | XS |
| I2 | Import OS customer groups → Hiri `CustomerGroup` (id + name only) | XS |
| I3 | For each variant + each `price_list_prices` entry → write `Price` row (variant, price_list, unit_price). No groups, no tiers. | S |
| I4 | Translate OS visibility — static map in importer:<br/>• Featured Products → public (no restriction)<br/>• The Bunker white-label → restricted, visible to Bunker Tactical group | XS |
| I5 | Customer import: set `price_list_id` (default to 2026 if null), set group via `customer_group_memberships`, translate `payment_terms_id` → `PaymentTermsDays` via lookup | S |
| I6 | For products in "Bunker white-label" category → set `visibility=restricted` + write `product_group_visibility` row for Bunker group | XS |

**Payment terms translation table:**
```
pt_zmzxkjrn (Immediate Payment) → 0
pt_ynw1k75m (15 Days)           → 15
pt_dmrkvqwe (21 Days)           → 21
pt_wepp165e (30 Days)           → 30
```

---

## Cleanup pass on OS side (do this BEFORE cutover)

10 customers need fixes in Orderspace before import. Run `go run ./cmd/os-report` for the latest list. As of 2026-04-27:

| OS Customer ID | Name | Issues |
|---|---|---|
| `cu_63l489m1` | BASELING SPORTS CARDS | Missing everything; typo email — suggest deleting in OS |
| `cu_636ge9k1` | MOCHA EXPRESS TRICITIES | On legacy 2024 price list — bump to 2025/2026 |
| `cu_rnykjd83` | ExpressUp Coffee | No group |
| `cu_g34kr7q1` | Port of Benton Admin | No group |
| `cu_r19oo961` | Hanford High School | No group, no payment_terms |
| `cu_63lmdqm1` | Panadería y Antojitos Mexicanos | No group |
| `cu_61gwo4qn` | Just Juice | No group |
| `cu_g3428jp1` | Kool Beanz Koffee | No group, no payment_terms |
| `cu_j35jlo2n` | Bunker Uniforms and Equipment | No payment_terms; **on 2025, not Tailwinds** — confirm intent |
| `cu_m1qmqqw3` | Byte Brew | No payment_terms |

---

## Cutover sequence

1. **Land schema + code** (W1–W5, W7) behind a feature flag, with passing tests.
2. **Land importer updates** (I1–I6) and run against staging DB. Diff a sample of high-value customers' effective prices against actual OS invoices from the last 30 days. Must match to the cent.
3. **Land admin UI** (W6) — only blocker for *managing* the system post-import.
4. **OS cleanup pass** — merchant fixes the 10 customers above.
5. **Freeze OS** — turn off OS ordering. Email wholesale customers (template needed).
6. **Final delta import** — re-run for any orders placed in OS during the freeze window.
7. **Flip DNS** — `wholesale.rockabillyroasting.com` points at Hiri.
8. **QB reconciliation** — verify imported QB customer records match existing QB entries (don't double-create). `SyncQBCustomerArgs` job should match-by-name/email first.
9. **Watch metrics** — Prometheus + Sentry for the first 2 weeks. Keep read-only OS access for 30 days for rollback.

---

## Open questions

1. **Tailwinds price list** — keep or retire? It has 1 customer who isn't Bunker. If it's vestigial, drop it pre-migration.
2. **`customer_reference` field** — does Marisa actually use OS's order-level reference field? If no, can leave unbuilt.
3. **`minimum_spend` per customer** — does RR use this in OS? Importer captures the value but Hiri has no field for it.
4. **Default new-customer onboarding** — confirmed: assign 2026 price list by default. Group default? Probably "Main Products".

**Resolved during contract review (2026-04-27):**
- ~~Cart price as hint vs. contract~~ → hint; handler resolves; `PlaceWholesaleOrder` trusts cart-stored prices.
- ~~Where `ResolveForCustomer` lives~~ → method on existing `PricingService`, no new boundary.
- ~~`Customer.PriceListID` shape~~ → plain FK, no association table.
- ~~Visibility filter call site~~ → handler passes `groupIDs`; `IsWholesale: true` mandatory.
- ~~Batched lookup shape~~ → separate `ResolveForCustomerBatch`; map omits unpriced variants.
- ~~Stale prices between cart-add and checkout-confirm~~ → wholesale rejects with 409 + cart-row update; retail keeps silent semantics.

---

## References

- `cmd/os-report/main.go` — read-only OS census tool
- `cmd/os-migrate/main.go` — archived importer; needs I1–I6 updates
- `internal/app/wholesale.go` — `QuickOrderCatalog`, `PlaceWholesaleOrder`
- `internal/app/cart.go:67` — where base price is currently snapshotted
- `internal/app/pricing.go` — where `ResolveForCustomer` will live
- `internal/store/catalog.go:285-302` — visibility filter (already correct, just not called from wholesale flow)
- `internal/db/migrations/001_customers.sql` — customer schema (legacy `customer_group_id` FK + `customer_group_memberships` join table)
- `internal/db/migrations/022_orders_wholesale.sql` — wholesale order fields
- `internal/db/migrations/033_*` — `requested_delivery_date` lives here
- [OS Price Lists docs](https://docs.orderspace.com/article/32-price-lists)
- [OS Customer Groups docs](https://docs.orderspace.com/article/16-customer-groups)
