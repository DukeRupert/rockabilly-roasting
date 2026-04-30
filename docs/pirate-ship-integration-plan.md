# Pirate Ship CSV Integration

## Goal

Round-trip orders between Hiri and Pirate Ship via spreadsheet. Staff export
unfulfilled orders to a CSV that drops cleanly into Pirate Ship's importer,
then upload Pirate Ship's tracking export back to Hiri to record shipments,
move orders to `shipped` fulfillment status, and notify customers.

## Scope

- Merchant-level shipping origin (added to `shipping_config`) plus a packaging
  tare weight in ounces
- CSV export of unfulfilled orders in Pirate-Ship-compatible format
- CSV import of Pirate Ship tracking results, persisted as `shipments` rows
  alongside any existing EasyPost-purchased shipments
- Admin UI affordances on the existing orders view

Out of scope: live rates at checkout, label purchase from Hiri, anything
involving the Pirate Ship API (they don't have one).

## Architectural fit

This feature lands inside Hiri's existing layered architecture (see
`CLAUDE.md`). Concretely:

- **`domain/`** — extend `ShippingConfig` with origin fields and tare weight;
  add a pure `CalculateShipmentWeightOz(items, productWeightsByVariant, tareOz)`
  function. No new domain types required for shipments — `domain.Shipment`
  already exists.
- **`store/`** — extend `ShippingStore.GetConfig`/`UpdateConfig` for new
  fields; add a `ListUnfulfilledForExport` query; reuse existing
  `CreateShipment` and `ListShipmentsByOrder`; add a small helper to update
  `orders.fulfillment_status`.
- **`app/`** — add an `app/shipping_export.go` (and/or extend
  `FulfillmentService`) that orchestrates: gather orders → compute weights →
  hand off to the format encoder. Add an import service that, per row, opens
  a tx, writes `shipments`, updates fulfillment status, records audit,
  enqueues the customer-shipped email job — all in the same transaction.
- **`platform/pirateship/`** — **new package**. Owns CSV column shape,
  encoding, decoding, and the `Provider` constant `"pirate_ship_csv"`. Mirrors
  how `platform/shipping/easypost.go` adapts EasyPost. No DB, no business
  logic — pure format conversion.
- **`web/`** — two thin handlers (export, import). Authorization via existing
  `PermUpdateFulfillment` middleware.
- **`ui/admin/`** — buttons + import form on the orders view; htmx-driven.
- **`jobs/`** — reuse the existing email-job infrastructure for the new
  `order_shipped` email (template needs to be built; see Open Questions).

Hiri is single-merchant by design (CLAUDE.md). There is no tenant scoping.

## Implementation Steps

Each step is independently testable and committable. Do not proceed to the
next step without explicit approval.

### Step 1 — Shipping origin + tare weight on `shipping_config`

Migration adds columns to the existing `shipping_config` single-row table:

```
origin_name      text
origin_street1   text
origin_street2   text
origin_city      text
origin_state     text
origin_zip       text
origin_country   text NOT NULL DEFAULT 'US'
tare_weight_oz   numeric NOT NULL DEFAULT 0
```

Origin is informational for now — Pirate Ship has its own origin config —
but we capture it once for future Shippo work. Tare weight is consumed by the
export to add packaging weight to every shipment.

`domain.ShippingConfig` gains the matching fields.
`ShippingStore.GetConfig` / `UpdateConfig` round-trip them.
Admin "Shipping settings" page gets fields for each.

Authorization: `PermUpdateFulfillment` (existing).

### Step 2 — Shipment weight calculation (pure)

Add to `internal/domain/shipping.go`:

```go
func CalculateShipmentWeightOz(
    items []LineItem,
    weightGramsByVariant map[uuid.UUID]*int, // nil = unknown weight
    tareOz float64,
) (float64, error)
```

Behavior:
- Skip line items whose variant has `RequiresShipping = false` (the digital
  exclusion). The caller is responsible for filtering or annotating these
  before passing in; the function operates on already-loaded data.
- For each remaining line item, multiply `WeightGrams * Quantity`, sum,
  convert grams → ounces, add `tareOz`.
- If any included variant has nil `WeightGrams`, return
  `ErrShipmentWeightUnknown` (new sentinel in `app/errors.go` is acceptable
  if domain-only sentinels are atypical here — pick one and stay consistent).

This is the unit-testable core. Tests cover: single physical item, multiple
items, mixed quantities, missing weight (error), digital item excluded, empty
order, tare-only calculation, gram-to-ounce conversion correctness.

