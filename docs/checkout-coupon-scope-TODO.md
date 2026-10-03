# Fixed: a second payment intent on one cart spends the coupon and does not apply it

**Status: fixed.** Redemption moved from placement to capture. The write-up
below is kept as the record of what the defect was and why the fix is where it
is; the decisions it asked for, and what was deliberately left alone, are in
[Resolution](#resolution) at the bottom.

`/api/checkout/payment-intent` is not a one-shot. The payment step re-runs it
whenever the shipping method or the pricing version changes, and every run calls
`PlaceOrder`, which redeems the cart's coupon in the transaction that creates the
order. Nothing releases that redemption until the order is cancelled, which for
an order nobody pays is the 24-hour abandoned-order sweep.

So the second run finds the code spent — by an order the same customer is still
trying to pay for — and prices the cart without it. The customer is charged full
price for the cart they were just shown a discount on, with nothing said about
it, and the code stays locked for a day.

Found after the fact, not accepted at merge.
`internal/web/checkout_coupon_reuse_test.go` was written failing, as the
specification for the fix, and passes now.

## What existed before the fix

**The endpoint runs in three phases** (`internal/web/checkout.go:692`):

1. Read tx: load cart, price lines, resolve the coupon, compute tax and
   shipping. The coupon block is at `:817`.
2. No tx: `PaymentProvider.CreatePaymentIntent`.
3. Write tx: `PlaceOrder` (`:956`), then `UpdateStripePaymentIntentID`.

**The client re-runs it deliberately.** `ui/checkout/src/steps/Payment.svelte:97`
holds an `$effect` gated on `shippingMethod !== initializedMethod ||
pricingVersion !== initializedPricingVersion`, so a pickup/delivery toggle or a
coupon apply/remove tears down the Stripe element and calls the endpoint again.
The comment at `:81` states the intended consequence: *"The previous order stays
in payment_status=awaiting and Stripe auto-cancels its PI within a day — same
lifecycle as a customer abandoning checkout."*

**Coupon codes are single-use and claimed atomically.**
`RedeemCouponCode` (`db/queries/discounts.sql:42`) is
`UPDATE … WHERE id = $1 AND redeemed_at IS NULL RETURNING *`, so the loser of a
race gets no row back rather than a second redemption. That part is right.

**Redemption happens at placement, not at capture.**
`PlaceOrder` (`internal/app/checkout.go:341`) guards on
`coupon.RedeemedAt != nil` at `:378` and calls `RedeemCouponCode` at `:464`.

**Release is tied to order cancellation only.**
`ReleaseCouponCodeByOrderID` (`db/queries/discounts.sql:56`) matches on
`redeemed_by_order_id` and is reached through `CancelOrder`
(`internal/app/orders.go:398`) — admin cancel, or
`abandoned_order_cleanup` at `abandonedOrderAge = 24 * time.Hour`
(`internal/jobs/abandoned_order_cleanup.go:24`).

**Removing the coupon does not release it.** `handleCheckoutRemoveCoupon`
(`internal/web/checkout.go:654`) clears `cart.AppliedCouponCodeID` via
`UpdateCartDiscount(ctx, tx, cartID, nil, nil)` and does nothing else.

## The problem

Order A is created by the first intent and redeems the code. The second intent
reaches the phase-1 coupon block, which reads:

```go
if cart.AppliedCouponCodeID != nil {
    cc, ccErr := d.CheckoutService.GetCouponCodeByID(ctx, tx, *cart.AppliedCouponCodeID)
    if ccErr == nil && cc.RedeemedAt == nil {
        // …discountTotal, discountName, couponCode set here
    }
}
```

`cc.RedeemedAt` is now set, so the whole block is skipped: `discountTotal` stays
0 and `couponCode` stays `""`. `PlaceOrder` is then called with a nil coupon and
succeeds. **The endpoint returns 200 with the discount silently gone.**

A refusal would at least be legible — `ErrCouponAlreadyUsed` maps to a 422 whose
copy is *"coupon was just used by another customer"*, which is wrong but visible.
The `RedeemedAt` check at `:819` converts that into an overcharge with no
message. The screen re-renders at the higher total and nothing explains why.

Three reachable sequences, none of them a double-click:

| Sequence | Who hits it |
|---|---|
| Coupon applied before the payment step, then pickup/delivery toggled | any local-zip customer with a code |
| Coupon applied on the payment step, removed, re-applied | anyone comparing with and without |
| Coupon applied on the payment step, then method toggled | both of the above |

The orphaned orders themselves are expected and documented. The coupon is the
part `Payment.svelte:81` does not account for.

## The test

`internal/web/checkout_coupon_reuse_test.go`, one test:
`TestCheckoutPaymentIntent_SecondIntentKeepsTheCoupon`. It is a handler test and
commits, like everything else in this package — see `internal/web/setup_test.go`.

**Why at the handler and not in `internal/app`.** `PlaceOrder` refusing a spent
coupon is correct in isolation; an app-level test asserting it would pass and
prove nothing. The defect lives in the seam between a client that re-runs the
endpoint and a service that treats each run as a new order, so the test drives
`d.handleCheckoutPaymentIntent` directly.

**Fixture** — `newCheckoutCouponFixture(t)`, committed, keyed on fresh UUIDs:

- product + variant, base price **5000** (`testutil.SetBasePriceForVariant`)
- customer + default address, postal code `59601`
- discount: percentage, value **10**, active, **no minimum** — the discount is
  not what is under test, so it is the least conditional one available
- coupon code `REUSE-<uuid[:8]>`, so parallel runs and a shared container cannot
  collide
- a cart with one line of that variant, via `AddItemForCustomer`
- the coupon applied to the cart via `OrderService.UpdateCartDiscount`

**Deps** — `newCheckoutPaymentDeps(t)` is `newSubscribeDeps(t)` plus
`DiscountService` (phase 1 reads the applied discount through it) and a
`fakePaymentProvider` from `payments_fake_test.go`.

**Body** — the same `checkoutPaymentIntentRequest` both times, `shipping_method`
empty. Nothing about the customer's intent changes between the two posts; the
point is that the endpoint re-running is normal.

**Assertions:**

| # | Assertion | Before the fix |
|---|---|---|
| 1 | first post is 200 | passes |
| 2 | `DiscountTotal == 500` on the first | passes — 10% of 5000 |
| 3 | second post is **200**, not a refusal | passes |
| 4 | `secondResp.DiscountTotal == firstResp.DiscountTotal` | **fails: 0 vs 500** |
| 5 | `secondResp.Amount == firstResp.Amount` | **fails: 5000 vs 4500** |

Assertion 3 is deliberately not the failing one. The bug is the money, not the
status code, and a fix that turned this into a 422 would satisfy a
status-shaped assertion while leaving the customer worse off.

## The plan, all of it carried out — see [Resolution](#resolution)

1. **Move redemption from placement to capture.** `ConfirmCheckoutPayment`
   (`internal/app/checkout.go:581`) already takes
   `GetOrderByStripePaymentIntentIDForUpdate` — a row lock — and already refuses
   unless `PaymentStatus` is `awaiting` or `failed`. That is the once-only guard
   the redemption wants, and it is where the money actually moves. Redeeming
   there means a superseded order never held the code at all.
   - Keep `PlaceOrder`'s **validation** of the coupon (active, not expired,
     minimum met, not already redeemed) so an unusable code still fails early
     and loudly. Move only the `RedeemCouponCode` call.
   - The order needs to carry the coupon code it was placed with, so capture can
     redeem it. `order.Metadata` already carries `cart_id` and
     `payment_intent_id`; a `coupon_code_id` alongside them is the cheap route,
     a column is the honest one.
   - Check what `CancelOrder`'s release does when nothing was ever redeemed —
     `ReleaseCouponCodeByOrderID` is a no-op `UPDATE … WHERE
     redeemed_by_order_id = $1`, so it should stay correct, but assert it.
