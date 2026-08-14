# Hiri Route Optimization — Implementation Plan

**Feature:** Delivery route planning for unfulfilled local-delivery orders
**Service area:** Tri-Cities, Washington (Kennewick / Pasco / Richland)
**Status:** Spec — reconciled against the codebase 2026-08-14, ready for implementation

> Revision note: this document was originally written against assumptions that
> did not match Hiri (multi-tenant, sqlc, a route-order filter that needed
> inventing). Section 8 records what changed and why, so a reader who saw the
> first draft knows which parts moved.

---

## 1. Purpose

Rockabilly currently exports delivery addresses by hand into a third-party
routing app (MyWay) every delivery day. This feature replaces that workflow
inside Hiri:

1. Gather addresses from the local-delivery orders already queued for a run.
2. Compute an optimized stop order (traveling-salesman style) on our own infra.
3. Hand the ordered route to the driver's phone via native maps deep links and a
   QR code — no third-party app, no per-seat subscription, no manual data entry.

This is the **third leg of the delivery-day workflow**, joining two shipped
features: the Mon/Thu 9am delivery cutoff with `scheduled_delivery_date`
(migration 064) and the pounds-per-coffee **Load list** (v1.94.0). The load list
tells the driver *what to put in the van*; this tells them *what order to drive
it in*. It belongs on the same screen.

**Non-goals (v1):** live driver tracking, multi-driver route splitting, time
windows, proof-of-delivery photos. Design should not preclude these, but do not
build them.

---

## 2. Architecture Overview

```
┌─────────────────────────── Hiri (Go) ───────────────────────────┐
│                                                                  │
│  Admin: Load list tab            Driver page (templ/htmx, mobile)│
│  "Plan route" action             ordered stops, nav links,       │
│        │                         delivered toggles, QR           │
│        ▼                                ▲                        │
│  app.RouteService ──────────────────────┘                        │
│    │            │                                                │
│    ▼            ▼                                                │
│  Geocoder    OSRM client                                         │
│  (cached)    (HTTP, internal network)                            │
└─────┼────────────┼───────────────────────────────────────────────┘
      │            │
      ▼            ▼
 Google         OSRM container
 Geocoding API  (Docker, Washington OSM extract,
 (cache-first)   /trip endpoint, no external deps)
```

**Division of labor — critical to understand:**

- **OSRM** (self-hosted) decides the *order* of stops. It never talks to
  Google/Apple Maps.
- **Google Maps / Apple Maps** on the driver's phone provide turn-by-turn
  navigation via **URL schemes only** — no API, no key, no SDK. They receive
  stops in our order and do not reorder them.
- **Google Geocoding API** is the only paid external call: address → lat/lng,
  required as OSRM input. Aggressively cached; repeat customers geocode once.

### External services

| Service | Role | Hosting | Cost |
|---|---|---|---|
| OSRM | Stop-order optimization (`/trip`) | Docker container on the Hetzner VPS | Free |
| Google Geocoding API | Address → coordinates | External, cache-first | ~$5/1k uncached; pennies/month after warm-up |
| Google Maps app | Driver navigation, multi-stop | Driver's phone, URL scheme | Free |
| Apple Maps app | Driver navigation, single-stop | Driver's phone, URL scheme | Free |

**Decision (2026-08-14):** Google Geocoding, as originally planned — accuracy on
apartment and rural-route addresses is worth the key. The `Geocoder` interface
keeps a swap to the free US Census geocoder cheap if billing ever annoys.

### OSRM deployment

- Image: `ghcr.io/project-osrm/osrm-backend`
- Data: **Geofabrik Washington extract** (`washington-latest.osm.pbf`, **344MB**
  as of 2026-08-13)
- Pipeline: `osrm-extract` (car profile) → `osrm-partition` → `osrm-customize`
- Runtime: `osrm-routed --algorithm mld`, bound to the internal Docker network
  only — **not** exposed publicly

#### Resource sizing

Two very different numbers, and conflating them is what oversizes the box:

Measured 2026-08-14 on the real hosts (estimates in the first draft were close
on the build side and far too pessimistic on the serving side):

| Phase | Frequency | Peak RAM | Disk |
|---|---|---|---|
| `osrm-extract` (car profile) | Once at deploy, then quarterly | **4.29GB** | 344MB pbf in |
| `osrm-partition` + `osrm-customize` | Same | 1.07GB | 1.5GB of `.osrm.*` out |
| `osrm-routed --algorithm mld --mmap` | **Always on** | **69MB RSS** (data sits in reclaimable page cache) | reads the `.osrm.*` set |

