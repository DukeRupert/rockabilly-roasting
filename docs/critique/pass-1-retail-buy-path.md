# Critique Pass 1 — Retail Buy Path

Scope: home → catalog → product → cart → checkout (Svelte) → order confirmation → order email.
Method: source-level review (templ, Svelte, handlers, compiled bundle, email templates). Not a
rendered-pixel review — visual-balance judgments are inferred from markup. 2026-06-12.

## Anti-Patterns Verdict: PASS

This does not look AI-generated. Locked warm palette (no cyan/purple/neon), hard offset stamp
shadows instead of soft drops, square corners, distinctive type stack, real voice ("Nothing in
the bag yet.", "No call, no hassle, no hold music."), real address/hours/phone. The design
system is committed and consistently executed.

Two slop-adjacent tics worth knowing about:
- **The heading formula repeats on every surface**: eyebrow → slab headline → one rust script
  word ("Pick your *roast.*", "In the *bag.*", "Seal the *deal.*", "Thanks for the *order.*",
  "Freshly *stamped*", "The *whole* lineup", "Tasting *profile.*", "Pour another *cup.*").
  Each instance honors the "script as garnish, one per surface" rule; collectively it's a
  template. The delight flattens by the third page.
- **The same arrow SVG appears in nearly every CTA** (~6–10 times per page). When everything
  points right, nothing does.

## Overall Impression

The brand work is genuinely strong and the engineering under the checkout is thoughtful
(redirect-back handling, error recovery, analytics dedup). The weakness is the second half of
the funnel: from "Add to cart" onward, the experience progressively forgets both the product
and the customer. Coffee's one critical variant (grind) disappears after the product page, a
logged-in customer is treated as a stranger at checkout, and the confirmation email — the
most-opened artifact in the journey — is the least branded surface in the system. The single
biggest opportunity: make checkout use what the platform already knows.

## What's Working

1. **Checkout failure engineering.** Klarna/async redirect-back is handled with a finalizing
   state; `address_incomplete` routes the buyer back to the step that can fix it instead of
   leaving a dead Stripe mount; the GA4 `purchase` event is cookie-gated and sessionStorage-
   deduped. This is above-average care for a small shop.
2. **Accessibility intent.** Skip link, `aria-live` price display, focus-trapped mobile filter
   drawer with Escape + focus restore, `prefers-reduced-motion` honored on the hero video and
   back-to-top scroll, real labels on every checkout field.
3. **Funnel instrumentation.** view_item_list → select_item → view_item → add_to_cart →
   view_cart → begin_checkout → purchase, all wired, all converting cents client-side, with
   sensible ownership gating on the purchase event.

## Priority Issues

### P1 — Checkout fights autofill and ignores known customers — ✅ FIXED 2026-06-12
*(autocomplete tokens on all fields + prefill from session customer's contact info and
default address via `GET /api/checkout/cart`. Remaining sub-item: state free-text →
select/normalize was deliberately left out of scope.)*
**What:** `Information.svelte` has `autocomplete` on exactly one field (phone). Email, name,
address, city, state, ZIP have none — browsers will guess or do nothing. There is no prefill
for logged-in retail customers, no saved-address picker (the account area has a full address
book with defaults), no "welcome back" recognition. Everyone types everything, every time.
The state field is free text; the country select has one option.
**Why it matters:** This is the highest-intent moment in the funnel and the single largest
source of mobile checkout abandonment. The platform already holds this data — magic-link
accounts, saved addresses, default flags — and the checkout uses none of it.
**Fix:** (a) Add the standard autocomplete tokens (`email`, `given-name`, `family-name`,
`address-line1`, `address-line2`, `address-level2`, `address-level1`, `postal-code`) — a
15-minute change with outsized payoff. (b) Have `GET /api/checkout/cart` (or a sibling)
return the session customer's email + default address and prefill/offer it. (c) Make state a
select or validate-and-normalize.
**Command:** /harden (forms), then /verify against a running server.

### P2 — The coupon system has no front door — ✅ FIXED 2026-06-12 (shipped the feature)
*(Coupon field in the checkout order-summary sidebar wired to the existing apply/remove
endpoints, with PI recreation on apply/remove. Admin discounts page gained a create form
[percentage/fixed + code + min order + expiry], codes column, and activate/deactivate.
Also enforced the previously-dormant minimum-order rule at apply + PI time, and made code
lookup case-insensitive.)*
**What:** `POST /api/checkout/coupon` and `DELETE` exist with rate limiting, four distinct
friendly error messages, Prometheus counters, and cart persistence. The compiled
`checkout.js` bundle contains **zero** references to coupon. No coupon field exists in cart
or checkout. Meanwhile `/admin/discounts` is list-only (no create route). The feature is
dead-ended at both ends.
**Why it matters:** Either customers are being promised codes somewhere (email campaigns,
wholesale outreach) that they cannot redeem — a trust failure — or this is dead code carrying
maintenance and attack surface (a rate-limited public endpoint) for nothing.
**Fix:** Decide. If discounts are launching: add the field to the checkout order-summary
sidebar (the response shape `discount_total`/`discount_name` is already rendered there) and
give admin a create form. If not: remove the endpoints and the dead rendering branch.
**Command:** product decision first; then /distill (remove) or implement (add).

### P3 — After "Add to cart," the product disappears — ✅ FIXED 2026-06-12
*(New `VariantLabel` service method resolves option values to "Whole Bean · 12oz". Cart
page and checkout sidebar now show product thumbnails + variant labels instead of raw
SKUs; order emails carry the label in the line item and the shipping address now includes
recipient name + line 2. Remaining: order-history pages and wholesale surfaces still show
SKUs — noted for passes 3/6.)*
**What:** Cart line items, the checkout order summary, and the confirmation email all show
no product image and no human-readable variant. Cart and checkout show raw SKUs ("SKU
RR-DRIP-12") as the only variant signal. The email shows only the product title — built in
`orders_email.go` from `product.Title` alone, with a literal `"Product"` fallback — and the
shipping address omits the recipient's name and line 2.
**Why it matters:** For coffee, grind is *the* variant that ruins an order when wrong. A
customer who isn't sure whether they picked whole bean has no way to verify at cart, at
payment, or in the email — until the bag arrives. That's support tickets and remorse refunds.
Images also do silent persuasion work in carts; an all-text receipt-style cart is a brand
choice, but an unverifiable order is a defect.
**Fix:** Resolve option values ("Whole Bean · 12oz") into cart/checkout/email line items
instead of (or beside) SKU; add thumbnails to cart and checkout summary; include recipient
name + line2 in the email address block.
**Command:** /clarify for the labels; small backend change for the email data.

### P4 — Fabricated specifics undermine the brand's own trust rule — ✅ FIXED 2026-06-12
*(Receipt strip now shows the real cart short ID [first 8 of the cart UUID — stable and
support-usable]. Stock line softened to always-true claims: "Roasted to order · ships
within 48 hours" for coffee, "Ships within 48 hours" for merch. JSON-LD keeps `InStock`
deliberately — with no inventory tracking, every published product is genuinely orderable,
which is what the schema enum means. "Fresh Weekly" stamp gated on coffee, on both the
product page and the home featured card. Cart cancel promise replaced with "Ships within
48 hours." Self-service cancel-before-fulfillment remains a candidate feature — the order
state machine supports it — but it's a feature decision, not a copy fix.)*
**What:** Four instances of invented or unconditional specificity:
- Cart renders a fake receipt number — `receipt #{ len(items)*17+3 }` — that changes as you
  edit your cart (`cart.templ:106`).
- Every product is permanently "In stock · roasted fresh · ships within 48 hours"
  (`product.templ:451`) and JSON-LD hardcodes `InStock` — no inventory signal exists.
- Every product image carries the tilted "Fresh Weekly" stamp unconditionally — including
  any non-coffee merch.
- Cart promises "Cancel anytime before shipping," but no customer-facing cancel exists
  (account orders are read-only; cancellation is an admin-only action).
**Why it matters:** Design principle #4 is "Specificity builds trust. Real names, real dates,
real prices. No placeholder enthusiasm." A decorative fake receipt number is placeholder
enthusiasm wearing the brand's own costume. Each of these is harmless until the one customer
notices — then the whole paper-and-ink honesty aesthetic reads as set dressing.
**Fix:** Receipt strip: show the real item count only, or the cart's short ID. Stock line:
either wire to something real or soften to claims that are always true ("Roasted to order ·
ships within 48 hours"). Stamp: gate on coffee products. Cancel copy: either build
self-service cancel-before-fulfillment (it fits the order state machine) or change the line
to "Questions? Reply to your confirmation email."
**Command:** /clarify for copy; /harden for the conditional rendering.

### P5 — The order email is the least-designed surface in the journey
**What:** `order_confirm.html` uses a pure-white `#ffffff` card (the system's stated "pure
white is a bug" rule), off-token colors (`#1A1612`/`#F5F0E6`/`#C0271D` vs ink `#0E0D0C` /
paper `#F6EFE1` / rust `#B4351D`), Arial, a text-only header instead of the badge mark, no
next-step expectations ("you'll get a tracking email"), no support path ("reply to this
email"), and the thin line items from P3.
**Why it matters:** Open rates on order confirmations approach 100% — it's the most-read
brand artifact a buyer receives, and right now it could be from any store. The
on-site confirmation page sets expectations beautifully ("We'll roast, pack, and ship within
48 hours — you'll get tracking once it's on a truck") and the email says none of it.
**Fix:** Bring the email onto the real tokens (email-safe: solid colors, system serif/sans
fallbacks are fine — it's the palette and voice that carry the brand, not the fonts), reuse
the confirmation page's expectation copy, add a reply-to/support line. Apply the same pass
to the whole `emailtemplates/` set later (pass 8).
**Command:** /normalize against the design system; /clarify for the copy.

## Minor Observations

- `subscriptionIntervalLabel` will happily render "Every 2 Minutes" — a test interval enum
  leaking into customer-facing copy if such a plan ever exists (`product.templ:818`).
- Catalog page meta is `Title: "Shop"`, description "Browse our collection of products" —
  generic, off-voice, and weak for the second-most-important SEO page (`catalog.templ:1067`).
- Product cards hardcode "· 12oz" beside every price — wrong for any non-12oz default
  variant or merch (`catalog.templ:582`).
- Add-to-cart feedback is a 2-second "Added!" + badge increment; no view-cart/checkout
  affordance at the moment of intent, and the toast system in the layout is unused here.
  Consider a brief "Added — View cart →" state instead of just "Added!".
- Quantity is clamped to 10 client-side only; `handleCartAdd` accepts any positive int
  (note for pass 7).
- Cart quantity changes require clicking the small "Update" text button; auto-submit on
  change (htmx `hx-trigger="change"`) would remove a step and a failure mode.
- Catalog search's hidden filter inputs submit the internal `regions` param while links emit
  the public `origin` alias — both work, but URLs are inconsistent depending on how you got
  there (`catalog.templ:284` vs `FilterSlugToParam`).
- `og:image` falls back to a *relative* path (`/static/rockabilly-logo.png`) — relative URLs
  are invalid for Open Graph; shares of pages without an explicit OGImage get no image
  (`layouts/storefront.templ:115`).
- Alpine.js loads from jsdelivr with a floating `@3.x.x` version — unpinned third-party
  runtime on every storefront page; vendor it like htmx is (`/audit-js` covers this).
- The newsletter form posts cross-origin, so its `hx-target="#newsletter-status"` aria-live
  region can never receive a response — dead wiring; the Broadwave API key is also exposed
  in markup (fine only if it's a publishable key by design).
- Checkout keeps the full storefront chrome including the footer newsletter signup —
  competing CTAs on the payment page; consider a quieter checkout frame.
- Cart/checkout/confirmation `<title>`s are bare ("Cart", "Checkout") — fine — but the cart
  page sets no meta description and is indexable; consider `noindex` on cart/checkout.
- `handleStorefrontHome` hero: `preload` hint points at the poster (`roaster.jpg`) while the
  video loads immediately anyway — verify the LCP story on a real run (`/audit-baseline`).

## Questions to Consider

1. What would checkout feel like if it knew you? Magic-link accounts, address books, and
   default-address flags all exist — and the checkout uses none of them.
2. Where does a customer confirm whole-bean vs ground after the product page? (Currently:
   nowhere until the bag arrives.)
3. If the coupon field shipped tomorrow, where would the codes come from? Admin discounts
   has no create form — is the discount feature half-launched at both ends?
4. Does every heading need the script word? What would it mean to spend that flourish only
   where the moment earns it (order confirmed: yes; pagination header: no)?
5. The cart is styled as a receipt — what if it leaned all the way in with *real* receipt
   data (real timestamps, real order of operations) instead of a fake receipt number?
