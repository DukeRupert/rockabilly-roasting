# Holiday promos and referral program

Client request, received 2026-11-27. Three offers plus tracking. None of it
worked on the platform as it stood: every coupon code was single-use globally,
and the subscription signup takes no coupon at all.

Deadlines: GIFT20 ends **2026-12-21**, WELCOME50 ends **2026-12-31**.

## The asks

1. **WELCOME50**: 50% off the first subscription order. New subscribers only,
   one use per customer. Ends 12/31.
2. **GIFT20**: 20% off any one-time order, active Daily Grind members only.
   Ends 12/21.
3. **Referral program**: a unique code per member. The friend gets the WELCOME50
   deal on their first subscription order, and the referring member earns a free
   bag. Fallback if per-member codes are too much: one shared code plus a "who
   referred you?" field at checkout.
4. **GA4**: send the code used on each order in the `purchase` event's `coupon`
   field.
5. **Reporting**: some way for the client to see redemptions and sales (export,
   spreadsheet or dashboard).

## Checklist

In build order. Items 1 to 3 shipped in #81 (v1.129.0), item 4 in #82
(v1.130.0). GIFT20 and WELCOME50 are live.

### 1. Codes many customers can use, each once per customer
- [x] Discount-level usage setting: `single_use` (today's behaviour) or
      `once_per_customer`. Set at creation only, like the code itself
- [x] Redemption ledger (`coupon_redemptions`) keyed on (discount, customer),
      so a customer can't use the same offer twice, even through different
      codes of one discount (this matters for the referral codes in item 5)
- [x] One rule check (`CheckoutService.checkCoupon`) used by the code box, the
      cart, the payment intent and placement. Capture keeps the
      first-to-pay-wins rule from `checkout-coupon-scope-TODO.md`
- [x] Cancelling an order releases the ledger row
- [x] A coupon refused at the payment step comes off the cart, and the shopper
      is told why (`coupon_notice`). It used to be dropped silently
- [x] Admin create form exposes the setting; the list and show pages display
      it and the redemption count, and the show page lists recent orders
- [x] Enforce `starts_at` (stored, but never checked at checkout before)

### 2. GA4 coupon field
- [x] `loadOrderAnalytics` sets `coupon` from the order's coupon code
- [x] `rrPurchase` passes it through to `gtag('event','purchase')`
- [ ] Subscription signup purchase gets it too, once item 4 lands

### 3. GIFT20: active members only, one-time checkout
- [x] Discount-level eligibility setting: `anyone` or `subscribers`. Editable,
      unlike the usage limit
- [x] `subscribers` needs a signed-in customer with an **active**
      subscription (paused and past-due don't count, per the client), on an
      order for themselves. A guest is told to sign in, with a link back to checkout;
      the cart survives signing in
- [x] Re-checked at placement, so a code applied before a cancel or sign-out
      doesn't carry through
- [x] Admin create and edit forms expose it; list and show pages display it

### 4. WELCOME50: subscription signup coupons
- [x] Coupon field on the subscribe flow (`SubscribeApp.svelte`); the code
      goes with every payment intent, and a refused one comes back as
      `coupon_notice`
- [x] `discounts.applies_to`: `one_time` (default) or `subscription_signup`.
      Each kind of code is refused on the other kind of order
- [x] Discount applies to the **first order only**; renewals are priced from
      the plan and never see it
- [x] Eligibility `new_subscribers`: the customer has never had a
      subscription, in any status
- [x] Redemption on signup capture, using the ledger from item 1
- [x] Tax and shipping priced on the discounted subtotal, as at cart checkout
- [x] Create WELCOME50 in admin once deployed: 50%, once per customer, new
      subscribers only, first subscription order, last valid day 12/31

### 5. Referral program
- [ ] One code per active member under a single referral discount
      (`coupon_codes.customer_id` = the owner)
- [ ] Generate codes on demand, show them on the account page
- [ ] Stop members redeeming their own code
- [ ] Record the referrer on redemption; a "free bags owed" list in admin
- [ ] Decide how the free bag is delivered: staff fulfil it by hand, or it's
      added to the member's next renewal

### 6. Reporting
- [ ] CSV export per discount: order number, date, customer, code, referrer,
      discount amount, order total
- [ ] Decide with the client whether a scheduled emailed export is worth
      building on top of that

## Notes
- A members-only code needs a sign-in. A guest checkout can't prove membership,
  and trusting the email typed into the form would let anyone who knows a
  member's address claim the discount.