2. **Decide what a coupon means between placement and capture.** Moving
   redemption later opens a window where two customers can both place an order
   holding the same single-use code, and the second to pay loses at capture. Per
   code that is rare and arguably correct — first to pay wins. Confirm that is
   the intent, and decide what the loser is told; today the only copy for this
   is the 422 string *"coupon was just used by another customer"*, which would
   finally be accurate.
3. **Make `handleCheckoutRemoveCoupon` consistent** with whatever (1) decides.
   If redemption moves to capture it needs no change at all, which is a good
   sign the fix is in the right place.
4. **Confirm the test fails for the right reason before trusting the fix.**
   Follow the habit in `dunning-dead-card-scope-TODO.md`: revert the change and
   watch assertions 4 and 5 fail with 0 and 5000. A fix that makes the test pass
   by refusing the second intent has not fixed anything.
5. **Consider the orphaned orders separately.** They are documented and swept,
   and they are not this bug. If they are dealt with — by having phase 3 cancel
   the prior pending order for the cart, or by reusing it — the coupon problem
   goes away as a side effect. Do not let that be the reason the redemption
   stays at placement; the window reopens the moment anything else calls
   `PlaceOrder` twice.

## Related, smaller, same area

- **Nothing passes a Stripe idempotency key.** `SetIdempotencyKey` appears
  nowhere in the repo and `payments.CreatePaymentIntentRequest`
  (`internal/platform/payments/provider.go:29`) has no field for one, so every
  re-run mints a fresh PaymentIntent. A key derived from cart ID plus a totals
  hash would collapse the churn. Independent of the coupon fix.
