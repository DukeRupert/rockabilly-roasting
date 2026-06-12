# App Map — Critique Reference

A surface-by-surface map of the Hiri / Rockabilly Roasting app, built for incremental critique passes.
Generated 2026-06-12 from `internal/web/router.go` and the package tree. Update as the app changes.

## The Four User-Facing Surfaces

### 1. Retail Storefront (public, branded)
Templates: `internal/ui/storefront/*.templ` · Handlers: `web/storefront.go`, `web/cart.go`, `web/checkout.go`, `web/subscribe.go`, `web/customer_auth.go`, `web/account.go`

| Area | Routes | Templates |
|---|---|---|
| Marketing/content | `/`, `/about` (+ contact form), `/subscriptions`, `/wholesale` (landing), `/privacy`, `/terms`, `/shipping`, `/newsletter/thanks`, `/help`, `/help/{slug}`, 404 page | `home`, `about`, `subscriptions`, `wholesale`, `legal`, `privacy`, `terms`, `shipping`, `newsletter_thanks`, `help`, `notfound` |
| Shopping | `/catalog`, `/catalog/{slug}`, `/cart` (+ add/update/remove POSTs) | `catalog`, `product`, `cart` |
| Checkout | `/checkout` + `/api/checkout/*` (cart, address, coupon, payment-intent, confirm), `/order/confirmed` | `checkout` (hosts **Svelte island** from `ui/checkout/`, embedded via go:embed) |
| Subscribe flow | `/subscribe` + `/api/subscribe/*` (payment-intent, confirm) | `subscribe` |
| Account (magic-link auth) | `/account/login`, `/account/magic`, `/account/settings`, `/account/orders[/{id}]`, `/account/subscriptions` (+ pause/resume/cancel), `/account/billing-portal`, `/account/addresses` (CRUD + default), `/account/security` (password set/change), `/account/verify-email/send`, `/account/password-setup` | `account_login`, `account`, `account_security`, `account_password_setup` |
| SEO/legacy | `/robots.txt`, `/sitemap.xml`, WooCommerce redirects (`/product/{slug}`, `/product-category/{slug}`, `/shop-merchandise`, `/rhythm-and-brews`) | — (`web/legacy_redirects.go`, `web/static.go`) |

### 2. Wholesale Portal (B2B, password auth, gated by `requireApprovedWholesale`)
Templates: `storefront/wholesale_*.templ` · Handlers: `web/wholesale.go`, `web/wholesale_account.go`

- **Onboarding (public):** `/wholesale/apply`, `/wholesale/setup` (password setup), `/wholesale/login` — templates `wholesale_apply`, `wholesale_setup`, `wholesale_login`, `wholesale_status`
- **Portal:** `/wholesale/portal` (quick order grid + bulk-add), wholesale cart update/remove, `/wholesale/checkout` (+ confirm), `/wholesale/order-confirmed` — templates `wholesale_portal`, `wholesale_checkout`, `wholesale_order_confirmed`
- **Account:** `/wholesale/account/orders[/{id}]` (+ reorder), `settings`, `addresses` (CRUD + default), `security` — template `wholesale_account`
- **Help:** `/wholesale/help[/{slug}]` — template `wholesale_help`
- Open follow-up (memory): capture a default address during wholesale onboarding.

### 3. Admin Panel "Hiri" (staff, RBAC: admin/fulfillment/finance/catalog/support)
Templates: `internal/ui/admin/*.templ` · Handlers: `web/admin_*.go` · Rules: `docs/admin-ui.md` (enforced by `mage checkAdminUI`)

Sidebar is 7 flat items; second-level pages reached via section tabs / channel toggle (`section_nav.templ`):

| Sidebar item | Routes / sub-areas | Templates |
|---|---|---|
| Dashboard | `/admin/` + htmx partials: top-sellers, revenue, subscriptions trend | `dashboard`, `charts/` |
| Orders | `/admin/orders` (retail), `/admin/orders/wholesale` (channel toggle), order detail w/ state actions (cancel, mark-paid, refund, fulfill, ship, revert-*, pickup/delivery transitions, shipping-method, line-item variant swap), manual order creation (`/admin/orders/new` + variant search), batch ops (invoices, packing slips, ready-for-pickup, picked-up, out-for-delivery), packing slip + invoice views | `order_list`, `order_show`, `order_new`, `order_invoice`, `packing_slip`, `timeline` |
| Fulfillment | `/admin/fulfillment`, `/admin/wholesale/fulfillment`, shipping rates, label buy (single + bulk), label download | `fulfillment_list`, `order_label_rates` |
| Catalog | products CRUD + clone + status/featured/visibility/subscribable toggles, variants (CRUD, archive, channels, price), options/values, images (Cloudflare upload, reorder, primary); tabs: Categories, Attributes (sets, keys, product assignment) | `product_list`, `product_new`, `product_edit`, `product_form`, `product_media`, `catalog_components`, `category_list`, `attribute_*` |
| Customers | `/admin/customers[/{id}]` — groups, payment terms, price list, billing method, local fulfillment, send-password-setup; tabs: Groups, Price Lists (incl. bulk price editing), Wholesale apps (approve/decline/suspend/reactivate) | `customer_list`, `customer_show`, `customer_activity`, `group_list`, `price_list_list`, `price_list_pricing`, `wholesale_list` |
| Subscriptions | list, detail (pause/resume/cancel, dunning-ack, variant/plan change); tab: Plans (create, activate/deactivate, discount) | `subscription_list`, `subscription_show`, `subscription_timeline`, `plan_list` |
| Discounts | `/admin/discounts` (list only — read-only?) | `discount_list` |
| User menu | Settings (shipping, default price list, box presets, QuickBooks OAuth connect/callback/disconnect), Audit log, Help, logout | `settings`, `box_presets`, `audit_list`, `help` |
| Auth | `/auth/staff/login` | `staff_login` |

