# Next session — Orderspace migration W11 (admin UI for price-list management)

Session-handoff doc for the next claude run. Delete it when the work is landed.

## Why this exists

W1–W10 are done. Schema, services, handlers, and tests for customer-aware pricing + visibility all green. The remaining blocker for cutover is **admin UI**: there is currently no way for staff to assign a customer to a price list, edit a variant×price-list price, or create/manage price lists themselves. The importer (I1–I6) writes these rows fine — staff just can't manage them post-import.

W11 is the **largest remaining single piece**. Plan it carefully and split into commits.

## Read first

- `docs/orderspace-migration-progress.md` — punch list. W1–W10 are ticked. W11 status: not started.
- `docs/orderspace-migration-plan.md` §"Schema + code work" row W11.
- `internal/web/admin_groups.go` — the closest analog to what W11 needs to build. Especially:
  - `handleAdminGroupList` / `handleAdminGroupCreate` / `handleAdminGroupDelete` — group CRUD pattern (W11 needs the same for price lists).
  - `handleAdminGroupPrices` (lines 87–172) — the per-variant pricing matrix renderer. **This is the template for the price-list pricing matrix.**
- `internal/ui/admin/group_list.templ` and `group_pricing.templ` — templ pattern to mirror for price lists.
- `internal/web/admin_customers.go:145` (`handleAdminCustomerPaymentTerms`) — the on-the-customer-page dropdown pattern. The price-list selector follows this exact shape.

## Heads-up before you start

1. **There is no `PriceListStore` and no `PriceListService` yet.** They were skipped in W1–W10 because the testutil fixture inserts into `price_lists` directly. You need to build both layers in this session. Mirror `CustomerGroupStore` + `CustomerGroupService` — they're the closest analog (id + name + status, no nested resources).
2. **There is no audit constant for price-list create/update/delete.** Add three:
   - `AuditPriceListCreated  = "price_list.created"`
   - `AuditPriceListUpdated  = "price_list.updated"` (covers rename + status change)
   - `AuditPriceListDeleted  = "price_list.deleted"`
3. **`PricingService.SetPriceListPrice` does NOT exist.** Add it (sibling to `SetGroupPrice` / `SetBasePrice` at `internal/app/pricing.go:46`). Same shape — takes variantID, priceListID, amountCents, currencyCode; uses `GetOrCreatePriceSet` then writes a single row. Also add `DeletePriceListPrice` for the empty-input-clears-row UX path that `handleAdminGroupPriceGroupUpdate` uses (`admin_groups.go:236-250`).
4. **The customer's `price_list_id` is set via `CustomerService.UpdatePriceList` (already exists, audited).** The handler just needs a route + a dropdown on `customer_show.templ`. **Do not** rebuild the service layer — it's done.
5. **`PermManagePricing` is the right gate** for all three new admin surfaces (price-list CRUD + variant×list editing + per-customer assignment). Already mapped to admin/finance/catalog roles.
6. **Currency is hardcoded `"USD"`** throughout the existing admin UI. Keep it that way — multi-currency is explicitly out of scope per `orderspace-migration-plan.md`.
7. **Migration `043_customer_price_list_id.sql` is already applied.** No schema work in this session.
8. **Pre-existing dirty state** in the working tree (`internal/ui/storefront/*_templ.go`, `output.css`, `cmd/support-reply/main.go`, etc.) is NOT W11 work — leave it alone, stage only W11 files.

## Scope

Three discrete user surfaces, each with its own handler file and templ. Build in order — each commit should leave the suite green.

### Commit 1 — `PriceListStore` + `PriceListService` + sqlc queries

