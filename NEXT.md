# Next session — Orderspace migration W9 + W10 (testutil + tests)

Session-handoff doc for the next claude run. Delete it when the work is landed.

## Why this exists
W1–W8 are done (commits `920e445`, `a1ab48d`, `44b04cc`). Customer-aware pricing + visibility now flow through wholesale cart, quick-order rendering, and checkout staleness. **No tests exist yet** for any of the new surface — the migration plan calls for a fixture + assertion layer (W9) plus a test matrix (W10) before we ship behind the flag.

## Goal
Land **W9 + W10** from `docs/orderspace-migration-progress.md`. Output: testutil fixtures for the new pricing/visibility primitives + a focused test suite covering D2/D3/D4/D5 invariants. After this session, the only remaining work for cutover is W11 (admin UI) and the importer track (I1–I6).

## Read first
- `docs/orderspace-migration-plan.md` — the **"Test boundary"** subsection inside each of D2–D5, plus the cumulative test-list at the bottom of the contracts section ("Test boundary (cumulative across all five decisions)"). Those name the fixtures and tests expected.
- `docs/orderspace-migration-progress.md` — punch list + status snapshot. W5/W6/W7/W8 are now ticked; G1+G3 are noted closed.

## Heads-up before you start

1. **There is no `domain.PriceList` type.** Searching the codebase confirms `price_lists` exists as a table only — no Go domain struct, no store, no service. The plan implicitly assumed one. Two options:
   - **Recommended:** add a minimal `domain.PriceList { ID uuid.UUID; Name string; Status string }` struct only as far as fixtures need (a `CreatePriceList` helper that inserts into `price_lists` directly via a hand-written SQL or sqlc query and returns the ID/struct). Do NOT build a full `PriceListStore` / `PriceListService` here — that's W11 territory. Keep the fixture self-contained.
   - Alternative: have the fixture return just `uuid.UUID` (no domain type) and skip adding a domain struct. Cleaner if no test code needs the name/status. Pick this if you're not sure.
2. **`prices` table mixes base + group + price-list rows** in one table, distinguished by which of `customer_group_id` / `price_list_id` is non-null (both null = base). The existing `PricingStore.SetBasePrice` / `SetGroupPrice` cover base + group. There is **no** `SetPriceListPrice` store method yet; the closest path is to insert directly into `prices` from the fixture. Keep the fixture short — three `INSERT` lines via `tx.Exec` is fine and avoids new sqlc queries that real code doesn't need yet.
3. **Customer-group membership is m:m via `customer_group_memberships`**, but `Customer.CustomerGroupID` (a single nullable FK) is what the wholesale handler reads when building `groupIDs`. Tests should set the FK via `CustomerStore.UpdateCustomerGroup(...)` (already exists, store/customers.go:288) — the m:m table is unused on the read path today.
4. **`testutil.CreateCustomer` returns a customer with `AccountType = retail`** by default. Wholesale tests need to either approve them (`SetWholesaleApplicationFields` + `SetWholesaleApproved`) or directly stamp the row. There's a similar pattern in `internal/app/wholesale_test.go:73` (`TestWholesaleService_ApproveApplication`) that you can crib from.
5. **`TestMain` setup lives in `internal/app/setup_test.go`** — it spins up Postgres via testcontainers and exposes `testPool`. Re-use it; tests in this session are all `package app_test` files using `testPool` + `testutil.NewTestTx(t, pool)`.
6. **Test isolation:** every test gets a `pgx.Tx` from `testutil.NewTestTx(t, pool)` that auto-rolls back on cleanup. No need to tear down inserted rows.

## Scope

### W9 — testutil fixtures + assertions

Add to `internal/testutil/fixtures.go` (one section per primitive, alphabetised after the existing customer-group section):