Preprocessing is the spike; serving is cheap. So:

- **Steady state needs ~2GB free** on top of Postgres and Hiri, and ~4GB of disk.
- **Preprocessing needs ~5GB free**, but only for the ~10–20 minutes it runs,
  four times a year.

#### Measured prod capacity (2026-08-14)

```
Mem:  3.7Gi total, 728Mi used, 2.8Gi buff/cache, 3.0Gi available, Swap: 0B
Disk: 75G total, 40G available on /
```

**Decision: preprocess off-box. Not optional.** A ~5GB `osrm-extract` peak against
3.0GB available with **zero swap** is an OOM kill, and the OOM killer on that box
would as likely take Postgres or Hiri as it would take OSRM. Adding swap doesn't
rescue it either — spilling gigabytes of a random-access graph build onto disk
turns a 15-minute job into an hours-long one while the storefront sits behind a
thrashing page cache. Disk is not a constraint (40G free vs ~4G needed).

So the pipeline splits across two machines:

- **Build — `angmar.dev`** (7.6GB RAM, 4.9GB available, 4GB swap, 108G free;
  Hetzner Hillsboro OR). A Mage target runs `osrm-extract` → `osrm-partition` →
  `osrm-customize` there, tars the resulting `.osrm.*` set, and `scp`s it to prod.
  angmar has the headroom for the ~5GB peak and serves nothing customer-facing,
  so a few minutes of memory pressure four times a year costs nothing.
- **Serve — prod.** The OSRM container unpacks the prepared tarball into a volume
  and runs **only** `osrm-routed --algorithm mld --mmap`. `--mmap` lets the kernel
  page the dataset in on demand rather than loading it all into RSS, which matters
  on a box where 2.8GB of RAM is currently doing useful work as Postgres page
  cache.
- **Quarterly refresh** is a re-run of the build target plus a container restart —
  no memory pressure on prod at any point.

**Why not run `osrm-routed` on angmar and have prod call it?** The two boxes are
in different Hetzner locations — prod is Ashburn VA (`5.161.245.139`), angmar is
Hillsboro OR (`5.78.89.141`) — so Hetzner's free private networking, which is
location-bound, does not apply. Cross-host would mean a WireGuard/Tailscale tunnel
across the country (OSRM ships with **no authentication of any kind** and must
never face the public internet), and it would make delivery-day route planning
depend on a second machine in another state being reachable. Latency itself is a
non-issue — one `/trip` call per planning action — but the availability coupling
and the tunnel are real costs for no benefit. Serving locally on prod is ~1GB.

A dedicated OSRM box in Ashburn (same location → free private network, and it
could preprocess its own data on 8GB) was considered and declined: it costs real
money monthly to avoid one `scp`, and it would still need a tunnel or a strict
firewall bind, since Hetzner does not encrypt intra-datacenter traffic and OSRM
query strings carry customer delivery coordinates. Revisit if the service area
widens or multi-driver splitting arrives.

Budget on prod after this: ~1GB of additional resident memory for `osrm-routed`,
leaving ~2GB headroom. Comfortable, but worth a `free -h` check after the first
week — if Postgres cache pressure shows up, the next move is a RAM bump, not a
config tweak.
- Add to the production compose file; healthcheck hitting `/nearest` with a
  fixed Tri-Cities coordinate
- Document a quarterly data-refresh procedure (re-download extract, re-run
  pipeline) in `docs/operations.md`

Key OSRM call:

```
GET /trip/v1/driving/{lng1},{lat1};{lng2},{lat2};...
    ?source=first&roundtrip=true
```

- **Coordinates are lng,lat order** (OSRM convention — easy to get backwards)
- First coordinate = the roastery (route origin); `source=first` pins it
- `roundtrip=true` for v1 (driver returns to shop)
- Response `waypoints[].waypoint_index` gives the optimized visiting order;
  `trips[0]` gives total duration/distance

---

## 3. Codebase Facts This Plan Depends On

Verified 2026-08-14. An implementer should re-check any of these that look stale.

### Hiri is single-merchant — there is no tenant scoping

No table carries a `tenant_id`. New tables must not invent one, and there is no
tenant-isolation test to write.

### Persistence conventions

- **Migrations:** goose, plain SQL, `db/migrations/`. Next free number is **066**
  (065 is `price_tiers`).
