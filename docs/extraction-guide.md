# Ecommerce Core Extraction Guide

**Audience:** an AI agent (or developer) building a new, lean single-merchant shop in a
*fresh repo*, reusing the architecture of this project (Hiri / Rockabilly Roasting) without
its B2B, subscription, and accounting machinery.

**The shop being built:** a small, fun storefront selling kids' artwork, 3D prints, cat toys,
etc. One merchant, retail only, flat-rate or free shipping. The goal is to avoid paying a SaaS
platform, on an architecture the owner understands and controls.

**How to use this doc:** This is a *reference*, not a copy list. The source repo at
`github.com/dukerupert/hiri` (this repo) is a mature ~120K-line platform. You are **not** forking
it and deleting things. You are building a clean ~15–20K-line core in a new module, *reading*
named files here to learn *how* each piece is structured, then writing a simpler single-channel
version. When a file in this repo branches on sales channel, subscription, price list, or
wholesale — that branch simply never exists in your version.

Read `docs/CLAUDE-backend.md` (the authoritative backend reference) and the root `CLAUDE.md`
alongside this guide. They describe the patterns; this doc tells you which to keep and what to cut.

---

## 1. Decisions already made (do not re-litigate)

- **Retail only.** No wholesale / B2B portal, no sales-channel concept. Every order is retail.
- **No subscriptions.** No renewal engine, no dunning, no plans.
- **Shipping: flat-rate or free.** No EasyPost / live rates / label buying. A single configurable
  flat fee (or free over a threshold) computed in code at checkout.
- **No accounting integration.** No QuickBooks, no invoices.
- **Lean operational spine.** Keep River (job queue) + audit log + Stripe webhooks. **Drop**
  Prometheus, Sentry, and the metrics registry. Add observability later only if missed.
- **Payments: Stripe** (Payment Intents + webhook), same as here.

---

## 2. The architecture to keep (this is the whole point)

The valuable, reusable asset is the **layered package discipline**, not the code. Keep it exactly:

```
domain/   → nothing from this project (pure types, enums, constants)
store/    → domain/                          (all SQL; every func takes pgx.Tx)
app/      → store/, platform/audit, domain/  (all business logic, validation)
web/      → app/, platform/*, domain/, ui/   (thin HTTP handlers)
ui/       → domain/ only                      (templ templates)
jobs/     → app/, platform/*, domain/         (thin River workers)
platform/ → domain/ only                      (infrastructure sub-packages)
```

Dependencies flow **inward only**. This is a compile-time guarantee — preserve it.

### Load-bearing patterns to carry over verbatim

1. **Transaction + audit + job atomicity.** Every data-modifying service method:
   - accepts `pgx.Tx` (never opens its own transaction),
   - calls `audit.Record(ctx, tx, ...)` in the **same** tx,
   - enqueues River jobs via `river.InsertTx(ctx, tx, ...)` in the **same** tx.
   - (We dropped Prometheus, so skip the "increment metrics after commit" step.)
   - Handlers wrap scope with `store.Tx(ctx, db, func(tx pgx.Tx) error { ... })`.
   - Reference: `internal/store/db.go:34` (`Tx` helper), any method in `internal/app/orders.go`.

2. **External calls never inside a DB transaction.** Call Stripe first, then write the result in
   its own tx. Reference pattern: `CheckoutService.ConfirmCheckoutPayment`
   (`internal/app/checkout.go:457`) and the RenewalService two-phase note in `CLAUDE-backend.md`.

3. **Customer data scoping by type.** Customer-facing store methods take `customerID` as a
   required parameter; staff/unscoped methods use `GetByID`. This enforces ownership at the type
   level. Keep it. Reference: the `Get` vs `GetByID` split throughout `internal/store/`.

4. **Sentinel errors + boundary mapping.** Errors live in `app/errors.go`, checked with
   `errors.Is()`; HTTP mapping lives in `web/respond.go`. Never log-and-return. Reference:
   `internal/app/errors.go`, `internal/web/respond.go`.

5. **htmx partial rendering for admin.** Each admin page splits into `<Name>Content` (partial)
   and `<Name>` (wraps content in `@layouts.Admin`); GET handlers check `IsHTMX(r)`. Reference:
   `internal/web/admin_orders.go` + matching `internal/ui/admin/`.

