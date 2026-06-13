# Critique Pass 2 — Subscribe & Retention

Scope: /subscriptions marketing page → product subscribe mode → /subscribe (Svelte) →
confirmation → account subscription management (pause/resume/cancel, billing portal) →
renewal/dunning engine (scheduler, batch renewal, webhook) → the four subscription emails.
Method: source-level review (templ, Svelte, handlers, services, jobs, email templates). Not a
rendered-pixel review — visual-balance judgments are inferred from markup. 2026-06-12.

## Anti-Patterns Verdict: PASS (storefront) / deferred (email)

The customer-facing surfaces stay on-brand: the /subscriptions hero (ink surface, candle glow,
double-candle mark, halftone mask) is one of the strongest compositions in the app; the account
area is honest paper-and-ink with square corners, stamp shadows, and inline confirm flows
("Nope" / "Never mind" as decline labels is exactly the right voice). No AI palette, no
glassmorphism, no fake urgency.

The four subscription emails share the off-token shell pass 1 flagged on `order_confirm`
(pure-white `#ffffff` card, `#1A1612`/`#F5F0E6`/`#C0271D` instead of ink/paper/rust, Arial,
rounded corners, text-only header). Visual normalization stays deferred to the email pass —
but the *copy* problems in those emails are in scope here and several are load-bearing (P3, P6).

Slop-adjacent tics, same family as pass 1:
- The eyebrow → slab → script-word heading formula appears on every surface in this pass too
  ("Set up the *routine.*", "Pick your *cadence.*", "You're on the *list.*", "The Daily *grind.*").
- The same right-arrow SVG rides along in nearly every CTA, including "Resume" — where an arrow
  pointing right means nothing.

## Overall Impression

The marketing promise is excellent and the renewal engineering has real care in it (batched
orders per customer+address, two-phase external-call pattern, atomic email enqueueing, payment
amount verification). But this pass found the same disease as pass 1 in a more dangerous form:
**the system writes checks its code can't cash, and this time money is involved.** The subscribe
flow can capture payment and create nothing. Subscriptions silently skip shipping and tax that
retail customers pay. A failed renewal gets one email promising automatic recovery that
effectively never comes. And the subscribe page — the highest-commitment purchase in the store —
never tells you what coffee you're buying. The single biggest opportunity: make the
subscribe/retention loop as trustworthy as the copy already claims it is.

## What's Working