**Unit reconciliation:** variant weights are stored in **grams**
(`Variant.WeightGrams`). Shipments are stored in **ounces**
(`shipments.weight_oz`) and the Pirate Ship CSV column is also ounces.
Conversion happens once, in this function.

### Step 3 — CSV export endpoint

`GET /admin/orders/export.csv`

Authorization: `PermUpdateFulfillment`.

Query params:
- `status` — defaults to orders where `fulfillment_status = 'unfulfilled'` and
  `payment_status IN ('captured', 'authorized')`. Confirm exact filter with
  the team during implementation; the goal is "paid and ready to ship."
- `ids` — optional comma-separated explicit order IDs (overrides status filter).

Behavior:
- Handler: parses params, calls
  `app.ShippingExportService.BuildPirateShipCSV(ctx, ...)`, which returns
  `(csvBytes []byte, skipped []SkippedOrder, err error)`.
- The service buffers the full result. Hiri's volume is one merchant's daily
  unfulfilled orders; in-memory buffering is fine and avoids the
  streaming-vs-skip-reporting conflict.
- Orders with `ErrShipmentWeightUnknown` are added to `skipped` with the
  reason; they do not appear in the CSV body.
- Handler writes:
  - `Content-Type: text/csv; charset=utf-8`
  - `Content-Disposition: attachment; filename="hiri-orders-{YYYY-MM-DD-HHMM}.csv"`
  - `X-Hiri-Export-Skipped-Count: N`
  - `X-Hiri-Export-Skipped-Order-Numbers: A-1001,A-1003` (cap at ~50; if
    more, set `X-Hiri-Export-Skipped-Truncated: true`)
- The admin UI surfaces skipped count via these headers (htmx attribute
  `hx-on::after-request` reads them) before triggering the file save.

No status side effects. The export does not change any order state.
Pirate Ship's tracking import (Step 5) is what records shipments.

CSV serialization lives in `platform/pirateship.Encode(rows []ExportRow) []byte`.
The handler/service do not write CSV bytes directly.

### Step 4 — Admin UI

On the existing admin orders view:

- "Export to Pirate Ship" button — GETs the Step 3 endpoint, displays
  skipped-count banner from response headers.
- "Import tracking from Pirate Ship" — file upload form posting to Step 5,
  result rendered inline as a summary table (updated / skipped / errors with
  per-row detail).

Both htmx-driven, server-rendered.

### Step 5 — Tracking import endpoint

`POST /admin/orders/import-tracking` — multipart form with a single CSV file.

Authorization: `PermUpdateFulfillment`.

Handler is thin:
1. Parse multipart, read file into memory (size cap, e.g. 10 MB).
2. Call `platform/pirateship.Decode(reader) ([]TrackingRow, error)`.
3. For each row, call
   `app.ShippingImportService.RecordPirateShipTracking(ctx, row, actor)`,
   which opens its own per-row transaction.
4. Render summary template.

Per-row service logic (single transaction per row, **not** per file):

1. `tx := store.BeginTx(...)`
2. Look up order by `Number`. If not found → skipped, reason `"order not found"`.
3. If `order.FulfillmentStatus` is `shipped`/`partially_shipped`/`delivered`/etc.
   (any value past `unfulfilled`/`partially_fulfilled`) → skipped, reason
   `"already shipped"`. This is the idempotency guard.
4. Insert row into `shipments` via `ShippingStore.CreateShipment` with:
   - `Provider = "pirate_ship_csv"` (new constant
     `domain.ShipmentProviderPirateShipCSV` in `domain/shipping.go`)
   - `Status = ShipmentStatusInTransit`
   - `TrackingNumber`, `CarrierName`, `ServiceName` from the CSV row
   - `LabelCostCents` from postage cost
   - `LabelCurrency = "USD"`
   - `WeightOz`, dimensions: zero (or nullable — see Data Model section)
   - `LabelURL`, `LabelR2Key`, `LabelFormat`: empty/nil (no label held by Hiri)
   - `ShippedAt` from CSV `Ship Date`
   - `CreatedBy = actor.ID`
5. Update `orders.fulfillment_status` to `shipped`. (Future enhancement:
   when imports cover only some line items, set `partially_shipped`. For v1,
   we set `shipped` and document the assumption that one Pirate Ship row =
   one fully-shipped order.) Do **not** touch `orders.status`.
6. `audit.Record(ctx, tx, ...)` with `AuditShipmentCreated` (existing) or a
   new `AuditShipmentImported` action — pick during implementation.