**New SQL queries** in `db/queries/pricing.sql` (or a new `db/queries/price_lists.sql` — your call, match what's idiomatic):
- `CreatePriceList(id, name, status)` returns the row.
- `GetPriceListByID(id)` returns the row.
- `ListPriceLists()` returns all rows ordered by name.
- `UpdatePriceList(id, name, status)` returns the updated row.
- `DeletePriceList(id)` — note the FK from `customers.price_list_id` is `ON DELETE SET NULL` and from `prices.price_list_id` is `ON DELETE CASCADE`, so a delete is safe.

**New store** `internal/store/price_lists.go`:
```go
type PriceListStore struct{}
func NewPriceListStore() *PriceListStore
func (s *PriceListStore) Create(ctx, tx, name, status) (*domain.PriceList, error)
func (s *PriceListStore) GetByID(ctx, tx, id) (*domain.PriceList, error)
func (s *PriceListStore) List(ctx, tx) ([]domain.PriceList, error)
func (s *PriceListStore) Update(ctx, tx, id, name, status) (*domain.PriceList, error)
func (s *PriceListStore) Delete(ctx, tx, id) error
```

**New service** `internal/app/price_lists.go` — mirror `CustomerGroupService` (`internal/app/customer_groups.go`):
```go
type PriceListService struct {
    lists *store.PriceListStore
    audit *audit.AuditWriter
    metrics *metrics.Registry
}
func NewPriceListService(...) *PriceListService
func (s *PriceListService) Create(ctx, tx, name, status, actor) (*domain.PriceList, error)
func (s *PriceListService) Get(ctx, tx, id) (*domain.PriceList, error) // returns ErrPriceListNotFound
func (s *PriceListService) List(ctx, tx) ([]domain.PriceList, error)
func (s *PriceListService) Update(ctx, tx, id, name, status, actor) (*domain.PriceList, error)
func (s *PriceListService) Delete(ctx, tx, id, actor) error
```

Add `ErrPriceListNotFound` to `internal/app/errors.go`. Add the three audit constants. Wire `priceListSvc` in `cmd/server/main.go`. Add `PriceListService *app.PriceListService` to `web.Deps`.

**Update fixture** `internal/testutil/fixtures.go`: replace the direct-SQL `CreatePriceList` body with a call to the new sqlc query so tests exercise the same path. (Optional if it makes the diff bigger than helpful — direct SQL still works; just leave a TODO.)

**Tests**: add `internal/app/price_lists_test.go` mirroring `customer_groups_test.go` if one exists, otherwise mirror `customers_test.go` shape. Cover Create, Update, Delete, List, and the not-found error.

### Commit 2 — `PricingService.SetPriceListPrice` + `DeletePriceListPrice`

**Store layer** `internal/store/pricing.go` — add:
```go
func (s *PricingStore) SetPriceListPrice(ctx, tx, priceSetID, priceListID, amount, currencyCode) (*domain.Price, error)
func (s *PricingStore) DeletePriceListPrice(ctx, tx, priceSetID, priceListID, currencyCode) error
```
+ sqlc queries `UpsertPriceListPrice` and `DeletePriceListPrice`. Pattern matches `UpsertGroupPrice` / `DeleteGroupPrice` at `pricing.sql.go:496-533`. Filter is `customer_group_id IS NULL AND min_quantity IS NULL`.

**Service layer** `internal/app/pricing.go` — add:
```go
func (s *PricingService) SetPriceListPrice(ctx, tx, variantID, priceListID, amountCents, currencyCode) (*domain.Price, error)
func (s *PricingService) DeletePriceListPrice(ctx, tx, variantID, priceListID, currencyCode) error
```
Mirror `SetGroupPrice` / `DeleteGroupPrice` at `pricing.go:81-110`.

**Tests**: extend `pricing_test.go` with `TestSetPriceListPrice_Inserts`, `TestSetPriceListPrice_OverwritesExisting`, `TestDeletePriceListPrice_RemovesRow`. The fixtures already cover the read side.

### Commit 3 — Admin price-list CRUD pages

**New handlers** `internal/web/admin_price_lists.go`:
- `handleAdminPriceListList` — `GET /admin/price-lists`. Lists all price lists with member counts.
- `handleAdminPriceListCreate` — `POST /admin/price-lists`. Form: name + status. Defaults status to "active".
- `handleAdminPriceListUpdate` — `POST /admin/price-lists/{id}`. Inline rename / status toggle.
- `handleAdminPriceListDelete` — `POST /admin/price-lists/{id}/delete`.

**New templ** `internal/ui/admin/price_list_list.templ`:
- `PriceListListProps`, `PriceListList(props)`, `PriceListListContent(props)`.
- Mirror the layout of `group_list.templ` (table with name + status + member count + delete button + create form at top).

**Routes** in `internal/web/router.go` — register the four routes under the existing `adminMux` block, gated by `PermManagePricing`. Match the position in the router where group CRUD lives (~line 320).

**Sidebar entry** — add a "Price lists" link to the admin sidebar. Find the existing entries in `internal/ui/admin/` (sidebar-related templ) and slot it in next to "Customer groups".

### Commit 4 — Variant×price-list pricing matrix page

This is the largest single page. Mirror `group_pricing.templ` / `handleAdminGroupPrices` exactly — but with `PriceList` columns instead of `CustomerGroup` columns.

**New handler** `internal/web/admin_price_lists.go` (same file as commit 3):
- `handleAdminPriceListPrices` — `GET /admin/price-lists/prices`. Loads all products, all variants, base prices, and all price-list prices, then renders the matrix.
- `handleAdminPriceListPriceUpdate` — `POST /admin/price-lists/prices/list`. Form: variant_id, price_list_id, price (dollars). Empty price = delete. Mirror `handleAdminGroupPriceGroupUpdate` precisely.

**New templ** `internal/ui/admin/price_list_pricing.templ`:
- `PriceListPricingProps { Products []ProductPricingGroup, Lists []domain.PriceList, ... }`.
- Same row-per-variant layout as `group_pricing.templ`. Each row has: SKU + base price (read-only here — base is edited on the group-pricing page) + one `<input>` per price list.
- Use the existing `VariantPricing` / `ProductPricingGroup` admin types where possible — extend with a `ListPrices map[uuid.UUID]int` field if the per-list shape doesn't already exist.

**Tests** — none required for the handler layer (no existing pattern in this codebase). Service-layer tests in commit 2 cover the invariants.

### Commit 5 — Customer-page price-list dropdown

**Edit** `internal/ui/admin/customer_show.templ`. In the "Billing Settings" section (line 237+), after the "Payment terms" `<dd>` block (~line 270) insert a sibling block for "Price list":

```templ
<div class="px-4 py-4 sm:grid sm:grid-cols-3 sm:gap-4 sm:px-6">
    <dt class="text-sm font-medium text-rr-muted">Price list</dt>
    <dd class="mt-1 sm:col-span-2 sm:mt-0">
        <form method="post" action={ templ.SafeURL(fmt.Sprintf("/admin/customers/%s/price-list", props.Customer.ID)) } class="flex items-center gap-2">
            <select name="price_list_id" onchange="this.form.submit()" class="...">
                <option value="" selected?={ props.Customer.PriceListID == nil }>Base prices</option>
                for _, pl := range props.PriceLists {
                    <option value={ pl.ID.String() } selected?={ props.Customer.PriceListID != nil && *props.Customer.PriceListID == pl.ID }>{ pl.Name }</option>
                }
            </select>
            ...chevron svg...
        </form>
    </dd>
</div>
```

Add `PriceLists []domain.PriceList` to `CustomerShowProps`. Populate from `d.PriceListService.List(ctx, tx)` in `handleAdminCustomerShow` (`internal/web/admin_customers.go`).

**New handler** `handleAdminCustomerPriceList` in `internal/web/admin_customers.go`:
```go
// POST /admin/customers/{id}/price-list  body: price_list_id (UUID, "" clears)
```
Mirror `handleAdminCustomerPaymentTerms` at line 145. Parse the form, optional UUID, call `d.CustomerService.UpdatePriceList` (already exists), redirect.

**Route** in `internal/web/router.go` next to `payment-terms`:
```go
adminMux.HandleFunc("POST /admin/customers/{id}/price-list", deps.handleAdminCustomerPriceList)
```

## Verify

```bash
mage generate    # regenerate sqlc bindings (commits 1-2 add queries)
mage templ       # regenerate templ (commits 3-5 add templates)
mage test        # all tests still green
go build ./...   # final sanity
mage dev         # spot-check the three pages locally — see "UI smoke test" below
```

### UI smoke test (do this; do NOT skip)

1. `mage dev` and log into `/admin` as the seeded admin user.
2. Visit `/admin/price-lists`. Create a list named "Test 2026". Confirm it appears.
3. Visit `/admin/price-lists/prices`. Set a non-zero price for one variant on "Test 2026". Reload — value persists. Clear the input — value goes back to base.
4. Visit a wholesale customer's detail page. Use the "Price list" dropdown to assign "Test 2026". Reload — selection persists.
5. Log into the wholesale storefront as that customer. Visit the quick-order page. Confirm the price reflects the test list, not base.
6. Delete "Test 2026" from `/admin/price-lists`. The customer's `price_list_id` should null out (FK is `ON DELETE SET NULL`); subsequent quick-order page should fall back to base.

If the dev server isn't ergonomic to test locally, **say so explicitly** in the session report — do not claim the UI works without checking.

## When done

- Tick W11 in `docs/orderspace-migration-progress.md`. Update "Status snapshot" with what's now wired.
- Append a session-log row.
- Delete this `NEXT.md`.
- Suggested commit message for the final commit: `Add admin UI for price-list assignment + variant×list overrides (W11)`.

## Out of scope (do NOT pull in)

- Admin sidebar reskin to paper-and-ink — that's a separate cosmetic track per the migration plan's "Out of scope" section.
- Volume-tier pricing UI — RR has zero tiers in OS; schema fields stay dormant.
- Importer (I1–I6) — independent track. **Don't run it; W11's job is to make the system manageable post-import, not to do the import itself.**
- Multi-currency support.
- Bulk-edit price-list prices via CSV upload — single-cell editing matches the existing group-pricing UX.
- Storefront changes — wholesale storefront already reads through `ResolveForCustomerBatch` (W6/W8); no UI work needed there.