1. **Renewal engine architecture.** The scheduler groups due subscriptions by
   (customer, shipping address) and consolidates them into one order/one shipment — fewer boxes,
   fewer charges, less email. `RenewSubscription` follows the read-tx → external call → write-tx
   pattern exactly as `CLAUDE-backend.md` prescribes, enqueues receipts atomically with the
   order, and only notifies on the active → past_due *transition* so retries don't spam. The
   payment-method picker has a comment documenting a real regression ("breaks Link customers —
   do not reintroduce") — institutional memory in the right place.
2. **The account subscriptions page is the right shape.** Product thumbnail, plan cadence in
   plain words ("Every 2 weeks · Qty 1 · $16.20 each"), status-differentiated badges, "Next
   order ships" with a real date, and inline two-step confirms for pause/cancel instead of
   modals. The empty state sells ("Subscribe to your favorites and never run out") instead of
   just reporting.
3. **Past-due email tone.** "Common causes: expired card, new card number, or a hold from the
   bank. Updating your payment method takes about a minute." Plain, non-blaming, names the
   product, one CTA. (Its central promise is broken — see P3 — but the writing itself is the
   best in the email set.)

## Priority Issues

### P1 — The subscribe flow can take money and create nothing — ✅ FIXED 2026-06-12
*(Re-architected to mirror retail's pre-creation pattern. `POST /api/subscribe/payment-intent`
now find-or-creates the customer + address, ensures a Stripe customer, creates the PI, and
pre-creates the first order in pending+awaiting stamped with `subscription_signup` metadata +
the plan ID + the PI ID — all before the customer touches the payment element. The
subscription row is created by payment success, not by the browser: `ConfirmCheckoutPayment`
drives the order awaiting→captured→confirmed, then `SubscriptionService.ActivateFromSignupOrder`
creates + links the subscription. This runs from either `/api/subscribe/confirm` (sync card
path) OR the `payment_intent.succeeded` webhook (the safety net when the browser never returns),
gated on `ConfirmCheckoutPayment`'s `transitioned` return so exactly one caller activates per
order. `SubscribeApp.svelte` gained redirect-back handling (the `payment_intent`/`redirect_status`
query params) and a truthful "processing" confirmation state for async methods. Retry-safety:
`ConfirmCheckoutPayment` now accepts the failed→captured transition (declined card, fixed,
retried on the same live PI) and refuses to resurrect a cancelled order (logs loudly if a
real capture lands on one). The abandoned-order sweep now also cancels pending+failed orders.
Tests: `ActivateFromSignupOrder` happy-path + no-metadata reject, the failed→captured and
cancelled-guard transitions, and the signup-metadata round-trip. Note: subscriptions still
charge no shipping/tax — that's P2, deliberately untouched here.)*
**What:** Retail checkout pre-creates the order at PaymentIntent time precisely so the Stripe
webhook can repair an interrupted flow. The subscribe flow doesn't: subscription + first order
are only created when the browser successfully calls `POST /api/subscribe/confirm` *after*
payment succeeds (`subscribe.go:262`). If the tab closes, the network drops, or confirm 500s,
the PI is captured and nothing exists — and `handlePaymentIntentSucceeded` logs "no order for
PI" and gives up (`webhook.go:131`). There is also no redirect-back handling at all:
`SubscribeApp.svelte` sets `return_url` but never checks the URL for a returning
`payment_intent`, so any redirect-based payment method dead-ends on a blank form. Retrying
submit doesn't recover either — `stripe.confirmPayment` errors on an already-succeeded PI.
**Why it matters:** This is the worst failure class ecommerce has: charged customer, no record
of why. No order in admin, no subscription, no email — the only trace is in Stripe. The
customer's first experience of "subscribe and trust us with your card" is a support ticket.
**Fix:** Mirror retail: pre-create the subscription + first order (pending/awaiting) when the
PI is created, stamp the PI ID on the order, and let `handlePaymentIntentSucceeded` confirm it
— the PI metadata (plan, variant, email, names) plus PI shipping address already carry enough
to do this. Add redirect-back handling to `SubscribeApp` while in there. Until then, every
async payment method offered by the Payment Element is a trap.
**Command:** implement (backend + Svelte), then /verify with a forced confirm failure.

### P2 — Subscriptions never charge shipping or tax; nobody decided that out loud — ✅ FIXED 2026-06-12 (decision: same as retail)
*(Product decision: subscriptions are priced identically to a one-off order — shipping + tax,
no perk. Threaded the existing checkout calc through both ends. Signup
(`handleSubscribePaymentIntent`) now builds the tax line from the variant's product
exemption, runs `CheckoutService.CalculateTax` + `CalculateShipping`, resolves the local
fulfillment method via `resolveLocalMethod`, and feeds shipping/tax/method into the PI amount
and `PlaceOrder`. Renewals gained `RenewalService.WithTaxCalc(settings, catalog)` + a
`renewalCharges` helper that runs the same `taxCalculatorForConfig` + `ShippingConfig.Calculate`
primitives; `RenewSubscription` and `RenewBatch` now set `Subtotal`/`ShippingTotal`/`TaxTotal`/
`Total` on the order and charge the full amount (batch: one shipping charge on the combined
subtotal, per-line tax). The renewal-receipt email's "Shipping $0.00 / Tax $0.00" cosmetic
resolves itself since the fields are now real. To keep the flow honest (you can't charge more
than you show), the subscribe form now renders a "Charged today" breakdown — roast / shipping /
tax / total — once the address is entered and the PI is created, and the Subscribe button shows
the real total instead of the bare item price. The math primitives (`CalculateFlatRateTax`,
`ShippingConfig.Calculate`) and `PlaceOrder`'s total composition already have unit coverage;
the renewal services are pool-based and, by existing project convention, have no integration
tests. Note: the static "Your plan" summary card still shows the item price with the misleading
"N × total" prefix — that's P4, untouched here.)*
**What:** Retail checkout computes shipping and Stripe Tax and adds them to the PI. The
subscribe flow charges exactly `discounted price × quantity` (`subscribe.go:197`), and renewal
orders hardcode `Subtotal = Total` with no shipping or tax lines (`renewal.go:213`). The
renewal receipt email then proudly prints "Shipping: $0.00 / Tax: $0.00" on every receipt.
**Why it matters:** If unintentional, it's margin leak on every renewal forever, plus tax
non-collection on recurring revenue — a compliance problem, not a design nit. If intentional
(free shipping as a subscriber perk), it is the single best retention argument the program has
and it appears *nowhere*: not on /subscriptions, not on the product page subscribe panel, not
on /subscribe, not in the confirm email. Either a money bug or an unmarketed benefit — it
can't be both nothing.
**Fix:** Decide. If perk: say "Free shipping on every delivery" on the marketing page, product
subscribe panel, and subscribe summary card — and confirm the tax position with an accountant.
If bug: thread the checkout's shipping/tax calculation into subscribe PI creation and
`RenewSubscription`/`RenewBatch`.
**Command:** product decision first; then /clarify (copy) or implement (calculation).

### P3 — Dunning is a dead end wearing a reassuring email — ✅ FIXED 2026-06-12
*(Built a real dunning state machine, replacing reliance on River's blind job backoff.
(a) The scheduler query now selects `status IN ('active','past_due') AND next_order_at <= now()`,
so for a past_due sub `next_order_at` doubles as the next dunning-retry time and the scheduler
re-attempts on a deliberate cadence. (b) On a declined charge, `recordRenewalFailure` increments
a `dunning_attempt` counter (in metadata) and either schedules the next retry — past_due,
`next_order_at` pushed +3d/+3d/+4d via `SetDunningRetry` — or, at the 4-attempt cap, expires the
subscription (`status=expired`, `ends_at` set) and sends a new "subscription ended" email with a
restart link. A successful charge clears the counter (`ClearDunning`). The renewal workers map a
new `ErrRenewalPaymentDeclined` sentinel to `river.JobCancel` so River no longer retries declines
— the scheduler owns retries; genuine infra errors still propagate for River retry. (c) Past-due
account cards now carry "Update payment method" (billing portal) + "Retry payment now"
(`POST /account/subscriptions/{id}/retry` → enqueues an immediate renewal, `ByArgs`-unique against
double-click) + an inline cancel, and the copy is honest about both the manual and automatic
retry. (d) Staff get the same "Retry payment now" on the admin subscription detail for past_due
subs (`POST /admin/subscriptions/{id}/retry`). The dunning escalation also fixes the renewal
receipt's reassurance being a lie. Tests: store-level dunning lifecycle (retry scheduling,
scheduler pickup of due past_due, future-retry exclusion, terminal expiry) + ClearDunning. Note:
expired subs aren't yet customer-restartable in-place — restart is via /subscriptions; in-place
restart is P6 territory.)*
**What:** When a renewal charge fails the subscription goes past_due and the customer gets one
email saying "Once we have a working card, the renewal will go out automatically." Nothing
makes that true. The scheduler's query is `WHERE status = 'active'`
(`sqlcgen/subscriptions.sql.go:343`) — past_due subscriptions are never picked up again. The
only retry is River's own backoff on the failed job (default ~25 attempts over ~2–3 weeks,
with multi-day gaps at the tail); when those exhaust, the subscription is stranded past_due
*forever*. Updating the card in the billing portal triggers nothing — the customer waits for
whenever the next blind job retry happens to land, if any remain. There's no second email, no
"final notice," no auto-cancel/expiry policy (`SubscriptionStatusExpired` and `ends_at` exist
in the domain and are never set by anything), no customer-visible "retry now," and no staff
retry button — admin can only acknowledge the dunning flag, pause, or cancel.
**Why it matters:** This pass is named *retention* and this is where retention actually
happens. Involuntary churn (expired cards) is the #1 subscription killer, and the current
system converts a fixable card problem into permanent silent churn — while the email promises
the opposite. The customer did everything right and still never gets coffee again.
**Fix:** (a) Make the scheduler (or a sibling query) re-enqueue past_due subscriptions on a
deliberate retry schedule (e.g. day 1/3/7/14) instead of riding River's backoff. (b) Cap it:
after N failures, auto-pause or expire with a final email — never strand. (c) On return from
the billing portal (the redirect already lands on /account/subscriptions), offer "Retry
payment now" on past-due cards. (d) Add a staff retry action on the admin subscription page.
**Command:** implement (jobs + handler), /clarify for the email sequence copy.

### P4 — The subscribe page never says what you're subscribing to — ✅ FIXED 2026-06-12
*(`handleSubscribePage` now loads the product (title, slug), the variant label via the pass-1
`VariantLabel` method, and the first product image. The summary card was rebuilt to lead with
the coffee: thumbnail + linked product title + "Whole Bean · 12oz" + cadence, with the plan
name demoted out of the headline entirely. The price is now unambiguous — per-unit price (base
struck through when the plan discounts) with an explicit "× N" multiplier on the left and the
"$Subtotal Per delivery" on the right — no more "2 × $36.00" hybrid. The same product-page bug
(`updatePrice` rendering "N × total") was fixed to show "N × unit". Added a plain billing-rhythm
line: "First bag ships now, then renews Every 2 Weeks. Next charge around Jul 26. Shipping and
tax shown at payment." (next-charge date via the new `SubscriptionService.NextRenewalDate`). The
Svelte confirmation now reads "Your <product> subscription is live" instead of the cadence-y plan
name. Also corrected the page's "skip, swap, or cancel" intro to "pause or cancel" (truthful
today; P6 still owns the full skip/swap copy sweep across the other surfaces).)*
**What:** `/subscribe` shows plan name, cadence, and price — and nothing else. No product
title, no image, no variant. Plan names are admin free-text with placeholder "e.g. Every 30
Days" (`plan_list.templ:54`), so the summary card typically reads "Your plan: EVERY 30 DAYS ·
Delivery Every Month" — cadence twice, coffee zero times. The variant travels as a UUID in a
data-attribute. Pass 1's P3 ("after Add to cart, the product disappears") was fixed for cart
and checkout; the subscribe flow has the same defect at *higher* stakes — this is a recurring
charge, and grind is still the variant that ruins coffee. The `VariantLabel` service method
built in pass 1 is sitting right there. Compounding it, the quantity price renders as
"2 × $36.00" where $36.00 is already the *total* (`subscribe.templ:51`, and the same
`qtyPrefix + total` pattern in product.templ's `updatePrice`) — it reads as $72.
**Why it matters:** Highest-commitment purchase in the store, lowest information. "Substance
over style — lead with the product" is design principle #3, and the page leads with a plan
record. The N × total misread is the kind of thing that kills checkout confidence on sight.
**Fix:** Load product + variant in `handleSubscribePage` and put the product title, thumbnail,
and "Whole Bean · 12oz" label at the top of the summary card; demote the plan name to the
cadence line. Render either "2 × $18.00" (unit) or "$36.00" (total), never the hybrid. While
there: state the billing rhythm plainly — "First bag ships now. Next charge $36.00 on Jul 12."
The customer currently learns when they'll be charged again only from the email.
**Command:** /clarify + implement (handler data), then /polish the summary card.

### P5 — The subscribe form regressed everything pass 1 fixed in checkout
**What:** `SubscribeApp.svelte` is a parallel form that shares no code with the fixed
`Information.svelte` — and none of the fixes. Zero `autocomplete` attributes (checkout has ten,
including the `shipping` prefixes); no prefill for signed-in customers (checkout prefills
contact + default address); name/city/state/ZIP grids lack the responsive `grid-cols-1
sm:grid-cols-2` treatment, so phones get three-across inputs. Backend equivalents too: the
subscribe endpoints have no rate limiting (coupon, contact, and magic-link all do) even though
`POST /api/subscribe/payment-intent` creates Stripe customers + PIs unauthenticated — a
card-testing target; every address-field blur discards and recreates the PI, and for new
emails mints a *fresh Stripe customer each time* (orphans accumulate; the abandoned PIs are
never cancelled since no order exists for the cleanup job to see); and every completed
subscribe `CreateAddress`es a brand-new address row, so a repeat subscriber's address book
fills with duplicates.
**Why it matters:** Pass 1's P1 was "checkout fights autofill and ignores known customers."
The fix landed in one of two forms. Subscribers — the customers most likely to already have an
account — get the worst form in the store at the moment of highest commitment.
**Fix:** Port the autocomplete tokens, prefill (a `GET /api/subscribe/context` sibling of the
checkout prefill), and responsive grids; add the standard rate limits; reuse-or-update the PI
instead of recreate-per-blur (or at least reuse the Stripe customer); dedupe address on
(customer, line1, zip) or offer the saved-address picker.
**Command:** /harden (forms + endpoints), then /verify.

### P6 — Retention copy promises powers the account page doesn't have
**What:** The subscribe page says "skip, swap, or cancel anytime from your account"; the
renewal receipt says "Pause, skip, or cancel anytime"; the confirm email says you can update
"shipping, payment, or your delivery cadence." The account page offers pause, resume, cancel.
There is no skip. There is no swap or cadence change (`ChangeVariant`/`ChangePlan` exist and
are admin-only). Beyond the copy gap, the pause/resume mechanics themselves fight retention:
customer pause is indefinite-only (handler passes `nil` pauseUntil — no "pause for 2
deliveries" option, no pause email, no resume reminder, i.e. silent churn with extra steps);
"Resumes {date}" renders when staff set pause_until, but *nothing auto-resumes* — no job reads
pause_until, so that line is a false promise at the system level; and Resume resets the period
from today, so a customer who's out of coffee and hits Resume gets their next bag a full
interval later with no warning and no "resume and ship now" option. Cancelled cards are a dead
end — no "Restart this subscription" even though every input still exists on the row.
**Why it matters:** "Specificity builds trust" cuts both ways: promising skip/swap that don't
exist is pass 1's fake-receipt problem relocated to the highest-LTV customers. And skip is not
decoration — "skip one delivery" is the #1 alternative to cancellation; its absence funnels
every too-much-coffee moment to the rust Cancel button.
**Fix:** Short term (copy): "pause or cancel anytime" everywhere; drop "cadence"; render
"Resumes {date}" only if auto-resume ships. Medium term (features, in value order): skip next
delivery (one button: `NextOrderAt += interval`), resume-and-ship-now, pause-until picker +
auto-resume job, restart on cancelled rows, and expose the existing ChangeVariant/ChangePlan
to customers (the same-price guard already makes it safe).
**Command:** /clarify now; implement for skip/auto-resume; /delight for the restart moment.

## Minor Observations

- The subscription card still shows `SKU RR-DRIP-12` as the variant line (`account.templ:622`)
  — the known pass-1 carry-over; `VariantLabel` is ready to drop in.
- Renewal order numbers are `SUB-<unix-millis>` (`renewal.go:201`) — "Subscription renewed —
  SUB-1749686400000" is a machine talking. First subscription orders get `ORD-XXXXXXXXXX`, so
  a subscriber's history mixes two formats. Generate them like retail's.
- The subscribe funnel has zero analytics. Retail is instrumented view_item → purchase
  (pass 1's "What's Working"); subscriptions — the highest-LTV conversion — never emit a
  begin_checkout or purchase event. Subscription revenue is invisible to GA4.
- The confirmation screen's only CTA is "Keep shopping" → /catalog. The natural next step is
  the thing they just created: link "See your subscription" → /account/subscriptions —
  which also teaches the magic-link login before the first renewal surprise.
- Signup sends two emails in the same minute (order confirm + subscription confirm). Probably
  correct, but the pair should read like siblings; today they share the off-token shell but
  not a voice ("Subscription Started" vs the storefront's confidence).
- Renewal receipt: "We've packed it up and charged your card" — at send time nothing is packed
  (the order is created unfulfilled seconds earlier). Pass-1 P4 energy; "It's headed to the
  roaster" is true and more on-brand.
- The plan discount is invisible at renewal: it's baked into the unit price, so the receipt
  never shows the subscriber what The Daily Grind saved them. A "Subscriber price — you saved
  $3.60" line is free retention.
- The past-due card on the account page describes the problem but holds no CTA; the "Update
  payment method" button sits above the list. Put the action on the failing card.
- `subscriptionStatusLabel` knows "Expired" and the domain carries `ends_at`, but nothing in
  the codebase ever sets either — dead vocabulary until P3's expiry policy gives it meaning.
- Customer pause sends no email. Pause is a churn-risk moment and a reactivation hook; even a
  one-liner ("Paused — resume anytime") with a resume link earns its send.
- "Cancel in two clicks" (/subscriptions hero) is honest about the clicks but not the
  magic-link login standing in front of them for the typical logged-out reader. Borderline;
  worth a thought when the email pass touches the login loop.
- The dev-only "Every 2 Minutes" interval would render verbatim on /subscribe via
  `subscriptionIntervalLabel` (pass-1 minor, second surface). `intervalDays` already guards
  emails by returning 0; the storefront helper has no such guard.
- Both `country` selects (checkout and subscribe) still offer exactly one option — fine for a
  US-only shop, but then a static "United States" line beats a one-item select.

## Questions to Consider

1. What does the relationship look like *between* boxes? Today the subscription is silent
   except when charging or failing — no "your next roast ships Thursday" note, no way to nudge
   the date. The brands that own subscription coffee own the week before the charge.
2. If a subscriber updates the address row their subscription points at, renewals follow it —
   but a *new* address from checkout doesn't. Where should "where is my coffee going?" live so
   the customer can answer it in one place?
3. The whole program hinges on plans being cadence records ("Every 30 Days, 10% off"). Is that
   the right product model long-term, or is the plan really a property of the product page UI
   — and would customer-facing cadence change (P6) make plans disappear as a concept?
4. The renewal engine can already batch multiple subscriptions into one box. The storefront
   never sells that ("add a second roast to your shipment — it rides free"). Why not?
5. What's the post-cancel story? The cancelled email says "the shop's open whenever" — but
   nobody ever follows up, and the account row is a tombstone. Is a 30-day "your spot's still
   warm" note in-voice for this brand, or too needy?
