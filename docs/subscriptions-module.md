# Subscriptions

Standing orders: a customer subscribes to a coffee on a plan, and the shop
charges and ships it on a schedule until they stop. Retail only; wholesale has
no recurring orders. Ported from hiri-core's `docs/subscriptions-module.md` in
October 2026 with the multi-item signup, and edited for this shop: subscriptions
here are always on (there is no module toggle), and there are no made-to-order
recipes.

## 1. Why it exists

Coffee sells on repeat, and a customer who has to come back and check out every
month eventually does not. A subscription removes the checkout from every order
after the first.

The multi-item signup answers a specific complaint: a customer wanted three
coffees on subscription, checked out three times, and paid shipping three times.
On 2026-10-03 a subscriber trying to add a second coffee left two unpaid orders
behind. One signup now carries several items, and the items that renew together
ship together (§2, "The box").

## 2. The model

**A plan** (`subscription_plans`) is *how often* and *how much off*: an interval
in days (7, 14, 21, 30, 60, 90 — never calendar months, so "every 30 days" means
one thing) and a discount percentage. It names no product. A customer can change
what they receive without changing their schedule, and the reverse.

**A subscription** (`subscriptions`) is one variant, one quantity, one plan and
one shipping address. One row per item; there is no subscription-items table. It
carries its schedule as `current_period_start`, `current_period_end` and
`next_order_at`, and its dunning state in `metadata`.

**A renewal** is an ordinary order and an ordinary off-session PaymentIntent. No
Stripe Billing objects: the shop keeps control of retries, grace and dunning,
and customers migrated in with saved payment methods rather than Billing tokens
carried straight across. Each renewal order is linked to the subscriptions it
renewed through `subscription_orders`.

**A signup** is the first order. The subscribe flow pre-creates it at
PaymentIntent time, pending, with `subscription_signup` in its metadata and each
line's plan in that line's metadata. Payment success — the confirm endpoint or
the `payment_intent.succeeded` webhook, whichever lands first — activates it:
one subscription per line, all started from one clock reading, and one
confirmation email listing every item. A signup order written before lines
carried their plan names it at the order level instead; `legacySignupLine` reads
that for a one-line order, and can go once no such order's PaymentIntent can
still be paid.

**`orders.subscription_id`** names one subscription. An order that started or
renewed several — a signup of several items, any batched renewal — leaves it
null and is found through `subscription_orders`. Every reader that asks "which
subscriptions is this order" goes through both: the admin order page
(`orderSubscriptionIDs`) and the dashboard's subscription revenue
(`SumOrderRevenue`).

### The box

A **box** is the customer's active and past-due subscriptions that share a
shipping address and the **same `next_order_at` instant**. It is derived every
time it is needed (`domain.GroupIntoBoxes`) and never stored — no `box_id`, no
group table.

This is not the "open box" of `CheckoutService.OpenShipmentTo`, which is a paid,
unpacked order a new one can be packed with (see `CLAUDE-backend.md`, *A signup
order packed with an open order ships free*). In customer-facing copy "box"
means this one only.

- **Keyed on the instant, not the day.** The renewal scheduler takes everything
  due at a run and groups it by customer and address, and every `next_order_at`
  is snapped to the renewal anchor, so "renews in one run" is "same instant".
  Two subscriptions can share a calendar day and not a run — after
  `RENEWAL_ANCHOR_HOUR` changes, existing rows keep the old hour — and those
  ship as two orders. The account page labels a box by its day in the
  merchant's zone, and shows two boxes on one day as two.
- **Not a table**, because nothing about a box needs remembering. It is exactly
  what the scheduler will batch, read off the rows the scheduler reads. A stored
  group would be a second answer that could disagree with the first. Existing
  single-item subscriptions placed on one day already renew as one order, and
  show as one box from the first deploy; nothing was backfilled.
- **Skip does not drag.** Skipping or pausing one subscription leaves its
  box-mates where they are; it simply moves to another box. The customer is told
  what separate days cost (below) and offered a whole-box action instead.
- **Whole-box skip is by date only.** Skipping a number of shipments walks each
  member's own cadence, so a weekly and a four-weekly that coincide today would
  land on different days — the box split by the action meant to keep it whole.
  One restart day keeps them together. `SubscriptionService.SkipBox`.
- **Whole-box actions are all or nothing.** Every member is checked before any
  is written; one past-due member (an unpaid charge to settle first) refuses the
  whole skip or pause.
- **Same plan, same box; different plans, different boxes.** Lines of one signup
  on one plan start in one box. On different plans they are in different boxes
  from the start — only the signup order ships them together.
- **Local delivery.** A box's members share an address and a customer, so they
  share the local-fulfillment preference. `RenewBatch` writes one order and
  resolves its method once (`renewalCharges`), so a box at a local address
  renews as one delivery, never as one delivery and one parcel.

Two places the box and the scheduler disagree, accepted:

- **A dead-card member** — one carrying a permanently declined card — is routed
  to a solo renewal and dropped from any batch. The page shows it inside its box,
  marked as shipping and charged separately, and counts it as a separate
  shipment for the notice.
- **A backlog** (a worker outage) makes everything overdue due at one run, so
  two boxes can ship as one order. The page then over-states the shipping; the
  customer is charged less than it says, never more.

And one accepted cost: a declined batch runs **one dunning ladder per member**,
so a box of three sends three emails per rung.

**The two notices.** The signup page, when its lines are on more than one plan:
"Mixed schedules: it all ships together this first time — after that, each
schedule rides out on its own day, with its own shipping." A single-plan signup
gets no notice. The account page, when one address has more than one shipment
coming: "These go out on different days — each shipment carries its own
shipping charge", adding "Skip one to line them up" only when the items share a
plan — a weekly and a monthly drift apart again whatever day they are moved to.

