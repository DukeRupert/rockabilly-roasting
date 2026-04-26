# Admin charts

Reference for the dashboard / report charts used in the admin UI.

## Where they live

- **Partials:** `internal/ui/admin/charts/charts.templ` — package `charts`
- **Data shapes:** `charts.ChartPoint`, `charts.LabeledValue` (in the same file)
- **Domain types backing the data:** `internal/domain/order.go` — e.g. `DailyRevenue`, `ProductSales`
- **Aggregation queries:** `internal/store/orders.go` — e.g. `RevenueByDay`, `TopProductsByUnits`
- **Service pass-throughs:** `internal/app/orders.go`
- **Wiring + label/magnitude prep:** `internal/web/admin.go` — `buildRevenueTrend`, `buildTopProducts`, `compactDollars`

## The contract

Charts are pure rendering. They never compute, look up data, or know about
business types. The caller computes purpose-shaped data, normalizes magnitudes
to `[0, 1]`, and pre-formats every label string. This keeps the partials
trivially reusable across any future report.

```go
type ChartPoint struct {
    Label     string  // x-axis label, pre-formatted (e.g. "Apr 22", "Today")
    Sub       string  // value label above the bar (optional)
    Magnitude float64 // bar height as fraction of available area, 0..1
    Highlight bool    // render bar in accent (rust) instead of ink
}

type LabeledValue struct {
    Label     string  // row label (e.g. product title)
    Sub       string  // secondary value label, e.g. "$1.2k"
    Value     string  // primary value label, e.g. "248 units"
    Magnitude float64 // bar width as fraction of available area, 0..1
}
```

Available partials:

- `@charts.BarVertical(data []ChartPoint)` — time-series bars
- `@charts.BarHorizontal(data []LabeledValue)` — ranked rows

Both render an empty-state ("No data.") if the slice is empty.

## Adding a new chart

1. **Add the domain type** in `internal/domain/<area>.go` — pure value type.
2. **Add the aggregation query** to the relevant `*Store` in `internal/store/`,
   following the `RevenueByDay` / `TopProductsByUnits` shape:
   - Take a `pgx.Tx` and `time.Time` window
   - Exclude cancelled/refunded orders where applicable
   - Use `(placed_at AT TIME ZONE $tz)::date` for day bucketing in merchant TZ
3. **Add a thin pass-through** to the service (`internal/app/<area>.go`) — no
   transactional concerns, just delegation.
4. **Compute display data in the handler** — fill any gaps (e.g. zero-order
   days), find the max for normalization, format every label string. Use
   `compactDollars` for tight chart labels (`$1.2k`), `formatCents` elsewhere.
5. **Render** with `@charts.BarVertical` or `@charts.BarHorizontal`.

A chart panel typically lives in a stamp-shadowed card:

```templ
<div class="border-2 border-rr-border bg-rr-surface px-6 py-5"
     style="box-shadow: 4px 4px 0 0 var(--color-rr-border);">
    <p class="label-font text-rr-muted">Panel title</p>
    <div class="mt-4">
        @charts.BarVertical(props.MyData)
    </div>
</div>
```

## When to graduate to a JS library

Server-rendered SVG works for static bar charts. Bring in a library
(**ECharts** or **ApexCharts**) when you need any of:

- Tooltips with hover context
- Click-to-drill-down or click-to-filter
- Zoomable time series
- Stacked bars with toggleable series
- Sparklines inside table rows
- Multi-axis charts

When that day comes:

- Load the library via `<script>` only on the report pages that need it
  (admin is server-rendered, no frontend build pipeline).
- Keep the data-shape conventions: handlers compute purpose-shaped JSON,
  templates render `<div data-chart='{...}'>`, a tiny init script picks up
  those nodes. The Magnitude/Label/Sub abstraction does not need to change.
- Theme overrides: square corners, hard offset shadow, no grid lines, no
  default pastel palette. Use `--color-rr-heading` for ink and
  `--color-rr-red` for accents.

## Brand / style notes

- Bars: solid ink (`fill-rr-heading`) by default; rust (`fill-rr-red`) for the
  highlight bar (e.g. "today").
- Bar value labels (`Sub`): Special Elite typewriter face, muted ink.
- Empty-bar background: `bg-rr-raised` (paper-warm) with 1px ink border.
- No drop shadows on bars themselves; the *card* gets the 4px stamp.
- Charts must fit the paper-and-ink palette on admin and the dark-mode tokens
  on storefront — both pages use the same `--color-rr-*` token names, so the
  fills work in either context.