6. **Thin everything.** Handlers parse→call service→render. Workers open tx→call service→return.
   All logic lives in `app/`.

---

## 3. What to drop entirely

Do not port any of these. They are a real coffee business's needs, not yours.

| Drop | Where it lives here | Notes |
|---|---|---|
| Wholesale / B2B | `app/wholesale*.go`, `web/wholesale*.go`, `web/admin_wholesale.go`, `domain/wholesale.go`, `store/customer_groups.go`, `store/price_lists.go`, `app/price_lists.go`, `app/customer_groups.go` | ~50 file refs. Collapse the `SalesChannel`/`OrderChannel` concept to "retail" and delete the branches. |
| Subscriptions | `app/subscriptions*.go`, `app/renewal.go`, `domain/subscription.go`, `store/subscriptions.go`, `web/subscribe.go`, `web/admin_subscriptions.go`, `web/admin_plans.go`, all `jobs/*renewal*`, `jobs/*subscription*`, `jobs/batch_renewal.go` | ~37 refs. |
| QuickBooks | `internal/platform/quickbooks/` (1,500 LOC), all `jobs/qb_*.go`, `app/orders_qb*.go`, `store/qb_credentials.go`, `domain/quickbooks.go` | |
| Invoices | `app/invoices*.go`, `web/admin_invoices.go`, `store/invoices.go`, `domain/invoice.go`, `jobs/*invoice*`, `jobs/invoice_send.go` | ~25 refs. |
| Live shipping / labels | `internal/platform/shipping/` (EasyPost), `jobs/buy_label.go`, `jobs/store_label_r2.go`, `jobs/shippo_tracking_update.go`, `web/admin_shipment.go`, `web/webhook_shippo.go` | Replace with a flat-rate calc (see §5). |
| Metrics / Sentry | `internal/platform/metrics/`, `internal/platform/sentry/` | Drop the `metrics *metrics.Registry` field from every service. |
| Price tiers / groups / coupons (optional) | `app/discounts.go`, `app/pricing.go` price-list paths, `domain/pricing.go` | Keep a *simple* coupon if wanted (see §5); drop the wholesale price-list machinery regardless. |
| Cloudflare R2 (labels) | the aws-sdk-go-v2/s3 deps | Only used for shipping labels. Drop unless you want R2 for product media. |

---

## 4. The lean core to build (package by package)

Build in dependency order: `domain → store → app → web/ui → jobs`. For each, read the named
reference file(s) here, then write a single-channel, no-subscription version.

### `domain/`
Keep the vocabulary, strip the B2B/subscription enums.
- `order.go` — keep `OrderStatus`, `PaymentStatus`, `FulfillmentStatus`, the `Order`/`OrderLineItem`
  structs. **Delete** `OrderChannel` (retail/wholesale) and `PaymentStatusPendingInvoice/Invoiced/
  Overdue/PartiallyPaid` (invoice states). Reference: `internal/domain/order.go:1`.
- `catalog.go` — products, variants, media. Keep as-is conceptually.
- `customer.go` — keep; drop `account_type`/wholesale fields.
- `cart.go` concepts (in `domain/catalog.go`/wherever `Cart`/`CartItem` live).
- Keep: `audit.go`, `session.go`, `staff.go`, `shipping.go` (trim to flat-rate config), `tax.go`
  (only if you keep Stripe Tax — otherwise drop).
- **Drop:** `wholesale.go`, `subscription.go`, `invoice.go`, `quickbooks.go`, `pricing.go`
  (price-list tiers), `discount.go` (unless keeping coupons).

### `store/`
One file per domain area; every function takes `pgx.Tx`. Read for SQL shape, simplify queries to
drop channel/price-list joins.
- Keep & simplify: `db.go` (the `Tx` helper — copy nearly verbatim), `catalog.go`, `carts.go`,
  `customers.go`, `orders.go`, `sessions.go`, `magic_links.go`, `settings.go`, `audit.go`.