## 3. Rules worth knowing before changing anything

- **The plan discount is a property of the line.** `OrderLine.PlanDiscountPct`,
  declared by the caller because the subscription does not exist yet when the
  signup is priced. `TestPriceLines_DiscountsEachLineByItsOwnPlan`.
- **A subscription prices from the base.** Every `PriceLines` and `PlaceOrder`
  call on the subscribe path passes `BasePrice: true`, because `RenewalService`
  charges `GetBasePrice` less the plan discount whatever price list the
  customer is on; a signup resolved through the list would charge the first box
  one number and every box after it another. It also makes the cart's volume
  rungs irrelevant, so a box holding one variant on two plans prices both lines
  the same way the cart would.
  `TestPriceLines_ASubscriptionPricesFromTheBaseLikeItsRenewals`.
- **One price computation.** The subscribe page, the payment intent and
  `PlaceOrder` all price through `CheckoutService.PriceLines`. Because a
  subscription prices from the base, the page needs no customer: a signed-out
  visitor is quoted what they will be charged.
  `TestSubscribePage_PricesMatchThePaymentIntentForTheSameCustomer`.
- **Shipping once per signup and once per renewal order**, and nothing for a
  signup packed with an open order.
  `TestSubscribePaymentIntent_TwoItemsShipForOneShippingCharge`,
  `TestSubscribePaymentIntent_ASecondSignupRidesWithAnOpenOrder`,
  `TestRenewBatch_OneSignupRenewsAsOneOrderWithOneShippingCharge`.
- **Activation is all or nothing.** Every line's plan is read before any
  subscription is written, and the whole signup is one transaction.
- **Never assume line order, and match on the whole key.** Order lines come
  back sorted by a random UUID. Match a line on everything that makes it one
  line — plan and variant, the key signup lines are merged on. One variant can
  be in a box on two plans at two prices; matched by variant alone, both prices
  land on one line. `TestSubscribePaymentIntent_EachPricedLineNamesItsPlan`.
- **A renewal claims its subscriptions.** `renewal_claimed_at` (migration 090)
  is taken, all or nothing, before phase 1 and released after phase 3; a second
  entrant gets `ErrRenewalInFlight` and River retries it. Under the claim, an
  active subscription that is not due has already been renewed and is refused
  (`ErrRenewalNotDue`) or dropped from a batch. This is what stops a Retry
  double-charging a batch member. `internal/app/renewal_claim_test.go`.
- **Renewal insert options are one function.** `jobs.RenewalInsertOpts`, never
  a literal: options that differ hash to different unique keys and deduplicate
  against nothing.
- **The anchor is forward-only and merchant-local.** Every `next_order_at` is
  snapped to `RENEWAL_ANCHOR_HOUR` in the merchant's zone, so renewals land
  pre-dawn and a box is one instant. Resume places the next order at the next
  anchor, never a fresh interval out. `TestAnchorRenewalTimeAcrossDST`.
- **The dunning ladder** retries on a fixed schedule with a one-click card link.
  A permanently declined card is never charged again; the ladder walks on
  without Stripe and releases only when a different card appears.
  `renewal_dunning_test.go`.

## 4. What happens when the catalog moves under it

- **An archived variant or a deactivated plan** stops new signups and leaves
  existing subscriptions renewing. A signup already paid for still activates.
- **A price change** reaches the next renewal: renewals price from the base
  price at the time, less the plan's discount.
- **A wholesale-only variant** is never offered by the `/subscribe` picker,
  which lists the retail channel's variants of subscribable products.
- **Past orders** keep what they were charged; line prices are copied onto the
  order.

## 5. How it plugs in

| Core service | Question it asks |
|---|---|
| `CheckoutService.PriceLines` | what a signup line costs, at its plan's discount, from the base |
| `CheckoutService.PlaceOrder` | writes the signup order and each line's plan |
| `CheckoutService.OpenShipmentTo` | whether the signup can be packed with an open order |
| `SubscriptionService.ActivateFromSignupOrder` | turns a paid signup into subscriptions |
| `RenewalService.RenewSubscription` / `RenewBatch` | charges and places a renewal |
| `jobs.RenewalSchedulerWorker` | what is due, grouped by customer and address |

## Known risks, not fixed by the port

hiri-core's review of its renewal code found these, and this shop's renewal
code is the same code. Only the Retry double charge (above) was fixed here.

- A renewal can charge the card and then fail to write the order.
- A subscription more than one interval overdue is billed for each missed
  period back to back rather than once.
- A declined batch runs one dunning ladder per member.
- Dunning state lives in `metadata` rather than in columns.

## Decided, and not to be reopened without reading why

- One variant per subscription row; no `subscription_items` table.
- Subscriptions from one signup are separate rows on the account page, grouped
  under a box heading; no merged card.
- A box is one renewal run at one address, derived, never stored.
- Skipping or pausing one subscription does not drag its box-mates.
- Lines are assembled on the `/subscribe` page; the URL carries them, so
  Stripe's redirect lands back on the same box. No subscription cart table: it
  would need expiry and a migration for what the URL already does. The product
  page's subscribe button starts a fresh one-line box.
- At most 10 lines per signup and 1 to 10 of each, checked after duplicates are
  merged; over the cap is refused, never clamped.

## Deferred, on purpose

- **"Add to my next box"** from the product page: a first period shorter than
  the plan interval, and a pro-rating question nobody has answered.
- **Whole-box resume.** Paused subscriptions have no box. Rows resumed the same
  day land on the same run anyway.
- **One combined skip email per box.** Per-member emails are correct, just
  chatty.
- **One dunning ladder per customer.**
- **Admin list grouping by box.**
- **Staff creating a subscription** from the admin. Still not possible.