```go
// --- Customer group ---

func CreateCustomerGroup(t *testing.T, tx pgx.Tx, name string) *domain.CustomerGroup
// Wraps sqlcgen.CreateCustomerGroup. Use a uuid suffix on `name` if empty.

// --- Price list ---

type PriceListOption func(*priceListInput) // local struct in fixtures.go
func WithPriceListStatus(status string) PriceListOption
func CreatePriceList(t *testing.T, tx pgx.Tx, opts ...PriceListOption) *domain.PriceList
// Direct INSERT into price_lists; default name = "Test PriceList <suffix>", status = "active".
// If you skip the domain struct entirely (heads-up #1), return uuid.UUID instead. Be consistent.

// CreatePriceListPrice writes a (price_set, price_list, amount) row.
// Auto-creates the price_set if one doesn't exist for variantID.
func CreatePriceListPrice(t *testing.T, tx pgx.Tx, priceListID, variantID uuid.UUID, amountCents int, currencyCode string) *domain.Price

// --- Visibility ---

func WithProductVisibility(v string) ProductOption // "public" | "wholesale" | "restricted"
// Sets sqlcgen.CreateProductParams.Visibility — verify the param exists; if not, follow up with an
// UPDATE products SET visibility = $1 in the fixture body after CreateProduct.

func AddProductGroupVisibility(t *testing.T, tx pgx.Tx, productID, customerGroupID uuid.UUID)
// Uses sqlcgen.AddProductGroupVisibility (already exists per invoices.sql.go:472 grep).

// --- Customer extensions ---

func WithPriceList(id uuid.UUID) CustomerOption
// Adds price_list_id to CreateCustomerParams. Verify the sqlc-generated param exists post-W3a.
// If the field isn't on CreateCustomerParams, do the assignment as a follow-up
// CustomerStore.UpdatePriceList(...) inside CreateCustomer. Don't regenerate sqlc here.

// --- Assertions ---

func AssertResolvedPrice(t *testing.T, wantCents, gotCents int) // simple int compare with named args
```

**Do not** create a separate `pricing_fixtures.go` file unless `fixtures.go` exceeds ~700 lines. Keep one fixtures file as the project does today.

### W10 — test cases

One file per concern, all in `package app_test`. Use `testPool` + `testutil.NewTestTx`.

#### `internal/app/pricing_test.go` (NEW)

Drives `PricingService.ResolveForCustomer` and `ResolveForCustomerBatch` (D2 + D5):

- `TestResolveForCustomer_BaseWhenNoList` — customer with `PriceListID == nil`, variant has base price → returns base.
- `TestResolveForCustomer_PriceListOverridesBase` — customer assigned to list X, variant has both base + list X price → returns list X.
- `TestResolveForCustomer_FallsBackToBaseWhenVariantMissingFromList` — customer assigned to list X, variant has base but no list X entry → returns base.
- `TestResolveForCustomer_ErrPriceNotFound` — customer assigned, variant has neither base nor list price → `ErrPriceNotFound`.
- `TestResolveForCustomer_ErrCustomerNotFound` — random uuid → `ErrCustomerNotFound`.
- `TestResolveForCustomerBatch_NoListUsesBase` — same as single but with 3 variants, no list assigned.
- `TestResolveForCustomerBatch_PriceListOverridesBase` — 3 variants, customer on list, all 3 have list prices.
- `TestResolveForCustomerBatch_VariantWithoutBasePrice` — 3 variants, one missing both list AND base entry → that variant key is absent from the returned map (not zero-valued, omitted).
- `TestResolveForCustomerBatch_MixedListAndBase` — customer on list, two variants have list prices, one only has base → all three returned, base used for the missing one.

#### `internal/app/cart_pricing_test.go` (NEW)

Drives `CartService.AddItemForCustomer` (D3) and `CartService.AddItem` (regression that retail still uses base):

- `TestAddItemForCustomer_UsesPriceListPrice` — customer on list, variant has list price → cart row's `unit_price` matches list.
- `TestAddItemForCustomer_FallsBackToBasePrice` — customer on list, variant has only base → cart row's `unit_price` matches base.
- `TestAddItemForCustomer_RetailCustomer` — customer with no list, variant has only base → uses base (sanity).
- `TestAddItem_StillUsesBasePrice` — retail path unchanged: `AddItem` (no customer arg) snapshots base regardless of any list rows present.
- `TestAddItemForCustomer_ErrInvalidQuantity` — quantity 0/-1 → sentinel.
- `TestAddItemForCustomer_ErrPriceNotFound` — variant with no base + customer with no list → sentinel.