- **Drop:** `customer_groups.go`, `price_lists.go`, `subscriptions.go`, `invoices.go`,
  `qb_credentials.go`, `shipping.go` (EasyPost shipments), `webhooks.go` (QB/Shippo webhook log —
  keep a trimmed version only for Stripe idempotency if desired), `discounts.go` (optional).
- Note: this repo has a `store/sqlcgen/` dir but the convention is hand-written SQL per file. Match
  whatever you choose, but the existing hand-written stores are the better reference.

### `app/`
The heart. Read these, write single-channel versions:
- `cart.go` — `internal/app/cart.go`. Drop `AddItemForCustomer`'s price-list path, the
  `assertVariantInChannel` / `assertVariantAccessible` checks (wholesale visibility). Keep
  `GetOrCreateCart`, `AddItem`, `UpdateItemQuantity`, `RemoveItem`, `ListItems`, `GetItemCount`.
- `checkout.go` — `internal/app/checkout.go`. This is the most entangled file; read it carefully.
  - Keep: `CalculateShipping` (rewrite as flat-rate, §5), `PlaceOrder`, `ConfirmCheckoutPayment`
    (Stripe two-phase), `Get/UpdateShippingConfig`.
  - Drop: the `isWholesale` parameter threaded through `taxCalculatorForConfig` /
    `CalculateTax` — pass a single retail path. Drop `CreateManualOrder` unless you want
    admin-created orders. Coupon methods (`ApplyCoupon`, `calculateDiscount`) are optional.
- `orders.go` — `internal/app/orders.go`. Note the `With*` builder pattern on `OrderService`
  (`WithEmail`, `WithShipments`, etc.) that injects optional deps. Keep the pattern but drop the
  `subscriptions`, `discounts` (unless coupons), `metrics`, and `pricing` (price-match) fields.
  Drop channel scoping in the list/count methods.
- `catalog.go`, `customers.go`, `auth.go` + `auth_email.go` (magic-link/session auth) — keep.
- `actor.go`, `errors.go`, `job_enqueuer.go`, `email_env.go` — keep (trim).
- **Drop:** everything wholesale/subscription/invoice/qb/price-list/renewal.

### `web/` + `ui/`
- Storefront: `web/storefront.go`, `web/cart.go`, `web/checkout.go`, `web/account.go`,
  `web/customer_auth.go`, `web/static.go`, `web/respond.go`, `web/middleware.go`, `web/router.go`.
- Stripe webhook: `web/webhook.go` (keep). **Drop** `webhook_qb.go`, `webhook_shippo.go`.
- Admin (core only): `web/admin.go`, `web/admin_orders.go`, `web/admin_catalog.go`,
  `web/admin_customers.go`, `web/admin_settings.go`, `web/admin_media.go`, `web/order_line_items.go`.
  **Drop** the wholesale/subscription/plans/invoices/shipment/groups/price-list admin handlers.
- `ui/` — keep `layouts/`, `storefront/`, `components/`, and the `admin/` templates matching the
  handlers you kept. The admin UI rules in `docs/admin-ui.md` and `mage checkAdminUI` are worth
  porting if you keep the paper-and-ink admin styling; otherwise the admin can be plain.
- Auth model: **Customers** scoped by `customerID` query param; **Staff** coarse RBAC in
  middleware (`web/router.go`), never in handlers. You may collapse the 5 staff roles to a single
  "admin" role for a one-person shop.

### `jobs/`
River workers, thin. Keep only:
- `email_order_confirm.go`, `email_order_shipped.go` (rename to a generic "shipped/fulfilled"
  notice since there are no carriers), `magic_link_send.go`, `email_verify_send.go`,
  `abandoned_order_cleanup.go`, `workers.go`, `enqueuer.go`.
- **Drop:** every `qb_*`, every `*renewal*`/`*subscription*`/`*invoice*`, `buy_label.go`,
  `store_label_r2.go`, `shippo_tracking_update.go`, `shipped_order_autodeliver.go` (delivery
  tracking), `wholesale_*`.

---

## 5. Replacements you must write fresh