- **Queries:** hand-written `pgx` in `internal/store/`, one file per domain area,
  every function taking `pgx.Tx`. `internal/store/sqlcgen/` exists but is
  legacy — **do not add sqlc queries for this feature.**

### The order filter already exists

"Unfulfilled local delivery" is not a new concept to define — it is the Load
list roster, at `internal/web/admin_fulfillment_load_list.go:81`:

```go
store.OrderFilter{
    Channel:                  &channel,
    ShippingMethod:           &domain.ShippingMethodLocalDelivery,
    FulfillmentStatuses:      fulfillmentNeedsActionStatuses,
    ExcludeUnconfirmed:       true,
    ExcludeCancelledRefunded: true,
    Limit:                    loadListRosterCap, // 500
}
```

The route planner reuses this filter with `Channel: nil` (see below).

### Routes span both channels

**Decision (2026-08-14):** one route covers retail *and* wholesale. The van makes
one run; a cafe and a house on the same street should be adjacent stops. The
existing Load list is channel-split (separate retail and wholesale tabs) — the
route is not. Each stop row shows a retail/wholesale badge so the driver knows
whether they're handing over two bags or six.

### The roastery origin already exists

`shipping_config` carries `origin_name / origin_street1 / origin_street2 /
origin_city / origin_state / origin_zip / origin_country / origin_email /
origin_phone` (used today for EasyPost labels), read via
`CheckoutService.GetShippingConfig`. Geocode that address once and cache it —
**no new roastery setting is needed.** This deletes most of the original Step 7.

### Delivery date

Orders already carry `ScheduledDeliveryDate *time.Time` (migration 064, the
Mon/Thu 9am cutoff). A route's `route_date` is a scheduled delivery date, not
"today", and the planner should default to the next run's date.

### Signed-link precedent for the driver page

