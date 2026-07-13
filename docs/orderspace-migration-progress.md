# Orderspace Migration — Progress Tracker

Working punch list for executing the OS → Hiri wholesale migration across multiple sessions. The architectural plan and boundary contracts live in [`orderspace-migration-plan.md`](./orderspace-migration-plan.md) — read that first for the **why** behind any item below. This doc tracks the **what's done / what's next**.

## Session log

| Date | Session focus | Outcome |
|---|---|---|
| 2026-04-27 | Boundary contracts (D1–D5) + census | Plan settled, no code |
| 2026-05-02 | Progress tracker created | This doc |
| 2026-05-03 | W1 + W3a + W3b | Schema + customer wiring landed; sqlc regenerated; migrate/rollback verified; tests green |
| 2026-05-03 | W4a + W4b + W4c | Customer-aware pricing resolver landed; PricingService gains ResolveForCustomer/Batch; tests green |
| 2026-05-03 | W5 + W6 + W8 + W7 | Wholesale cart + checkout now use customer-aware pricing + visibility; staleness check returns 409 + banner; G1 + G3 closed; tests green |
| 2026-05-03 | W9 + W10 | testutil fixtures (price list, price list price, customer group, product visibility) + 21 tests across `pricing_test.go`, `cart_pricing_test.go`, `wholesale_pricing_test.go` covering D2/D3/D4/D5 invariants; all green |
| 2026-05-03 | W11 | PriceListStore + PriceListService (CRUD, audited); PricingService.SetPriceListPrice + DeletePriceListPrice; admin pages at /admin/price-lists (list + inline edit) and /admin/price-lists/prices (variant×list matrix); customer-show price-list dropdown; sidebar entry; tests green |
| 2026-07-07 | **Batch 1 (pilot) migrated to PRODUCTION** | Rehearsed on fresh prod copy, then imported for real: 8 customers, 169 orders, 298 line items, 0 warnings; all on Wholesale 2026 + NET 7, verified; 8/8 welcome emails sent via Postmark |

When you finish a session, append a row. One sentence each.

## Status snapshot (verified 2026-05-03)

- Latest migration: `043_customer_price_list_id.sql` (W1 landed).
- `Customer.PriceListID *uuid.UUID` present in domain + scanned in `customerFromRow`, `List`, `ListWholesaleWithQBCustomerID`. `CustomerStore.UpdatePriceList` + `CustomerService.UpdatePriceList` (audited) wired.
- `PricingService.ResolveForCustomer` / `ResolveForCustomerBatch` landed (W4c). `PricingStore` has `GetPriceListPrice`, `ListBasePricesByVariants`, `ListPriceListPricesByVariants` (W4a/W4b). `NewPricingService` now takes `customerPricingReader`; wired in `cmd/server/main.go`.
- `CartService` now holds `*PricingService` (not `*PricingStore`); `AddItemForCustomer` resolves customer-aware price via `PricingService.ResolveForCustomer`.
- `handleWholesaleBulkAdd` calls `AddItemForCustomer` — **G1 closed** for the wholesale write path.
- `QuickOrderCatalog(ctx, tx, groupIDs, customerID, pricing, currency)` passes `VisibilityContext{IsWholesale: true, GroupIDs}` and uses `ResolveForCustomerBatch` — **G3 closed**.
- `handleWholesaleCheckoutConfirm` performs a fresh-resolve staleness check inside the order tx; on mismatch it rewrites cart rows via `AddItemForCustomer`, commits, and renders the wholesale checkout page with `PriceChangeBanner` + HTTP 409.
- `cart.AddItem` (retail) is unchanged — retail still snapshots base price by design.
- New customer-aware pricing + visibility paths now have test coverage: `internal/app/pricing_test.go` (D2/D5 resolver matrix), `cart_pricing_test.go` (D3 cart paths), `wholesale_pricing_test.go` (D3 denormalization + D4/D5 quick-order visibility & pricing). Fixtures under `internal/testutil/fixtures.go`.
- Admin price-list management is live: `PriceListService` (Create/Get/List/Update/Delete + CountCustomers), `PricingService.SetPriceListPrice` / `DeletePriceListPrice`, three new audit constants (`AuditPriceList{Created,Updated,Deleted}`), and three admin surfaces — `/admin/price-lists` (CRUD + inline rename/status), `/admin/price-lists/prices` (variant×list matrix mirroring the group-pricing page), and a customer-page price-list dropdown next to payment terms.
- `cmd/os-migrate/main.go` exists but predates pricing/visibility work; needs I1–I6.
- Wholesale storefront templates exist and route correctly; reskin to paper-and-ink is a separate cosmetic track and does not block the import.