- **`POST /api/checkout/payment-intent` has no per-IP limit.** It is registered
  with a bare `mux.HandleFunc` (`internal/web/router.go:227`) under the global
  limiter only. Its sibling `POST /api/subscribe/payment-intent` carries
  `subscribeIPLimit` with a comment at `:202` explaining that an unauthenticated
  endpoint minting Stripe customers and PaymentIntents is a card-testing target.
  Both endpoints fit that description; the asymmetry looks unintentional.

## Resolution

Redemption now happens at capture. The changes, in the order the flow reaches
them:

- **`PlaceOrder` (`internal/app/checkout.go`)** still resolves and validates the
  coupon — not found, inactive, expired, minimum not met, already redeemed —
  and still prices the discount into the order and writes its adjustment. It no
  longer calls `RedeemCouponCode`. Instead it stamps `coupon_code_id` onto
  `order.Metadata`, next to the `cart_id` and `payment_intent_id` already
  carried there, so capture knows what to claim. A column would be the honest
  home for it; this is the route the other two take, and it needs no migration
  a fork has to inherit.
- **`ConfirmCheckoutPayment`** calls the new `redeemOrderCoupon` after the
  state transitions and before the cart delete — inside the transaction that
  already holds `GetOrderByStripePaymentIntentIDForUpdate`'s row lock and has
  already refused anything not in `awaiting` or `failed`. That is the
  once-only guard the redemption wanted.
- **`cartIDFromMetadata`** and the new `couponCodeIDFromMetadata` now share
  `uuidFromMetadata`, which still returns `(uuid.Nil, false)` for anything
  missing or unparseable — metadata is JSONB and nothing enforces its shape.
- **`handleCheckoutRemoveCoupon` is unchanged**, which item 3 predicted and
  took as the sign the fix was in the right place.
- **`redeemOrderCoupon` reads the coupon back when the claim fails.**
  `RedeemCouponCode` returns no rows for any already-spent code, whoever spent
  it, and capture is re-enterable — a late `payment_intent.payment_failed` for
  an earlier declined attempt knocks a captured order back to `failed`, and the
  next success drives it forward again. So a failed claim is only a loss when
  the code sits against a *different* order; against this one it is the same
  redemption seen twice.
- **`CancelOrder`'s release was asserted, not assumed.**
  `ReleaseCouponCodeByOrderID` is `UPDATE … WHERE redeemed_by_order_id = $1`
  and is a no-op for an order that never redeemed anything, which is now the
  normal case for an abandoned checkout. There is a test on it.

### The decision item 2 asked for

Moving redemption later opens a window where two customers can both place an
order on the same single-use code. **First to pay wins; the second is not
blocked.** By the time `ConfirmCheckoutPayment` runs, that customer's money has
already moved at Stripe and the discount is already in the amount charged.
Refusing the redemption there would strand a paid order in `awaiting` for
someone to refund by hand, which helps nobody and protects a coupon at the
customer's expense. So the capture stands, and the lost redemption is recorded
as `coupon.redemption_lost` (`audit.AuditCouponRedemptionLost`) against the
order — the one trace the merchant has that a single-use code went out twice.

The 422 copy *"coupon was just used by another customer"* is still on the
placement path, where it is now accurate: it fires when the code was genuinely
captured by someone else before this order was placed.

### What the reviews added

- `DiscountService.GetCouponCodeForOrder` now takes the order and falls back to
  the `coupon_code_id` it was placed with. It keyed on `redeemed_by_order_id`,
  which is only set at capture now, so the admin order page had stopped showing
  the code for any unpaid order — and would never have shown it for one whose
  redemption was lost. That field is there because the discount's *name*, which
  the adjustments table already shows, is not what support gets asked about.
