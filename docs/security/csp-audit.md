# Content-Security-Policy audit

**Status:** Phase 1 — report-only policy drafted, ready to deploy.
**Last audited:** 2026-06-04
**Scope:** Storefront, wholesale portal, and admin (all served from `localhost:3000` behind Caddy).

This document inventories everything a CSP must account for in the frontend, the
policy we're deploying, and the work required to reach a strict (no `unsafe-*`)
policy. It is the reviewable home for the policy while it runs report-only; keep
it in sync when the policy changes.

> **Header location.** Security headers (HSTS, X-Frame-Options, etc.) are set in
> the Caddy reverse proxy on the VPS, not in the Go app — see the
> `(security_headers)` snippet in `/etc/caddy/Caddyfile`. The CSP lives there
> too. The Go app sets no security headers itself.

---

## TL;DR

- A **strict, nonce-based CSP is not achievable today** without meaningful
  refactoring (Phase 2 below). Three things force `unsafe-*`:
  1. **Alpine.js** (standard build) evaluates directives via `new Function()` → needs `script-src 'unsafe-eval'`.
  2. **~48 inline event handlers** (`onclick=`, `onchange=`, …), almost all in admin → need `script-src 'unsafe-inline'` (nonces don't cover inline handlers).
  3. **Heavy inline `style=""`** (119 in `account.templ` alone) + Alpine `:style` → need `style-src 'unsafe-inline'`.
- A **pragmatic origin-locking CSP is worthwhile now** and ships report-only with
  zero breakage risk. Even with `unsafe-inline`/`unsafe-eval`, it blocks injected
  `<script src="evil.com">`, plugin objects (`object-src 'none'`), clickjacking
  (`frame-ancestors`), and `<base>` hijacking (`base-uri`).
- The vendored templui components already render `nonce={ templ.GetNonce(ctx) }`,
  but **no middleware ever calls `templ.WithNonce`**, so the nonce is empty/dormant
  today. Activating nonces is part of Phase 2, not a flip of a switch.

---

## External origins (the allowlist)

Origins the browser actually fetches, and where they're loaded from:

| Directive | Origins | Source |
|---|---|---|
| `script-src` | `js.sentry-cdn.com`, `www.googletagmanager.com`, `cdn.jsdelivr.net` (Alpine — storefront only), `challenges.cloudflare.com` (Turnstile), `js.stripe.com` (loaded by the Svelte checkout bundle) | `layouts/storefront.templ:55,67,246`, `storefront/wholesale_apply.templ:12` |
| `style-src` | `fonts.googleapis.com` | layout heads (`storefront.templ:243`, `admin.templ:122`, `staff_login.templ:84`) |
| `font-src` | `fonts.gstatic.com` | layout heads |
| `img-src` | `cdn.rockabillyroasting.shop` (Cloudflare Image Transformations), GA pixels (`www.googletagmanager.com`, `www.google-analytics.com`), `data:` | `platform/media/media.go:34`, preconnect at `storefront.templ:242` |
| `connect-src` | Sentry ingest (`*.sentry.io`), `www.google-analytics.com`, `api.stripe.com` | Sentry/GA init blocks, Stripe.js |
| `frame-src` | `js.stripe.com`, `hooks.stripe.com` (Stripe Elements), `challenges.cloudflare.com` (Turnstile widget) | checkout, wholesale apply |

> **Inconsistency to note:** the storefront loads Alpine from the `cdn.jsdelivr.net`
> CDN (`storefront.templ:246`) while the admin self-hosts it from
> `/static/js/alpine.min.js` (`admin.templ:125`). Vendoring the storefront Alpine
> too would let us drop `cdn.jsdelivr.net` from `script-src` entirely.

---

## What blocks a strict policy

### 1. Alpine.js requires `unsafe-eval`
Alpine's standard build compiles directive expressions with the `Function`
constructor (confirmed in the vendored `internal/ui/assets/js/alpine.min.js`).
Usage is heavy and spans both surfaces:

- **48** `x-data`, **66** `x-show`, **34** `@click`, **18** `x-bind`, plus `@submit`, `:class`, `:style`, `x-if`, `x-init`.
- Files: `admin/{attribute_key_edit,order_list,order_new,product_edit,product_media,wholesale_list,fulfillment_list}.templ`, `admin/charts/charts.templ`, `storefront/{account,product,wholesale_account,wholesale_checkout,wholesale_portal}.templ`.

Removing `unsafe-eval` means migrating to the **Alpine CSP build**, which forbids
inline expressions — every `x-data="{...}"` / `@click="..."` must move to
registered components/methods. Large, mechanical, error-prone.

### 2. Inline event handlers require `unsafe-inline` (script)
~48 `on*=` attributes, almost entirely in admin templates:

`order_show.templ` (5), `customer_show.templ` (9), `wholesale_list.templ` (7),
`product_edit.templ` (2), `subscription_show.templ` (3), `settings.templ`,
`product_form.templ` (2), `price_list_list.templ` (2), `attribute_*` (4),
`audit_list.templ` (2), `category_list.templ`, `group_list.templ`,
`box_presets.templ`, `discount_list.templ`, `invoice_show.templ`,
`product_list.templ`, `subscription_list.templ`, `admin.templ:614`, plus
print-page `onload=` in `packing_slip.templ` and `order_invoice.templ`.

Nonces and hashes do **not** apply to inline handler attributes. Eliminating
`unsafe-inline` for scripts means refactoring each handler to `addEventListener`.

### 3. Inline styles require `unsafe-inline` (style)
Inline `style=""` is pervasive — top offenders: `account.templ` (119),
`wholesale_account.templ` (81), `home.templ` (41), `product.templ` (37),
`catalog.templ` (33), `layouts/storefront.templ` (33) — plus Alpine `:style`
bindings. Style attributes can't carry nonces, so `style-src 'unsafe-inline'` is
effectively required short of a sweeping move to utility classes.

### 4. Un-nonced inline `<script>` blocks
Hand-written inline scripts carry no nonce: Sentry init and gtag init
(`storefront.templ:56,68`), and per-page scripts in `home.templ:80`,
`product.templ` (472, 726, 838, 963), `catalog.templ` (312, 459, 905),
`checkout.templ:49`, `order_confirmed.templ:118`, `cart.templ:237`,
`product_edit.templ:665`, `admin.templ:436`.

> **Not a problem:** `<script type="application/ld+json">` (JSON-LD) and
> `<script type="application/json">` data blocks (`ga-cart-data`,
> `ga-purchase-data`, `ga-list-data`, `ga-product-data`, `ga-checkout-data`) are
> **not executed** and are **not** governed by `script-src`. They need no nonce.

---

## Phase 1 — the report-only policy (current)

Deployed via the `(security_headers)` snippet in `/etc/caddy/Caddyfile` as a
single-line, double-quoted value (HTTP header values cannot contain newlines —
do **not** use Caddy heredoc syntax here):

```
Content-Security-Policy-Report-Only:
  default-src 'self';
  script-src 'self' 'unsafe-inline' 'unsafe-eval'
    https://js.sentry-cdn.com https://www.googletagmanager.com
    https://cdn.jsdelivr.net https://challenges.cloudflare.com https://js.stripe.com;
  style-src 'self' 'unsafe-inline' https://fonts.googleapis.com;
  font-src 'self' https://fonts.gstatic.com;
  img-src 'self' data: https://cdn.rockabillyroasting.shop
    https://www.googletagmanager.com https://www.google-analytics.com;
  connect-src 'self' https://*.sentry.io https://www.google-analytics.com https://api.stripe.com;
  frame-src https://js.stripe.com https://hooks.stripe.com https://challenges.cloudflare.com;
  frame-ancestors 'self';
  base-uri 'self';
  form-action 'self';
  object-src 'none'
```

(The directives above are shown wrapped for readability; in the Caddyfile they are
one line.)

### Validation procedure
Report-only emits **no server-side reports** by default — violations appear only
in the browser DevTools console as `[Report Only]` warnings. (Add a
`report-uri`/`report-to` endpoint later if server-side collection is wanted.)

1. Deploy and confirm the header is present:
   ```bash
   caddy validate --config /etc/caddy/Caddyfile
   systemctl reload caddy
   curl -s -D - https://rockabillyroasting.com -o /dev/null | grep -i content-security-policy
   ```
2. Browse the real flows with DevTools → Console open, watching for `[Report Only]`:
   - Storefront: home, catalog, product (Alpine, GA, JSON-LD)
   - Checkout + subscribe (Svelte bundle → Stripe Elements iframe, `api.stripe.com`)
   - Wholesale apply (Turnstile widget)
   - Admin: `order_show`, `customer_show`, `product_edit` (inline handlers)
   - Confirm Sentry and GA still report (check their dashboards)
3. Tighten the origin lists to what's actually observed.
4. When a full pass is clean, flip the header name from
   `Content-Security-Policy-Report-Only` to `Content-Security-Policy` to enforce.

---

## Phase 2 — earning back the `unsafe-*` (backlog)

Each item independently tightens the policy. Not prerequisites for Phase 1.

- [ ] **Activate nonces.** Add middleware that generates a per-request nonce and
      calls `templ.WithNonce(ctx, …)`; thread the nonce onto every hand-written
      inline `<script>` (§4). Then drop `'unsafe-inline'` *for nonced scripts*.
- [ ] **Refactor inline event handlers** (§2) to `addEventListener` — start with
      admin (`customer_show`, `order_show`, `wholesale_list`).
- [ ] **Migrate to the Alpine CSP build** (§1) to drop `script-src 'unsafe-eval'`.
- [ ] **Reduce inline styles** (§3) toward Tailwind utility classes to drop
      `style-src 'unsafe-inline'`.
- [ ] **Vendor the storefront Alpine** so `cdn.jsdelivr.net` leaves `script-src`.
- [ ] (Optional) add a `report-to` collector for ongoing violation telemetry.

---

## Change log

- **2026-06-04** — Initial audit. Phase 1 report-only policy drafted; deployed in
  Caddy `(security_headers)` snippet.
