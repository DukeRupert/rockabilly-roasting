# Lean Commerce — Subscription Design

> Design decisions and architecture for the subscription system.
> Last updated: March 2026

---

## Design Philosophy

Subscriptions are a **scheduling mechanism**, not a billing engine. Each renewal generates a standard Order with a Stripe PaymentIntent — there are no Stripe Subscriptions or Stripe Billing objects. This keeps the order and fulfillment pipeline unified: one-time purchases and subscription renewals flow through the same checkout, payment, and fulfillment code.

This approach was chosen for two reasons:

1. **WooCommerce migration compatibility.** The existing site uses WooCommerce Subscriptions with PaymentIntents (not Stripe Billing). Matching this model means subscriptions can be migrated without re-tokenizing payment methods.
2. **Full control over renewal logic.** Custom retry schedules, grace periods, and discount application are handled in application code without being constrained by Stripe's subscription lifecycle.

---

## Key Design Decisions

### Plans are decoupled from products

A `SubscriptionPlan` is a universal cadence template — it defines *how often*, not *what*. Plans have no foreign key to products or variants. Instead, the `Subscription` itself owns the `variant_id`, linking the customer to both a delivery frequency (plan) and a product (variant) independently.

**Why:** This mirrors how Black Rifle Coffee and similar DTC brands work. A customer subscribes to "Every 30 Days" delivery and picks a coffee. They can later swap to a different coffee without changing their schedule, or change frequency without touching their product selection.

### Product subscribability is opt-in

Products have a `subscribable` boolean (default `false`). Only products marked as subscribable show subscription options on the storefront. Admin controls this via a toggle on the product edit page.

**Why:** Not all products make sense as subscriptions (e.g. merchandise, gift cards). This keeps the storefront clean and gives the merchant explicit control over which products appear with "Subscribe & Save" options.

### Day-based intervals, not calendar-based

Intervals are fixed day counts, not calendar units:

| Interval | Days | Enum value |
|---|---|---|
| Every 14 Days | 14 | `every_14_days` |
| Every 21 Days | 21 | `every_21_days` |
| Every 30 Days | 30 | `every_30_days` |
| Every 60 Days | 60 | `every_60_days` |

**Why:** Modeled after Black Rifle Coffee's shipping frequency options. Day-based intervals are simpler to reason about than "monthly" (which month? 28, 30, or 31 days?) and align with how customers think about consumable reorders.

### Discounts live on plans, not on products

Each plan has a `discount_pct` (integer, 0–100). When a subscription order is created, the discount is applied to the variant's base price. This means the same product can have different effective prices depending on the chosen frequency.

**Why:** "Subscribe & Save 10%" is a function of the commitment (frequency), not the product. A plan offering "Every 14 Days — Save 15%" incentivizes higher-frequency orders across all subscribable products.

---

## Data Model

### SubscriptionPlan

| Field | Type | Description |
|---|---|---|
| id | uuid PK | |
| name | text | Display name (e.g. "Every 30 Days") |
| interval | enum | `every_14_days`, `every_21_days`, `every_30_days`, `every_60_days` |
| interval_count | int | Multiplier (default 1, reserved for future use) |
| discount_pct | int | Percentage discount (0 = no discount) |
| is_active | bool | Whether this plan is offered to new subscribers |
| metadata | jsonb | Extensible key-value data |

### Subscription

| Field | Type | Description |
|---|---|---|
| id | uuid PK | |
| customer_id | uuid FK → customers | |
| plan_id | uuid FK → subscription_plans | Delivery cadence |
| variant_id | uuid FK → variants | What gets shipped |
| status | enum | `active`, `paused`, `past_due`, `cancelled`, `expired` |
| shipping_address_id | uuid FK → addresses | |
| current_period_start | timestamptz | Start of current billing period |
| current_period_end | timestamptz | End of current billing period |
| next_order_at | timestamptz | When the next renewal order is created |
| cancelled_at | timestamptz | When cancellation occurred (null if active) |
| pause_until | timestamptz | Resume date when paused (null if not paused) |
| metadata | jsonb | Extensible key-value data |

### SubscriptionOrder

Join table linking generated orders back to the subscription.

| Field | Type | Description |
|---|---|---|
| subscription_id | uuid FK | Parent subscription |
| order_id | uuid FK | Generated order |
| period_start | timestamptz | Period this order covers |
| period_end | timestamptz | Period end |

### Product (relevant field)

| Field | Type | Description |
|---|---|---|
| subscribable | bool | Whether this product can be subscribed to (default false) |

---

## Relationships

```
SubscriptionPlan  1 ←——— N  Subscription  (cadence)
Variant           1 ←——— N  Subscription  (what ships)
Customer          1 ←——— N  Subscription
Subscription      1 ←——— N  Order         (via SubscriptionOrder)
```