## Next up

The importer track (I1–I6). All schema, services, and admin UI are now in place.

---

## Code work

Each item is a single small commit unless flagged otherwise. Decisions column refers to D1–D5 in the plan doc.

### Schema

- [x] **W1** — `db/migrations/043_customer_price_list_id.sql` — add `customers.price_list_id uuid REFERENCES price_lists(id) ON DELETE SET NULL` + partial index. (D1, XS)
- [ ] **W2** — migration adding `orders.customer_reference text NULL`. (—, XS)

### Customer wiring

- [x] **W3a** — `Customer.PriceListID *uuid.UUID` in `internal/domain/customer.go`; map in `customerFromRow`; add `CustomerStore.UpdatePriceList`. (D1, XS)
- [x] **W3b** — `CustomerService.UpdatePriceList` + `AuditCustomerPriceListUpdated` constant in `platform/audit/actions.go`. (D1, XS)

### Pricing service

- [x] **W4a** — `PricingStore.GetPriceListPrice` (single) + sqlc query. (D2, XS)
- [x] **W4b** — `PricingStore.ListBasePricesByVariants` + `ListPriceListPricesByVariants` (batch) + sqlc queries. (D5, S)
- [x] **W4c** — `PricingService.ResolveForCustomer` + `ResolveForCustomerBatch` + `customerPricingReader` interface; update constructor in `cmd/server/main.go`. **Blocks W5–W8.** (D2 + D5, S)

### Cart + checkout

- [x] **W5** — `CartService.AddItemForCustomer` (sibling to existing `AddItem`). (D3, XS)
- [x] **W6** — wire wholesale handlers (`handleWholesaleBulkAdd`, quick-order single-add) → `AddItemForCustomer`; update `QuickOrderCatalog` signature + handler call site. (D3 + D4 + D5, S)
- [x] **W7** — `handleWholesaleCheckoutConfirm` staleness check: re-resolve at confirm; on mismatch update cart row, return 409, render cart-page banner. (D3, S)
- [x] **W8** — `QuickOrderCatalog` two-pass refactor (collect variantIDs → batch resolve → stitch); pass `Visibility: &VisibilityContext{IsWholesale: true, GroupIDs: ...}` filter. (D4 + D5, S)

### Tests

- [x] **W9** — `testutil` fixtures + assertions per the plan's "Test boundary" section. (—, S)
- [x] **W10** — test cases: pricing matrix (base/list/customer combos), cart hint vs. handler resolution, visibility filter, batch omitted-variants behavior, wholesale denormalization. Handler-level checkout staleness test deferred (covered transitively by D3 denormalization + cart tests). (—, M)

### Admin UI (post-import operability)

- [x] **W11** — admin pages: assign price list to customer; manage variant×price-list overrides; price-list CRUD. (—, M–L)

**Suggested order:** W1 → W3a → W3b → W4a → W4b → W4c → W5 → W6 → W8 → W7 → W9 → W10 → W11.

---

## Importer work (`cmd/os-migrate`)

Do these only after W4c is green so the importer can write into the new pricing model.

- [ ] **I1** — import OS price lists → Hiri `PriceList` (no tiers, no group binding). (XS)
- [ ] **I2** — import OS customer groups → Hiri `CustomerGroup` (id + name). (XS)
- [ ] **I3** — for each variant + each `price_list_prices` entry → write `Price` row (variant, price_list, unit_price). No groups, no tiers. (S)
- [ ] **I4** — translate OS visibility via static map: Featured Products → public; Bunker white-label → restricted, visible to Bunker Tactical group. (XS)
- [ ] **I5** — customer import: set `price_list_id` (default `2026` when null), set group via `customer_group_memberships`, translate `payment_terms_id` → `PaymentTermsDays`. (S)
- [ ] **I6** — products in "Bunker white-label" category → `visibility=restricted` + `product_group_visibility` row for Bunker group. (XS)