`internal/platform/auth/order_action.go` + `ORDER_ACTION_SECRET` is the HMAC
signed-link infrastructure behind the switch-to-pickup email link. The driver
page still gets a **stored** `share_token` (so it can be revoked when the route
completes, which a stateless HMAC can't do), but copy the URL handling, expiry
checks, and problem-page rendering from `internal/web/switch_to_pickup.go`.

### Marking a local delivery delivered needs a new transition

This is the one real gap. Existing paths don't fit:

- `MarkOrderDelivered` (`internal/app/orders.go:882`) requires
  `shipped`/`partially_shipped` — a local delivery is never "shipped".
- `ReconcileDelivery` is driven by carrier tracking rows — none exist here.
- `MarkPickedUp` (`internal/app/orders.go:834`) is the closest shape:
  `ready_for_pickup` → `delivered` + `complete`, audited, no email.

So Step 5 adds `OrderService.MarkLocallyDelivered(ctx, tx, id, actor)` modeled on
`MarkPickedUp`: guarded to the needs-action fulfillment statuses, sets
`fulfillment_status=delivered` and `status=complete`, records a new
`AuditOrderDelivered`-family action with `"source": "driver_route"`. Guard so a
double-tap is a no-op (`ErrInvalidOrderStatus` treated as "already handled").

### Permissions

There is no route-specific permission. Admin route screens sit behind the
existing `PermUpdateFulfillment` (`orders:fulfill`) — admin + fulfillment roles,
the same gate as the Load list. Do not add a new permission constant.

### Frontend

templ + htmx + Alpine + Tailwind. Admin screens follow `docs/admin-ui.md` (read
it before writing admin UI; `mage checkAdminUI` enforces the banned-class list).
The **driver page is a storefront-side page, not admin** — it is unauthenticated,
mobile-first, and can use the full paper-and-ink treatment.

---

## 4. Core Entities

### `geocoded_addresses` (cache)

| Column | Type | Notes |
|---|---|---|
| id | uuid pk | |
| normalized_address | text, unique | lowercased, whitespace-collapsed, USPS-style abbreviation normalization; the cache key |
| raw_address | text | as last seen on an order |
| lat | double precision | |
| lng | double precision | |
| provider | text | `google` |
| confidence | text | Google `location_type` (ROOFTOP, RANGE_INTERPOLATED, …) |
| geocoded_at | timestamptz | |

Cache-first lookup: normalize → select → on miss, call Google, insert, return.
Results worse than `RANGE_INTERPOLATED` are flagged to the admin at plan time
rather than silently routed.

**Transaction rule:** the Google call is an external service call and must
happen **outside** any transaction — read the cache in one tx, call Google, write
the row in its own tx. This is the `RenewalService` two-phase pattern; violating
it is the single easiest way to fail review on this feature.

### `delivery_routes`

| Column | Type | Notes |
|---|---|---|
| id | uuid pk | |
| route_date | date | the `scheduled_delivery_date` this route covers |
| status | text | `draft` → `active` → `completed` |
| origin_lat / origin_lng | double precision | roastery, geocoded from `shipping_config` |
| total_distance_m | int | from OSRM |
| total_duration_s | int | from OSRM |
| share_token | text, unique | random, URL-safe; authenticates the driver page without a login |
| created_at / completed_at | timestamptz | |

No `tenant_id`. One active route per `route_date` — enforce with a partial unique
index on `(route_date) WHERE status <> 'completed'` so re-planning replaces
rather than accumulates.

### `route_stops`

| Column | Type | Notes |
|---|---|---|
| id | uuid pk | |
| route_id | uuid fk | |
| order_id | uuid fk | link back to the order |
| position | int | 1-based optimized order from OSRM |
| address | text | display address |
| lat / lng | double precision | passed to maps URLs — exact pins, no re-geocoding drift |
| status | text | `pending` → `delivered` (or `skipped`) |
| delivered_at | timestamptz null | |
| notes | text | delivery instructions from `orders.notes`, if any |
| skip_reason | text | free text, captured when the driver skips |

**Skip semantics (decided 2026-08-14):** `skipped` means *the driver had good
reason to drive past this stop today* — a mistake on the order, nobody home with
a signature-required drop, an address that turned out wrong. It is a
**route-level** outcome only:

- The **order is untouched.** It stays in its current fulfillment status, stays
  in the Load list roster, and rolls onto the next run's route automatically —
  no separate re-queue step, because the route is derived from the order queue
  rather than the other way round.
- Skipping does **not** call `MarkLocallyDelivered` and writes no order audit
  record. The audit trail is on the route.
- The driver is prompted for a short reason; it surfaces on the admin route view
  so staff can fix the underlying problem (correct the order, call the customer)
  before the next run.
- A route with skipped stops can still auto-complete — "all stops resolved"
  means every stop is `delivered` **or** `skipped`.

**Fulfillment linkage:** marking a stop delivered calls
`MarkLocallyDelivered` — one source of truth, not a parallel status. The
`route_stops.status` column is a UI convenience that follows the order, never
leads it.

---

## 5. Driver Handoff Mechanics

### Delivery day, end to end

Nothing is installed on the driver's phone. The whole handoff is one scan.

1. **Packout (staff, in admin).** Open the Load list tab as they do today, check
   the orders going out, hit **Plan route**. Hiri geocodes (cache-warm, so
   instant for regulars), calls OSRM, and shows the ordered stop list with a
   total drive time. Staff drop any stop that shouldn't go and re-plan.
2. **Activate.** Generates the `share_token` and renders a **QR code** next to
   the printable load sheet. The QR encodes `https://…/routes/{share_token}`.
3. **Handoff.** The driver scans the QR with their phone camera — iOS and Android
   both open URLs straight from the camera app, no scanner app needed. The
   driver page opens in their browser. That is the entire transfer: no login, no
   account, no app install, no typing an address. If the driver prefers, the same
   URL can be texted to them (it's just a link) — worth adding only if the QR
   proves awkward in practice.
4. **Driving.** The driver page is the checklist; the phone's own maps app is the
   navigation. Tapping **Navigate** on a stop opens Google or Apple Maps with a
   `lat,lng` pin — an OS-level handoff via URL scheme, no API and no key. **Navigate
   all** does the same with up to 10 stops at once. Maps takes over turn-by-turn;
   the driver switches back to the Hiri tab (still loaded) to check the stop off.
5. **Per stop.** Mark **Delivered** (htmx POST → `MarkLocallyDelivered`, so the
   order goes delivered/complete in Hiri in real time — staff watching the
   fulfillment queue see it land) or **Skip** with a reason.
6. **End of run.** Route auto-completes once every stop is resolved; the
   `share_token` stops working at that moment.

The one hard requirement on the driver's phone is a data connection — the page is
server-rendered on each interaction and there is no offline mode in v1. Tri-Cities
coverage makes that a safe assumption; a dead-zone fallback is the printed load
sheet they already carry.

### Driver page (primary interface)

Mobile-first templ page at `/routes/{share_token}` — token-authenticated, no
login, registered on the **public** mux (not `adminMux`). Shows:

- Stops in optimized order, current stop highlighted
- Per-stop: address, customer name, order summary, channel badge, notes,
  **Navigate** button, **Delivered** toggle (htmx POST, optimistic UI via Alpine)
- **Navigate all (Google Maps)** button at top — chunked multi-stop deep link
- Progress indicator (e.g., 4 of 12 delivered)

Token lifetime: **invalid once the route is `completed`** (open question #5,
resolved). No time-boxing on top — a route that runs long shouldn't lock the
driver out mid-run.

Admin gets a QR code encoding the driver page URL. The driver scans once at the
start of the day; everything else happens from their phone.

### Maps URL schemes

**Google Maps multi-stop** (cap: ~9 waypoints + 1 destination = 10 stops/link):

```
https://www.google.com/maps/dir/?api=1
  &origin={lat},{lng}              ← roastery, or omit for Current Location
  &destination={lat},{lng}          ← last stop in chunk
  &waypoints={lat},{lng}|{lat},{lng}|...
  &travelmode=driving
```

**Apple Maps single-stop** (no multi-stop URL support):

```
https://maps.apple.com/?daddr={lat},{lng}&dirflg=d
```

Rules:

- **Always pass lat/lng, never addresses** — prevents the maps app re-geocoding
  to a different pin than we planned around.
- **Chunking:** routes >10 stops split into sequential Google Maps links
  ("Navigate stops 1–10", "Navigate stops 11–18"). Chunk N's destination = its
  last stop; chunk N+1's origin omitted (Current Location picks up from there).
- Per-stop Navigate offers both schemes (or detects platform via user agent and
  shows the likely one with a fallback link).

### QR code

Generate server-side with `github.com/skip2/go-qrcode` (new dependency — not
currently in `go.mod`), encoding the driver page URL. Render as a PNG in the
admin route view, printable alongside the existing load sheet.

---

## 6. Implementation Steps

Each step is independently testable and committable. **Stop after each step for
review.**

### Step 1 — Geocoding service + cache ✅ done 2026-08-14
Migration 066, `platform/geocode`, `app.GeocodingService`, `cmd/geocode-warm`.
Not yet wired into the server — step 3 is its first consumer.
- Migration `066_geocoded_addresses.sql`
- Address normalization (unit tests: casing, whitespace, `St`/`Street`,
  `Apt`/`#` handling)
- `platform/geocode/provider.go` with a `Geocoder` interface + `google.go`
  implementation; cache-first wrapper in `app/`. Config: `GOOGLE_GEOCODING_API_KEY`
- Two-phase pattern: cache read tx → external call → write tx
- Backfill/admin action to warm the cache from existing local-delivery order
  history and surface low-confidence addresses immediately
- **Test:** unit tests for normalization; integration test against a handful of
  known Tri-Cities addresses; verify cache hit on second call

### Step 2 — OSRM deployment ✅ done 2026-08-14
Live on prod: `rr-osrm` on `rr-network`, reachable at `http://osrm:5000`.
Verified with a four-stop Tri-Cities `/trip` (see the known-good baseline in
`ops/osrm/README.md`).
- Mage target `osrm:build` — run on **angmar.dev**: download the Washington PBF,
  run extract → partition → customize, tar the `.osrm.*` set, `scp` to prod
  (see §2 — prod cannot preprocess; 3.7GB RAM, no swap)
- OSRM service in the production compose file: unpack the tarball into a volume,
  run `osrm-routed --algorithm mld --mmap` bound to the internal Docker network
  only, never published to a host port
- Healthcheck + `docs/operations.md` note for the quarterly data refresh
- **Test:** `curl` `/trip` with 4–5 real Tri-Cities coordinates; verify sensible
  ordering and durations

### Step 3 — OSRM client + route planner service
- Thin client in `platform/routing/` for `/trip` (lng,lat order; map
  `waypoint_index` back to input stops)
- `app.RouteService.PlanRoute`: load orders via the Load list filter with
  `Channel: nil` → geocode (cached) → OSRM trip → ordered stops + totals
- Surface failures explicitly: ungeocodable address, OSRM unreachable, address
  outside extract coverage. Sentinel errors in `app/errors.go`
- **Test:** table-driven tests with a mocked OSRM response; one integration test
  against the live container

### Step 4 — Route persistence + admin flow ✅ done 2026-08-14
Migration 067, `store.RouteStore`, `RouteService.WithPersistence`, admin review
page at `/admin/routes/{id}`, "Plan route" on the Load list tab. Wired into
`main.go`; prod needs `OSRM_BASE_URL=http://osrm:5000` and
`GOOGLE_GEOCODING_API_KEY` in `.env`.
- Migration `067_delivery_routes.sql` (`delivery_routes`, `route_stops`)
- "Plan delivery route" action on the Load list tab: runs the planner, saves a
  `draft` route, shows ordered stops with a review list (addresses, ETAs,
  low-confidence flags, channel badges)
- Admin can drop a stop and re-plan, then **Activate** (generates `share_token`)
- Audit every transition (`audit.Record` in the same tx as the write); new
  actions in `platform/audit/actions.go`
- **Test:** plan → review → activate; re-plan replaces rather than duplicates
  (partial unique index); permission gate is `orders:fulfill`

### Step 5 — Driver page
- `OrderService.MarkLocallyDelivered` (see §3) + audit action
- Token-authenticated mobile page: ordered stops, per-stop Navigate, Delivered
  toggle wired to `MarkLocallyDelivered`, progress header
- htmx for toggles; Alpine for optimistic check-off; no new build step
- **Test:** toggle marks both `route_stops.status` and the order
  delivered/complete; double-tap is a no-op; a completed route's token 404s;
  one-handed phone usability (manual check)

### Step 6 — Deep links, chunking, QR
- Google Maps multi-stop URL builder with 10-stop chunking (unit-test the chunk
  math at 9, 10, 11, 20 stops)
- Apple Maps per-stop links
- `go-qrcode` dependency; QR on the admin route view, printable
- **Test:** open generated links on a real iPhone and Android; verify stop order
  is preserved and pins land correctly

### Step 7 — Polish + rollout
- Route completion: auto-complete when all stops resolve, plus explicit
  "End route"
- Metrics via the existing Prometheus setup: routes planned, stops per route,
  geocode cache hit rate
- Walkthrough with Rockabilly on a real delivery day; capture friction for v1.1
- *(Roastery-origin setting dropped — `shipping_config` already has it.)*

---

## 7. Open Questions

Resolved on 2026-08-14:

- ~~Which orders qualify~~ → the existing Load list filter, channel-unscoped.
- ~~Geocoding provider~~ → Google.
- ~~One route or per-channel~~ → one route, both channels.
- ~~Driver token lifetime~~ → invalid once the route is `completed`.

Still open:

1. **Roundtrip vs. open-ended** — v1 assumes the driver returns to the roastery
   (`roundtrip=true`). Confirm with Rockabilly.
2. **Pin labels** — lat/lng pins show coordinates, not names, in the maps app.
   Confirm acceptable (recommended for accuracy).
3. **Stop volume** — if a run routinely exceeds ~10 stops, the chunking UX
   matters more; past ~25 stops, load-test OSRM `/trip`.
4. ~~Hetzner VPS memory~~ → measured 2026-08-14 (3.7GB, no swap): preprocess
   off-box, serve with `--mmap`. See §2 sizing.
5. ~~Skipped stops~~ → resolved 2026-08-14, see §4.

---

## 8. Changes From the First Draft

| Original | Corrected | Why |
|---|---|---|
| `tenant_id` on every table, isolation tests | No tenant scoping | Hiri is single-merchant |
| sqlc queries | Hand-written `pgx` in `store/` | `sqlcgen/` is legacy; CLAUDE.md is explicit |
| "locate the fulfillment-type field" | The Load list filter, verbatim | Already exists and is already the delivery-day roster |
| Roastery origin as a new tenant setting | `shipping_config.origin_*` | Already there for EasyPost labels |
| Delivered toggle hits "the existing fulfillment path" | New `MarkLocallyDelivered` | Existing paths require `shipped` or carrier tracking; neither applies |
| Route date = today | Route date = `scheduled_delivery_date` | Migration 064 already schedules runs |
| Channel unaddressed | One route, both channels | One van, one run |
| OSRM ops unqualified | Off-box preprocessing, mandatory | Prod measured at 3.7GB RAM, no swap; extract peaks ~5GB |
| Driver page auth from scratch | Stored token, patterns from `switch_to_pickup.go` | Signed-link precedent exists |
| New permission implied | Reuse `orders:fulfill` | Same gate as the Load list |