7. `river.InsertTx(ctx, tx, OrderShippedEmailArgs{OrderID, ShipmentID})` —
   the email job is enqueued in the same transaction as the writes.
8. `tx.Commit()`.
9. After commit, increment a Prometheus counter
   `hiri_pirateship_imports_total{result="success"}` (also `"skipped"`,
   `"error"`).

The email job fires the customer "your order has shipped" email. **This
template does not exist yet** (see Open Questions); building it is part of
Step 5 if not handled separately.

If the file has 200 rows and row 50 errors mid-write, rows 1–49 stay
committed, row 50 is reported as an error, rows 51–200 still attempt.

The handler is idempotent at the row level: re-uploading the same file is a
no-op because every order is now past `unfulfilled` and gets skipped.

## CSV Export Format

Owned by `platform/pirateship.Encode`. Headers in this exact order:

| Header     | Source                                          | Required |
|------------|-------------------------------------------------|----------|
| Order ID   | `orders.number` (the customer-facing identifier)| yes      |
| Name       | Shipping address first + last name, space-joined| yes      |
| Company    | Shipping address company, blank if absent       | no       |
| Address    | Shipping address line 1                         | yes      |
| Address 2  | Shipping address line 2, blank if absent        | no       |
| City       | Shipping city                                   | yes      |
| State      | Shipping state code                             | yes      |
| Zipcode    | Shipping postal code, preserved with leading zeros | yes   |
| Country    | Shipping country code, defaults to "US"         | yes      |
| Email      | Customer email                                  | no       |
| Weight     | Result of `CalculateShipmentWeightOz`, formatted to 2 dp | yes |
| Weight Unit| Literal string "oz"                             | yes      |
| Items      | Comma-separated SKUs of line items (Pirate Ship's Rubber Stamp feature) | no |

Encoding rules:
- File encoding: UTF-8 with BOM (Excel needs the BOM to render non-ASCII
  correctly when the user opens the file before re-uploading).
- Line endings: CRLF.
- Zipcode is a string; leading zeros preserved (Massachusetts zips). Quote
  the field if needed.
- Any field containing a comma, double quote, or newline is RFC-4180 quoted.

Filename convention: `hiri-orders-{YYYY-MM-DD-HHMM}.csv`.

## CSV Import Format (from Pirate Ship)

Owned by `platform/pirateship.Decode`. Pirate Ship exports a spreadsheet
that contains the original columns plus tracking columns it adds. Before
implementation, request a real export sample from Rockabilly to confirm
exact column names. Expected columns (case-insensitive header matching,
extra columns ignored):

- Order ID (round-tripped from the export)
- Tracking Number
- Carrier or Service (e.g. "USPS", "USPS Ground Advantage")
- Postage Cost (decimal dollars; converted to cents during decode)
- Ship Date (date)

If the Order ID column cannot be located, decode fails with a clear error
and the entire import is rejected before any writes happen.

## Data Model

**No new tables.** Pirate-Ship-imported shipments use the existing
`shipments` table with `provider = 'pirate_ship_csv'`, sitting alongside any
EasyPost-purchased shipments.

A migration is required because the current `shipments` schema (migration
`011_shipping.sql`) has NOT NULL constraints on columns Hiri didn't generate
for an imported row:

- `label_url` — make nullable (we have no label artifact)
- `length_in`, `width_in`, `height_in` — make nullable (Pirate Ship doesn't
  give us dimensions)
- `weight_oz` — keep NOT NULL; we always have it from the export (the
  customer round-trips it). For genuinely-unknown imports we'd store 0;
  acceptable for v1.

The migration should also update the `domain.Shipment` Go type accordingly
(`*float64` for nullable dimensions, `*string` for nullable `LabelURL`),
and adjust the existing EasyPost path to populate them as before — this is a
breaking change to the type, so callers need updating in the same change.

Alternative considered: leave the constraints, write empty strings/zeros for
imported rows. Rejected — using NULL preserves the semantic distinction
between "no label" and "label is the empty string."

## Status transitions on import

For v1, an imported tracking row sets:
- `orders.fulfillment_status = 'shipped'` (the order is fully shipped)
- `orders.status` is left alone (typically remains `processing` or whatever
  the order's current value is — the order moves to `complete` through a
  separate flow, e.g. delivery confirmation or admin action).

`partially_shipped` is supported by the schema but not by v1 of this
feature. If a future version of Pirate Ship's import or a manual workflow
needs to express "this label covers a subset of line items," that's a v2
addition with its own design.

## Authorization

Both endpoints sit behind the existing staff RBAC system in
`web/router.go`. The relevant permission is `PermUpdateFulfillment`
(`"orders:fulfill"`), held by the `admin` and `fulfillment` roles. No new
permissions required.

## Audit

Each successful import row writes one audit record — either reusing
`AuditShipmentCreated` (the EasyPost path uses `AuditShipmentLabelCreated`,
so a new value here may be cleaner) or adding `AuditShipmentImported` to
`platform/audit/actions.go`. Decide during implementation. The audit record
must be written inside the per-row transaction.

The export endpoint does not write audit records — it's a read.

## Email

Step 5 enqueues an `OrderShippedEmailArgs` River job (new), which composes
and sends the "your order has shipped" email via Postmark. The job:
- Looks up order, customer, shipment
- Renders a new `order_shipped.html` + `order_shipped.txt` template (does
  not currently exist in `internal/emailtemplates/`)
- Uses the existing `platform/email` provider abstraction
- Is idempotent: if the same job runs twice, the customer gets two emails
  unless we track sent-at on the shipment. Acceptable for v1; River
  unique-job constraints can address it if needed.

The template needs Rockabilly visual treatment (paper/ink/amber per the
design system in `Rockabilly Roasting Design System/`).

## Testing Strategy

- **Step 2 weight calculation:** unit tests, no HTTP or DB. Cover all
  enumerated cases including unit conversion.
- **Step 3 export:** integration test that hits the endpoint with a fixture
  order set, parses the CSV response, asserts column shape, BOM, CRLF, RFC
  4180 quoting on a comma-bearing address, and that skipped-count headers
  are accurate.
- **Step 5 import:** integration test with a fixture CSV exercising all
  paths (success, order-not-found, already-shipped) in one file. Assert that
  a `shipments` row was inserted, fulfillment_status updated, audit record
  written, and an email job enqueued — all in a single transaction.
- **Manual QA:** full round-trip with a Pirate Ship test account before
  shipping. Run one supervised round-trip with Rockabilly present.

## Rollout

Ship behind a feature flag (or just behind admin nav). Run one real
export-and-import cycle with the client present before considering it done.

## Open Questions

These are concrete decisions to be made during implementation, not
speculation deferred to the implementer:

1. **Audit action constant** — reuse `AuditShipmentLabelCreated`, reuse
   `AuditShipmentCreated`, or add `AuditShipmentImported`? Read existing
   constants in `platform/audit/actions.go` and pick.
2. **Sentinel error placement** — `domain.ErrShipmentWeightUnknown` (close
   to the type) or `app.ErrShipmentWeightUnknown` (consistent with other
   sentinels per CLAUDE.md)? Pick one.
3. **Email template visual design** — the `order_shipped.html` template
   needs to be designed against the Rockabilly design system. Either copy
   the structure of `order_confirm.html` and swap content, or treat it as a
   small design task in its own right.
4. **Pirate Ship export sample** — confirm exact CSV column names from a
   real Pirate Ship export with the client before finalizing
   `platform/pirateship.Decode`.
5. **Status filter for export default** — confirm the exact filter for
   "ready to ship" against the orders state machine. Likely
   `fulfillment_status = 'unfulfilled' AND payment_status IN ('captured',
   'authorized')`, but verify against existing admin filters to avoid drift.

## Progress

Steps 1–5 implemented. Step 5 work landed locally; commit pending.

**Commits:**
- `033311c` Step 1 — origin + tare on `shipping_config` (migration `039`,
  domain/store/app/web wiring, admin form fields, Rockabilly's address
  seeded).
- `9b74fa2` Step 2 — `app.CalculateShipmentWeightOz` (pure) +
  `ErrShipmentWeightUnknown` sentinel + 9 unit tests.
- `a340b50` Step 3 — `platform/pirateship.Encode`,
  `app.ShippingExportService`, `GET /admin/orders/export.csv`,
  `OrderFilter.PaymentStatuses`, encoder unit tests.
- `4edbe1f` Step 4a — Alpine-driven export button on the admin orders
  list with skipped-count banner from response headers.

**Deviations from this plan, locked in:**
1. **Sentinel + function landed in `app/`, not `domain/`.** Open Question
   #2 resolved: every existing `Err*` lives in `app/errors.go`, and
   `domain/` cannot import `app/` under the inward-flow rule. Co-locating
   `CalculateShipmentWeightOz` and `ErrShipmentWeightUnknown` in `app/`
   preserves both conventions. New file:
   `internal/app/shipping_weight.go`.
2. **No new `ListUnfulfilledForExport` store method.** Instead,
   `OrderFilter` gained `PaymentStatuses []domain.PaymentStatus` (added
   to both `ListOrders` and `CountOrders` builders). The export reuses
   `ListOrders` with the right filter. Reusable for future financial
   reports without inventing parallel queries.
3. **Variant + inventory caches are per-export-request, not bulk.** Hiri
   has no bulk variant fetch; the service caches by ID across orders so a
   shared SKU only hits the DB once per export. No bulk fetch was
   introduced.

**Deferred / not done:**
- **Step 3 integration test.** Encoder format (BOM, CRLF, RFC 4180,
  leading-zero zips) and weight math are unit-tested. The full-stack
  service test would need new fixtures (`WithWeightGrams`,
  `CreateInventoryItem`, `CreateLineItem`) — that's a fixture-extension
  task, not a Step 3 task. Add it alongside Step 5 fixture work.
- **Admin import form.** Step 4 shipped only the export button. The
  matching "Import tracking from Pirate Ship" upload form lives with
  Step 5 (it talks to the Step 5 endpoint).
- **Per-route role gating.** Existing `/orders/{id}/ship` and
  `/orders/{id}/fulfill` rely on `requireStaffSession` only; the export
  handler matches that pattern. When project-wide
  `PermUpdateFulfillment` enforcement lands, the export and import
  endpoints join in alongside. TODO comments point to the spots.
- **Origin field validation.** Country defaults to `US`, state/country
  are uppercased, all fields trimmed. No semantic validation of state
  codes, zip format, or country codes. Open Question for Step 3+ if a
  live-rate provider ever consumes the origin. TODO comment in
  `handleAdminShippingSettingsUpdate`.

**Decisions still open going into Step 5:**
- **Open Question #1 (audit action).** Recommend adding
  `AuditShipmentImported` rather than overloading
  `AuditShipmentLabelCreated` — the EasyPost path *creates* a label, the
  CSV path *imports* one Pirate Ship printed. Audit logs benefit from
  the distinction. Final call when implementing.
- **Open Question #3 (email template design).** New
  `order_shipped.html` + `order_shipped.txt` need Rockabilly visual
  treatment. Cheapest path: copy `order_confirm.html` structure, swap
  content (headline → "Your order's on the road"; show tracking number
  + carrier + estimated arrival; preserve the paper/ink/amber palette).
- **Open Question #4 (real Pirate Ship export sample).** Before
  finalizing `platform/pirateship.Decode`, request a real Pirate Ship
  tracking export from Rockabilly. Column names are best-guess until
  then.
- **Schema relaxation is a breaking change.** The
  `shipments.label_url` / `length_in` / `width_in` / `height_in` columns
  flip to nullable, and `domain.Shipment` shifts to pointer types.
  Existing EasyPost path in `internal/app/fulfillment.go:67-113` and
  `internal/store/shipping.go` need updating in the same change.
- **`pirateship.ProviderCSV = "pirate_ship_csv"`** already exists (added
  in Step 3 for the encoder package); use it in the import path's
  `CreateShipment` call.

**Step 5 work, as landed:**
1. Migration `040_shipments_nullable.sql` relaxes NOT NULL on
   `label_url` / `length_in` / `width_in` / `height_in`. `domain.Shipment`
   flipped to pointer types for those four fields; EasyPost callers in
   `internal/app/fulfillment.go` and the R2-label-store handler in
   `internal/web/admin_shipment.go` updated to populate / dereference
   pointers; `store/shipping.go` adds `numericToFloat64Ptr` /
   `float64PtrToNumeric` helpers. The `CreateShipment` query gained a
   `shipped_at` column so imports can record the carrier's ship date in
   one shot.
2. `platform/audit/actions.go` gained `AuditShipmentImported` and
   `AuditEmailOrderShipped`.
3. `platform/pirateship.Decode` — case-insensitive header matching,
   tolerates extra columns and ragged rows, accepts both ISO and US
   `M/D/YYYY` ship dates, parses dollar/cents costs with currency-symbol
   and comma stripping. `ErrMissingOrderIDColumn` is the one hard-fail.
4. New `OrderShippedEmailWorker` + `OrderShippedEmailArgs` (kind
   `email:order_shipped`), thin delegator over
   `OrderService.SendOrderShippedEmail`. The enqueue path goes through
   the existing `app.JobEnqueuer` interface (extended with
   `EnqueueOrderShipped`) to avoid the `app → jobs` import cycle.
5. New `order_shipped.html` + `order_shipped.txt` templates in the
   paper-and-ink design system, mirroring the masthead / double-rule /
   stamp-shadow rhythm of `lost_order_cutover.html`. Tracking-number
   stub renders carrier + service + tracking; the rust CTA renders only
   when a `TrackingURL` is present. `app.trackingURL(carrier, number)`
   maps USPS / UPS / FedEx / DHL to public tracking URLs; unknown
   carriers fall through and the CTA is hidden.
6. `ShippingImportService` (new file `internal/app/shipping_import.go`).
   `RecordPirateShipTracking` opens its own per-row transaction;
   `recordPirateShipTrackingInTx` is the testable core that takes a tx
   directly. Idempotency guard checks
   `canImportTrackingFor(fulfillment_status)` — anything past fulfilled
   is "already shipped" and skipped. Successful row writes shipment,
   flips fulfillment to `shipped`, records `AuditShipmentImported`, and
   enqueues `OrderShippedEmailArgs` — all in the same tx. Guest orders
   (`customer_id IS NULL`) skip the email but still record the
   shipment.
7. `POST /admin/orders/import-tracking` (handler:
   `internal/web/admin_shipping_import.go`). 10 MB upload cap; renders
   `admin.PirateShipImportSummary` inline. A missing-Order-ID column
   returns 400 with the same summary template populated as a single
   error row. Per-row results are bucketed into recorded / skipped /
   errored; per-row failures log via `slog.Error` for ops visibility.
8. UI: `admin.PirateShipImportButton` (popover with file input) +
   `admin.PirateShipImportSummary` (three-bucket table). Sits next to
   the existing export button on `/admin/orders`; htmx swaps the
   summary into `#pirate-ship-import-summary`.
9. Wired in `cmd/server/main.go`: `OrderService.WithShipments`,
   `OrderShippedEmailWorker` registration, `ShippingImportService`
   construction (after the enqueuer exists), and
   `Deps.ShippingImportService`.
10. Tests:
    - Decoder unit tests in `internal/platform/pirateship/decoder_test.go`
      (minimum columns, missing-Order-ID hard fail, case-insensitive
      headers, extra columns ignored, ragged rows, $-and-comma cost,
      US date format, blank rows skipped, empty file).
    - Integration tests in `internal/app/shipping_import_test.go`
      using the test-only `RecordPirateShipTrackingInTxForTest` export
      (file `shipping_import_export_test.go`): success path
      (shipment + fulfillment flip + audit + email-job enqueue all in
      one tx, dimensions stay NULL), order-not-found, already-shipped
      (no writes, status untouched), preflight skips, fulfillment
      status guard.
    - Email render tests in
      `internal/emailtemplates/renderer_test.go` for `order_shipped`
      (with and without tracking URL).
    - Carrier-URL helper tests in `internal/app/tracking_url_test.go`.

**Decisions made during implementation:**
- **Open Question #1** — added `AuditShipmentImported` (vs. reusing
  `AuditShipmentLabelCreated`). Audit logs benefit from distinguishing
  the two paths; the metadata also tags `"source": "pirate_ship_csv"`.
- **Open Question #3** — copied the `lost_order_cutover.html`
  structure for `order_shipped.html` (paper masthead, double rule,
  stamp-shadow tracking stub, rust CTA, ECE0C6 footer). The CTA URL
  is built by `app.trackingURL` based on the carrier name; unknown
  carriers degrade to text-only tracking.
- **Schema relaxation as separate per-row tx.** The plan called for
  per-row transactions; the import service opens its own tx and
  `recordPirateShipTrackingInTx` is exposed via `_test.go` test
  helper for unit-style integration tests inside the wrapping
  test-tx.
- **Prometheus counter.** `hiri_pirateship_imports_total{result=...}`
  registered alongside the other counters; incremented after the
  per-row tx commits or on skip/error.

**Deferred / not done:**
- **Per-route role gating.** Same as the export endpoint — the import
  handler currently relies on `requireStaffSession`. TODO comment
  flags the spot where `PermUpdateFulfillment` should attach.
- **Step 3 integration test.** Not addressed in Step 5; encoder/format
  unit tests still cover the file-shape contract.
- **`mage db:migrate` not run on local** — committing migration `040`
  along with the rest; deploy needs the migration applied before the
  binary rolls.