#### `internal/app/wholesale_pricing_test.go` (NEW)

Drives `QuickOrderCatalog` (D4 + D5) and the wholesale order denormalization (D3):

- `TestQuickOrderCatalog_FiltersByGroup_RestrictedHidden` — product `restricted`, customer has no group membership → product absent.
- `TestQuickOrderCatalog_FiltersByGroup_RestrictedShownToMember` — product `restricted` + `product_group_visibility(group=G1)`, customer's `CustomerGroupID = G1` → product present.
- `TestQuickOrderCatalog_NoGroupShowsPublicAndWholesale` — three products: public, wholesale, restricted; customer with no group → first two visible, restricted hidden.
- `TestQuickOrderCatalog_PricesUsePriceList` — variants priced via list X; customer assigned to list X → returned `UnitPrice` matches list, not base.
- `TestQuickOrderCatalog_PricesFallBackToBase` — customer with no list → returned `UnitPrice` matches base.
- `TestPlaceWholesaleOrder_DenormalizesCartPrice` — call `PlaceWholesaleOrder` directly with `CartItem{UnitPrice: 999}` → assert created `LineItem.UnitPrice == 999` (proves the cart price is the contract; no internal re-resolve in `PlaceWholesaleOrder`).

#### `internal/app/wholesale_checkout_staleness_test.go` (NEW, optional but recommended)

The handler-level staleness flow lives in `internal/web/wholesale.go`, not the service layer — testing it end-to-end means standing up an `httptest.Server` with `Deps`, which is heavyweight for this codebase (no existing pattern). **Recommendation:** skip the handler test in this session. Cover the underlying invariants instead:

- `TestPlaceWholesaleOrder_LineItemPriceMatchesCartItemPrice` — already implied by D3 denormalization test above; one assertion suffices.
- The "rewrite cart row on stale" path is just `AddItemForCustomer` invoked again; that's covered by W10's cart tests.

If you really want a checkout test, add a service-layer helper that mirrors the staleness check (`func (s *PricingService) DetectStaleCartItems(ctx, tx, customerID, items []CartItem) (stale []CartItem, _ error)`) and test that — but only if the user explicitly asks, since it adds an API surface that the handler already inlines fine.

### Conventions to honor (from CLAUDE.md)
- One file per concern, `snake_case.go`. The three new test files map to three discrete services.
- Use `testify`'s `assert` for non-fatal checks, `require` for setup that must succeed.
- No mocks — every test hits the real Postgres tx via `testutil.NewTestTx`.
- Don't comment what the test does — the name is the doc. Add a comment only if the *why* is non-obvious (e.g., "this proves the cart price is the contract").
- Don't pre-add tests for W11 admin UI or importer — those land in their own sessions.

## Verify

```bash
mage test
go test ./internal/app/ -run TestResolveForCustomer -v
go test ./internal/app/ -run TestQuickOrderCatalog -v
```

No `mage templ` / `mage css` / `go build` needed for a tests-only session — but run `go build ./...` once at the end as a sanity check.

## When done
- Tick W9 and W10 boxes in `docs/orderspace-migration-progress.md`.
- Append a session-log row.
- Update the "Status snapshot" to note that the new pricing + visibility paths are now covered by tests.
- Delete this `NEXT.md`.
- Commit. Suggested message: `Add testutil fixtures + tests for customer-aware pricing/visibility (W9/W10)`.

## Out of scope (do NOT pull in)
- W11 (admin UI for assigning price lists / managing variant×list overrides / managing groups) — bigger session.
- Importer (`cmd/os-migrate`, I1–I6) — independent track.
- Retail tests for cart/checkout — only add a single regression that retail `AddItem` still uses base price; nothing more.
- A full `PriceListStore` / `PriceListService` — fixtures only need a thin `CreatePriceList` helper that inserts directly. Real CRUD lives in W11.
- Handler-level (`internal/web/wholesale.go`) tests — see the staleness test file note above.
- Volume-tier pricing — RR has zero tiers in OS, schema fields are dormant.