- **Flat-rate shipping.** Replace `CheckoutService.CalculateShipping`
  (`internal/app/checkout.go:139`) with: read a `ShippingConfig{ FlatCents int; FreeOverCents int }`
  from settings; return `0` if subtotal ≥ `FreeOverCents`, else `FlatCents`. Keep it a method on
  the service so checkout/admin stay unchanged in shape. Store the config in `store/settings.go`.
- **Tax.** Simplest: keep Stripe Tax (it's already wired via the Stripe Payment Intent flow) OR
  drop tax entirely and charge tax-inclusive prices. If you drop it, remove the `tax` platform pkg
  and the `CalculateTax` call from `PlaceOrder`. For a tiny hobby shop, dropping it is reasonable —
  confirm your local sales-tax obligations.
- **Coupons (optional).** If you want a simple "FRIENDS10" code, keep a trimmed `domain.Discount`
  + `ApplyCoupon`/`calculateDiscount` from `checkout.go`. Drop the wholesale price-list interplay.

---

## 6. Dependencies (lean `go.mod`)

Start from these direct deps (everything else here is for dropped subsystems):

```
github.com/a-h/templ            // templ templates
github.com/jackc/pgx/v5         // Postgres
github.com/google/uuid
github.com/riverqueue/river + riverdriver/riverpgxv5   // job queue
github.com/stripe/stripe-go/v82 // payments
github.com/pressly/goose/v3     // migrations
github.com/joho/godotenv        // .env loading
github.com/mrz1836/postmark     // email (or swap for any SMTP/provider)
github.com/stretchr/testify + testcontainers-go + .../postgres   // tests
golang.org/x/crypto             // password/session hashing
github.com/Oudwins/tailwind-merge-go  // only if porting the templ UI helpers
github.com/yuin/goldmark        // only if you render markdown content
```

**Drop:** `EasyPost/easypost-go`, all `aws-sdk-go-v2/*` (R2 labels),
`getsentry/sentry-go`, `prometheus/client_golang`.

---

## 7. Build order & migrations

1. `go mod init`, scaffold the `internal/{domain,store,app,web,ui,jobs,platform}` dirs + `cmd/server`.
2. Port the Mage targets you need from `magefiles/mage.go`: `templ`, `css`, `build`, `dev`,
   `test`, `db:migrate/rollback/status/create`, `seed`. Drop `checkout` (Svelte) unless you build a
   JS checkout island; drop `wcMigrate`/`os-migrate`/`checkAdminUI` (port the last only if you keep
   the admin styling).
3. **Migrations: start fresh.** Do not copy the 54 migrations here — they carry wholesale, plans,
   invoices, QB tables. Write a clean initial schema for: `products`, `variants`, `media`,
   `customers`, `sessions`, `magic_links`, `carts`, `cart_items`, `orders`, `order_line_items`,
   `audit_log`, `settings`, plus River's own tables. Read the existing migrations as a *schema
   reference* for column shapes only.
4. Wire `cmd/server/main.go` from `internal` outward. Reference: `cmd/server/main.go` (598 lines
   here — yours should be ~150). It shows the full dependency graph; delete every node you dropped.
5. Stand up `domain → store → app` with tests (the testcontainers harness in `internal/testutil/`
   ports almost verbatim and is worth doing early), then `web/ui`, then `jobs`.

---

## 8. Sanity checks (definition of done for the core)

- A visitor can browse products, add to cart, check out, and pay via Stripe.
- Stripe webhook confirms the order; an order-confirmation email is enqueued via River **in the
  same tx** as the order write + audit record.
- Admin can log in, see orders, mark one fulfilled (firing the shipped/fulfilled email), and
  add/edit products + media.
- Flat-rate (or free-over-threshold) shipping shows at checkout.
- `go vet` clean; the import-direction rules in §2 hold (no `app` importing `web`, etc.).

When in doubt about *why* something is shaped a certain way, check the "Design rationale" appendix
in `docs/CLAUDE-backend.md` before copying — some decisions here exist for B2B/subscription reasons
that don't apply to you, and you should simplify rather than cargo-cult them.

---

## 9. Storefront & UI: reuse the plumbing, leave the brand

The new shop wants its **own** look — playful, kid-made-goods energy — not Rockabilly's
paper-and-ink coffee brand. The `internal/ui/` tree splits cleanly into **brand-neutral plumbing**
(reuse) and **Rockabilly-specific styling/content** (leave behind). The rule of thumb: *structure
and primitives transfer; theme tokens and content templates don't.*

### Reuse — brand-neutral plumbing

- **`ui/components/`** — these are shadcn-style headless primitives: `button`, `card`, `dialog`,
  `dropdown`, `input`, `label`, `table`, `toast`, `tooltip`, `badge`, `avatar`, `sheet`, `popover`,
  `switch`, `separator`, `aspectratio`, `sidebar`, `textarea`, `icon`. Their *structure, props, and
  accessibility wiring* are reusable; only the Tailwind classes inside carry Rockabilly styling.
  Port the ones you need and restyle by swapping class names — the component API stays.
- **`ui/utils/`** — class-merge / variant helpers (the `tailwind-merge-go` glue). Copy verbatim.
- **The templ + htmx plumbing itself** — `IsHTMX(r)` checks, the `<Name>Content` / `<Name>` split,
  `hx-boost` on `<body>`, toast slide-in mechanics. This is *interaction architecture*, not brand.
  Reuse the pattern; it's the same in any theme.
- **Layout skeletons** — `ui/layouts/storefront.templ` and `admin.templ` as *structural* references
  (head/meta, asset links, nav slot, content slot, htmx setup). Keep the skeleton, replace the
  visual chrome (logo, nav styling, footer) with your own.
- **The Tailwind build wiring** — `ui/assets/css/input.css`'s `@import "tailwindcss"` + `@source`
  globs, and the `mage css` / `mage watch` targets. Copy the *mechanism*; replace the tokens (below).

### Leave behind — Rockabilly-specific

- **The `@theme` block in `input.css`** (the `--color-rr-*`, paper-and-ink palette, `--font-slab`/
  `--font-heritage`/`--font-script`, stamp shadows, candle/rust/espresso tokens). This *is* the
  brand. Start a fresh `@theme` with your own palette, type, and shapes. Keep the file's *shape*
  (a `@theme` block + `@source` globs) but none of its values.
- **`ui/storefront/*.templ`** — these are coffee-content pages (`product.templ` shows roast/origin
  attrs via `coffee_attrs.go`, `home.templ`, `about.templ`, `subscribe.templ`, all `wholesale_*`).
  Use them only as *layout references* for "what a product page / cart / checkout needs structurally",
  then write your own copy and markup. **Drop** every `wholesale_*` and `subscribe*`/`subscriptions`
  template outright. `coffee_attrs.go` and `format.go` are coffee-specific — replace with your own
  product attributes (e.g. "made by", "material", "age range").
- **The admin paper-and-ink styling** (`docs/admin-ui.md`, the `rr-*` token allowlist,
  `mage checkAdminUI`). For a one-person hobby shop the admin can be plainly styled — port the
  *structure* of the admin templates but skip the brand-enforcement tooling unless you want it.
- **The `Rockabilly Roasting Design System/` folder** at the repo root — entirely brand-specific.
  Don't carry it; build (or vibe out) your own small design language for the kids' shop.

### The Svelte checkout island — decide explicitly

`ui/assets/checkout/` + the `ui/checkout/src/*.svelte` bundle (built via `mage checkout`, embedded
with `go:embed`) is a compiled Svelte component for the multi-step checkout. It's powerful but adds
a Node/Svelte build step. For a small shop you have two clean options:
- **Drop it** and build a server-rendered checkout in templ + htmx + a little Alpine.js (simpler
  stack, no JS build). Recommended for a fun project — one less toolchain.
- **Keep it** if you want the richer client-side checkout UX; then port `mage checkout` and the
  embed wiring. The Stripe Payment Intent flow on the server (`app/checkout.go`) is identical either
  way — the island is just the front-end.

### Net

Reuse: `components/`, `utils/`, the templ+htmx interaction patterns, layout *skeletons*, the
Tailwind build mechanism. Rewrite: the `@theme` tokens, every storefront content template, product
attributes, and the brand. You keep a tested component kit and a proven interaction model, and you
get a storefront that looks like *your kids' shop*, not a coffee roaster.