**Payment terms translation table** (paste verbatim into the importer):
```
pt_zmzxkjrn (Immediate) → 0
pt_ynw1k75m (15 Days)   → 15
pt_dmrkvqwe (21 Days)   → 21
pt_wepp165e (30 Days)   → 30
```

After I1–I6 land, dry-run against staging DB and diff effective prices for a sample of high-value customers against actual OS invoices from the last 30 days. **Must match to the cent.**

---

## OS-side cleanup (Marisa)

10 customers need fixes in Orderspace before import. Re-run `go run ./cmd/os-report` for the live list — these were the issues as of 2026-04-27:

- [ ] `cu_63l489m1` BASELING SPORTS CARDS — missing everything; typo email; suggest deleting in OS
- [ ] `cu_636ge9k1` MOCHA EXPRESS TRICITIES — on legacy 2024 list; bump to 2025/2026
- [ ] `cu_rnykjd83` ExpressUp Coffee — no group
- [ ] `cu_g34kr7q1` Port of Benton Admin — no group
- [ ] `cu_r19oo961` Hanford High School — no group, no payment_terms
- [ ] `cu_63lmdqm1` Panadería y Antojitos Mexicanos — no group
- [ ] `cu_61gwo4qn` Just Juice — no group
- [ ] `cu_g3428jp1` Kool Beanz Koffee — no group, no payment_terms
- [ ] `cu_j35jlo2n` Bunker Uniforms and Equipment — no payment_terms; on 2025 not Tailwinds — confirm intent
- [ ] `cu_m1qmqqw3` Byte Brew — no payment_terms

---

## Cutover sequence

Execute top-to-bottom. Each step gates the next.

- [ ] Land schema + code (W1–W5, W7) behind a feature flag, tests green.
- [ ] Land importer updates (I1–I6); dry-run against staging; price diff vs. last 30 days of OS invoices.
- [ ] Land admin UI (W11) — required to manage the system post-import.
- [ ] OS cleanup pass — Marisa fixes the 10 customers above.
- [ ] Freeze OS — turn off OS ordering. Email wholesale customers (template still TBD).
- [ ] Final delta import — re-run for any orders placed during the freeze window.
- [ ] Flip DNS — `wholesale.rockabillyroasting.com` → Hiri.
- [ ] QB reconciliation — verify imported QB customer records match existing entries (don't double-create). `SyncQBCustomerArgs` job should match-by-name/email first.
- [ ] Watch metrics — Prometheus + Sentry for first 2 weeks. Keep read-only OS access for 30 days for rollback.

---

## Open questions to resolve before/during cutover

- [ ] Tailwinds price list — keep or retire? Has 1 non-Bunker customer.
- [ ] Does Marisa use OS's order-level `customer_reference`? If no, W2 can be skipped.
- [ ] Does RR use `minimum_spend` per customer in OS? (Importer captures it; Hiri has no field.)
- [x] Default new-customer onboarding: assign **2026** price list, group **Main Products** (resolved 2026-04-27).

---

## Out of scope (do not bundle into this migration)

- Wholesale storefront paper-and-ink reskin (8 templates, ~131 `rr-*` refs). Cosmetic; ship independently. See [reskin-status memory](../../.claude/projects/-home-dukerupert-Repos-rockabilly-roasting/memory/reskin-status.md).
- Volume-tier pricing — RR has zero tiers in OS; schema fields stay dormant.
- Discount codes for wholesale — not used in OS.

---

## References

- [`docs/orderspace-migration-plan.md`](./orderspace-migration-plan.md) — full architectural plan, D1–D5 contracts, data findings
- `cmd/os-report/main.go` — read-only OS census tool
- `cmd/os-migrate/main.go` — importer to update
- `internal/app/pricing.go` — where `ResolveForCustomer` lives
- `internal/app/wholesale.go` — `QuickOrderCatalog`, `PlaceWholesaleOrder`
- `internal/app/cart.go` — `AddItem` (G1 site)
- `internal/store/catalog.go:225+` — `VisibilityContext` already implemented; just not called from wholesale