- `coupon.redemption_lost` has a label and a marker colour in
  `order_activity.templ`, rendered like the other things that undid or refused
  something. The decision above rests on that entry being legible on the order
  page; without a case it was a grey dot reading "Redemption lost".
- The lifecycle comments on `AbandonedOrderCleanupWorker` and the
  `payment_intent.payment_failed` handler said cancellation releases the held
  coupon. An abandoned order holds none now, so both say what is actually true.

From the independent PR review, which ran the fix against seven mutations and
found two that nothing caught:

- **`app.ErrCouponAlreadyRedeemed` is gone.** `PlaceOrder` was its only
  producer, and a sentinel nothing can return is a trap for the next reader.
  Of its two consumers, the 422 arm in `handleCheckoutPaymentIntent` was one
  half of an `||` whose other half — `ErrCouponAlreadyUsed` — still fires, so
  dropping it changed nothing. `classifyCheckoutError`'s `coupon_redeemed` arm
  was deleted rather than repointed: its one caller is `handleCheckoutConfirm`,
  which cannot produce a coupon error at all, so the arm was dead before this
  change and would have stayed dead after. (An earlier draft of this document
  claimed the repoint fixed a live mislabelling. It did not — the second review
  caught that, and the arm is gone instead.)
- **`docs/CLAUDE-backend.md` said the loser of the race sees
  `ErrCouponAlreadyRedeemed`**, and called the placement race the
  highest-priority concurrency test. Both describe behaviour this change
  replaced; both now describe what it does.
- **The claim in `redeemOrderCoupon`'s comment that the row lock guards the
  redemption was wrong.** That lock is on the order and serializes two captures
  of one order. Two orders reaching for one code are arbitrated by the
  optimistic `UPDATE … WHERE redeemed_at IS NULL`. The code was right; the
  sentence was not.
- **The web test's header described the defect in the present tense**, which
  after merge reads as a description of what the code does. It now says what it
  did, and points at where redemption moved.
- **The admin lookup fallback had no test** — disabling it survived the whole
  suite. `TestDiscountService_GetCouponCodeForOrder` covers the unpaid, the
  captured and the no-coupon order, and was confirmed to fail with the fallback
  disabled. It does not pin the *redeemed-row* lookup: delete that branch and
  the metadata fallback answers the captured case too, so the suite stays
  green. The branch is kept for a fork writing its own redemption path, which
  is also why nothing here can hold it in place.

Two notes from that review are deliberately not acted on. There is no
Prometheus counter for a lost redemption, because the only place to increment
one is inside the capture transaction and this repo increments after commit;
the audit row is the trace. And `MarkCouponCodeRedeemed` sets `redeemed_at`
without `redeemed_by_order_id`, which would misfile as a loss — it is reachable
only from test code today, but a fork writing its own redemption path should
either teach it that column or stop using it.

### Tests

- `internal/web/checkout_coupon_reuse_test.go` —
  `TestCheckoutPaymentIntent_SecondIntentKeepsTheCoupon`, the original
  specification. Assertions 4 and 5 were confirmed to fail with 0 and 5000
  against the reverted fix before it was trusted, per item 4.
- `internal/app/checkout_coupon_lifecycle_test.go` — where the code is spent
  and when: placement prices it in without spending it, capture spends it and
  ties it to the order, cancelling an unpaid order leaves it usable, and a
  second capture on one code still captures while the code stays with whoever
  paid first. The first and last of those were also confirmed to fail against
  the reverted fix. `TestCheckoutService_RecapturingOneOrderIsNotALostRedemption`
  covers the re-entry case above and was likewise confirmed to fail — filing
  one `coupon.redemption_lost` — without its guard.

### Left alone, on purpose

- **The orphaned orders** (item 5). Expected, documented at
  `Payment.svelte:81`, and swept at 24 hours. The coupon no longer depends on
  them being dealt with.
- **Phase 1 still prices a genuinely-spent coupon out of the cart silently.**
  That path is now only reachable when someone else really did pay with the
  code, rather than on every second intent, but the customer is still shown a
  higher total with nothing said. Telling them properly means a response field
  and copy on the payment step — a change to the client, not to this seam.
- The two items in *Related, smaller* above — the missing Stripe idempotency
  key and the missing per-IP limit on the checkout PI route — are untouched and
  still open.