Shared admin UI: `badges.templ`, `section_nav.templ`, `ui/components/` (shadcn-style: button, card, dialog, dropdown, sheet, sidebar, table, toast, tooltip, …), `layouts/admin.templ`. htmx partial pattern: `<Name>Content` + `<Name>` wrapper, `IsHTMX(r)` check.

### 4. Transactional Email (Postmark)
`internal/emailtemplates/{html,text}/` — 19 pairs: magic_link, verify_email, password_setup, account_not_migrated, order_confirm, order_shipped, order_ready_for_pickup, order_out_for_delivery, refund_confirmation, subscription_{confirm, renewal_receipt, past_due, cancelled}, invoice_{sent, paid, past_due}, wholesale_{application, approved, suspended}.

## Backend Layers (strict inward-only imports)

- **`domain/`** (22 files) — types/enums: catalog, order, customer, subscription, invoice, discount, fulfillment, shipping, pricing, wholesale, attributes, tax, tracking_url, staff, session, audit, webhook, quickbooks, box presets
- **`store/`** (24 files) — all SQL, every fn takes `pgx.Tx`; sqlc-generated code in `store/sqlcgen/`; 53 migrations in `db/migrations/`
- **`app/`** (~30 services) — OrderService, CheckoutService, CartService, CatalogService, CustomerService, SubscriptionService (+ renewal.go two-phase pattern), FulfillmentService, ShipmentTracking, DiscountService, PricingService, PriceListService, CustomerGroupService, WholesaleService, InvoiceService, AttributeService, AuthService (+ magic link email), AuditQueryService, WebhookService, QB sync (orders_qb*.go), email composition per domain (`*_email.go`), errors.go sentinels
- **`web/`** (38 files) — thin handlers; `router.go` (506 lines) is the single route registry; `respond.go` maps errors; `middleware.go` (sessions ×3 flavors, request ID, logging, body limit)
- **`jobs/`** (34 River workers) — grouped: emails (×14), QB sync (×7: ensure/sync customer, create invoice, process update, reconcile ×2, sync payment), subscriptions (renewal_scheduler → batch_renewal → subscription_renewal), shipping (buy_label, store_label_r2, shippo_tracking_update, shipped_order_autodeliver), wholesale lifecycle (×3), housekeeping (abandoned_order_cleanup, r2_image_delete, magic_link_send, invoice_send)
- **`platform/`** (16 sub-packages) — audit, auth (RBAC perms), email (Postmark), help, logging, media (Cloudflare Images + R2), metrics (Prometheus), payments (Stripe), quickbooks (OAuth + client), ratelimit, sentry, sessions, shipping (Shippo — EasyPost/Pirate Ship both removed), tax (Stripe Tax), turnstile (Cloudflare bot protection)

## External Integrations
Stripe (payments, billing portal, tax, webhook) · Shippo (labels, tracking webhook w/ URL-token auth) · QuickBooks (OAuth, invoice/customer/payment sync, webhook) · Cloudflare (Images, R2, Turnstile) · Postmark (email) · Sentry · Prometheus (separate internal listener)

## Cross-Cutting Concerns (critique anywhere)
- **Three auth populations:** retail customers (passwordless magic link, optional password), wholesale (password + approval workflow), staff (password + 5-role RBAC in middleware only)
- **Rate limiting:** global IP limit + endpoint limits on contact, coupon, wholesale-apply, magic-link, auth, staff-login, account-security
- **Two sales channels** (retail/wholesale) thread through orders, fulfillment, carts, variants (`variant channels`, migration 052), pricing (price lists; group pricing fully removed)
- **Order lifecycle:** pending → paid → fulfilled → shipped → delivered, with local-fulfillment branch (ready-for-pickup → picked-up; out-for-delivery), reverts, refunds, cancellation, auto-deliver job
- **Atomicity invariant:** tx + audit.Record + river.InsertTx in same transaction; metrics after commit; external calls never inside a tx
- **Design system:** `Rockabilly Roasting Design System/` (storefront: paper-and-ink, stamp shadows) vs `docs/admin-ui.md` (quiet treatment, semantic badges)

## Not Routes But Still Critiquable
- `cmd/` one-shots: server, seed, support-reply, sentrycheck, shippo-smoketest, fix-pending-paid, os-report (untracked), archived importers (migrate, os-migrate)
- `magefiles/mage.go` build pipeline; `mage check` = vet + tests + admin-UI lint
- `testutil/` (testcontainers, per-test rollback tx, fixtures, assertions); test coverage is concentrated in `app/` and `domain/`
- Docs: `docs/CLAUDE-backend.md` (authoritative), `operations.md`, runbooks, `guide/` end-user docs, untracked `docs/security/`
- Untracked at repo root: `migrate/`, `server` binary, `new_logos/`, `wc-variant-mapping.json`

## Suggested Critique Passes
1. **Retail buy path** (highest revenue impact): home → catalog → product → cart → checkout (Svelte) → confirmation + order emails
2. **Subscribe & retention:** /subscribe flow, account subscription management, renewal/dunning jobs + emails
3. **Wholesale lifecycle:** apply → approve → setup → portal → checkout → invoicing/payment terms
4. **Admin operations:** orders + fulfillment + shipping (the daily staff workflow)
5. **Admin catalog & pricing:** products/variants/options/attributes, price lists, discounts
6. **Auth & account surfaces:** three auth systems, password/magic-link/security pages, rate limits
7. **Backend invariants:** tx/audit/job atomicity, error handling, customer scoping, webhook handlers
8. **Content & brand:** marketing pages, help center, legal, emails vs. design-system voice rules