A customer can have multiple active subscriptions (e.g. two different coffees on different schedules). When multiple subscriptions share the same customer and shipping address and come due on the same day, the renewal scheduler batches them into a single order with multiple line items and one Stripe charge. See `docs/batched-subscription-renewals.md` for the full design.

---

## Subscription Lifecycle

### Status State Machine

```
         ┌──────────┐
    ┌───→│  active   │←──────────────┐
    │    └────┬──┬───┘               │
    │         │  │                   │
    │   pause │  │ payment fails     │ payment succeeds
    │         ▼  ▼                   │
    │    ┌────────┐    ┌──────────┐  │
    │    │ paused │    │ past_due ├──┘
    │    └────┬───┘    └────┬─────┘
    │         │             │
    │  resume │     max retries exceeded
    │         │             │
    └─────────┘             ▼
                      ┌───────────┐
         cancel ────→ │ cancelled │
                      └───────────┘
```

- **active** → `paused` (customer request), `past_due` (payment failure), `cancelled` (customer or admin)
- **paused** → `active` (resume, manually or auto via `pause_until`)
- **past_due** → `active` (successful retry payment), `cancelled` (max retries exceeded)
- **cancelled** — terminal state
- **expired** — terminal state (reserved for fixed-term subscriptions)

### Renewal Flow (Batched)

When multiple subscriptions for the same customer and shipping address come due on the same day, they are batched into a single order with one Stripe charge.

1. River periodic job (`RenewalScheduler`) polls `ListDueForRenewal()` — finds subscriptions where `next_order_at <= now()` and status is `active`
2. Groups results by `(customer_id, shipping_address_id)` into batches
3. Enqueues one `BatchRenewalArgs` job per group (containing all subscription IDs)
4. `BatchRenewalWorker` calls `RenewalService.RenewBatch()`:
   a. For each subscription: look up variant price, apply `plan.discount_pct`, compute line total (`unit_price * quantity`)
   b. Sum all line totals for the order total
   c. Create a single Stripe PaymentIntent (off-session) for the total amount
   d. Create one Order with one LineItem per subscription
   e. Link each subscription via `subscription_orders`
   f. Advance each subscription's period independently (plans may differ)
5. On payment failure: mark all subscriptions in the batch `past_due`
6. On retry success: restore to `active`

Single-subscription batches delegate to the existing `RenewSubscription` method for backwards compatibility.

For the full batched renewal design, see `docs/batched-subscription-renewals.md`.

### Checkout Flow (New Subscription)

1. Customer visits a subscribable product page, sees "Subscribe & Save" options with quantity selector
2. Customer picks a plan and quantity → routed to `/subscribe?plan_id=X&variant_id=Y&quantity=N`
3. Subscribe page shows plan summary with discounted price × quantity
4. Svelte checkout component creates a PaymentIntent with setup for future use
5. On payment confirmation: create Subscription + initial Order in one transaction

Each subscription is single-product. Customers who want multiple products subscribe separately — the system batches them into one order at renewal time (see Renewal Flow above).

---

## Admin Interface

### Plan Management (`/admin/plans`)

- Create/list subscription plans with name, interval, and discount percentage
- Plans are global — they appear on all subscribable products
- Toggle plan active/inactive status

### Product Subscribability (`/admin/catalog/{id}`)

- Toggle switch on the product edit page (Subscriptions panel)
- Uses htmx to update in-place without page reload
- Error feedback via OOB toast notification; success is silent

### Subscription Management (`/admin/subscriptions`)

- List all subscriptions with status, customer, and plan info
- Detail page with pause/resume/cancel actions
- View renewal history (linked orders)

---

## Storefront Behavior

- **Product page:** If `product.subscribable == true`, a "Subscribe & Save" section appears below "Add to Cart" showing all active plans with their discounts
- **Subscribe links** include both `plan_id` and `variant_id` so the subscription is tied to the specific variant the customer is viewing
- **Price display:** Shows the discounted price (base price minus plan discount percentage)

---

## Payment Architecture

- **Stripe PaymentIntents only** — no Stripe Subscriptions, no Stripe Billing
- Initial subscription checkout creates a PaymentIntent with `setup_future_usage: off_session`
- Renewals create off-session PaymentIntents using the customer's saved PaymentMethod
- All payment state is tracked in the Order, not the Subscription
- Refunds go through the standard order refund flow

---

## Future Considerations

These are noted but not yet implemented:

- **Product swaps:** Customer self-service to change the variant on an active subscription
- **Frequency changes:** Customer self-service to switch plans on an active subscription
- **Skip a delivery:** Pause for exactly one period, then auto-resume
- **Customer self-service portal:** View all subscriptions, see next delivery date with all batched products
- **Gift subscriptions:** Fixed-term subscriptions purchased for another person
- **Win-back flows:** Re-engagement emails for cancelled subscriptions
- **Migration tooling:** Import existing WooCommerce subscriptions with saved payment methods
