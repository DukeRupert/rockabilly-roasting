# Batched Subscription Renewals

> Design plan for consolidating multiple subscriptions into a single renewal order when they share the same customer and renewal date.

---

## Problem

A customer who subscribes to multiple products (e.g., 12oz PNG Whole Bean + 3lb House Blend Drip) currently gets a separate order and separate Stripe charge for each subscription. This is a poor customer experience (multiple charges, multiple shipments) and operationally wasteful (multiple picks, packs, and labels for the same address on the same day).

## Solution

When subscriptions for the same customer come due on the same day, batch them into a single order with multiple line items and a single PaymentIntent. Each subscription still exists independently — customers can pause, cancel, change frequency, or modify one subscription without affecting others.

### What stays the same

- Subscription model: one variant + quantity per subscription (no multi-item subscriptions)
- Product page subscribe flow: creates a single-product subscription as today
- Subscription lifecycle: pause/resume/cancel operate on individual subscriptions
- `subscription_plans` and `subscription_orders` tables unchanged
- Admin subscription management unchanged

### What changes

- **Renewal scheduler**: groups due subscriptions by customer before enqueuing jobs
- **Renewal worker**: processes a batch of subscription IDs instead of one
- **Renewal service**: creates one order with multiple line items, one PaymentIntent, and links all subscriptions to that order

---

## Implementation Plan

### Phase 1: Scheduler — Group by Customer

**Current flow:**
```
RenewalScheduler.Work()
  → ListDueForRenewal() returns []Subscription (status=active, next_order_at <= now)
  → For each: enqueue SubscriptionRenewalArgs{SubscriptionID}
```

**New flow:**
```
RenewalScheduler.Work()
  → ListDueForRenewal() returns []Subscription (unchanged query)
  → Group subscriptions by customer_id
  → For each customer group: enqueue BatchRenewalArgs{SubscriptionIDs: [...]}
```

**Changes:**
- `internal/jobs/renewal_scheduler.go` — group results by `CustomerID`, enqueue `BatchRenewalArgs` instead of `SubscriptionRenewalArgs`
- New `BatchRenewalArgs` struct with `SubscriptionIDs []uuid.UUID` and `Kind() = "batch_renewal"`

**Edge case:** If a customer has subscriptions on different plans with different `next_order_at` dates, they won't batch — they'll renew on different days as separate orders. This is correct behavior. Batching only happens when dates align naturally.

### Phase 2: Batch Renewal Service

**New method:** `RenewalService.RenewBatch(ctx, pool, subscriptionIDs []uuid.UUID) (*domain.Order, error)`

**Flow:**

1. **Read phase** (single read tx):
   - Load all subscriptions by ID, verify all are active/past_due
   - Verify all belong to the same customer (safety check)
   - Load the customer, shipping address (use the first subscription's address — see open question below)
   - For each subscription: load plan + variant price, apply plan discount, compute line total
   - Sum all line totals for the order total

2. **Payment phase** (external, outside tx):
   - Create one PaymentIntent for the total amount
   - On failure: mark all subscriptions past_due

3. **Write phase** (single tx):
   - Create one Order with `subtotal = sum of all line totals`
   - Create one LineItem per subscription (variant_id, quantity, unit_price from that subscription)
   - Store Stripe PI ID on the order
   - For each subscription:
     - Link via `subscription_orders` (subscription_id, order_id, period_start, period_end)
     - Advance billing period (each subscription advances independently based on its own plan)
   - Audit record

**The existing `RenewSubscription` method stays** for backwards compatibility and for the case where a customer has exactly one due subscription (the scheduler will still create a batch of size 1).

### Phase 3: Batch Renewal Worker

**New worker:** `BatchRenewalWorker` processes `BatchRenewalArgs`.

```go
type BatchRenewalArgs struct {
    SubscriptionIDs []uuid.UUID `json:"subscription_ids"`
}

func (BatchRenewalArgs) Kind() string { return "batch_renewal" }
```

**Worker logic:**
- Call `RenewalService.RenewBatch(ctx, pool, args.SubscriptionIDs)`
- On `ErrSubscriptionNotActive` for any subscription in the batch: filter it out and retry with remaining subscriptions (don't fail the whole batch)
- If all subscriptions are inactive/cancelled: cancel the job

### Phase 4: Deprecate Single Renewal Path

Once batch renewals are validated:
- Remove `SubscriptionRenewalArgs` / `SubscriptionRenewalWorker`
- The scheduler only enqueues `BatchRenewalArgs` (even for single-subscription batches)
- `RenewBatch` handles batches of any size, including 1

---

## Shipping Address Handling

**Open question:** When batching subscriptions, which shipping address to use?

**Proposed approach:** Use the shipping address from the first subscription in the batch. In practice, a customer's subscriptions will almost always share the same address. If they have subscriptions shipping to different addresses, the system should NOT batch them — they need separate shipments.

**Refinement for Phase 1:** Group by `(customer_id, shipping_address_id)` instead of just `customer_id`. This ensures subscriptions going to different addresses stay as separate orders.

---

## Order ↔ Subscription Linking

Currently `orders.subscription_id` is a nullable FK pointing to a single subscription. With batched renewals, one order maps to multiple subscriptions.

**No schema change needed.** The `subscription_orders` join table already handles the N:N relationship. The `orders.subscription_id` column can be set to the first subscription in the batch (or left null for batched orders) — it's a convenience field, not the authoritative link. The `subscription_orders` table is the source of truth.

**Proposed:** Set `orders.subscription_id = NULL` for batched orders (more than one subscription). The join table is always populated. Admin UI and queries should use `subscription_orders` to find linked subscriptions.

---

## Metrics

- `subscription_renewals_total{result="success"}` — increment once per batch (not per subscription)
- `subscription_renewal_failures_total{reason="..."}` — increment once per batch
- New: `subscription_renewal_batch_size` histogram — track how many subscriptions per batch

---

## Testing Plan

1. **Unit tests** (mock payment provider):
   - Single subscription batch (backwards compatible with current behavior)
   - Multi-subscription batch: correct total, correct line items, all subscriptions advanced
   - Mixed plans in batch: each subscription advances by its own interval
   - One cancelled subscription in batch: filtered out, rest proceed
   - Different addresses: not batched together

2. **Integration test** (Stripe test mode, 2-minute dev interval):
   - Create two subscriptions for same customer, same plan
   - Wait for renewal scheduler
   - Verify: one order with two line items, one Stripe charge, both subscriptions advanced

---

## Files to Change

| File | Change |
|---|---|
| `internal/jobs/renewal_scheduler.go` | Group by (customer_id, shipping_address_id), enqueue `BatchRenewalArgs` |
| `internal/jobs/subscription_renewal.go` | Add `BatchRenewalArgs`, `BatchRenewalWorker` |
| `internal/app/renewal.go` | Add `RenewBatch` method |
| `cmd/server/main.go` | Register `BatchRenewalWorker` with River |
| `internal/platform/metrics/registry.go` | Add batch size histogram (optional) |

No database migrations needed. No store changes. No UI changes.

---

## Future: Customer Self-Service

Once batched renewals work, the customer account page can show:

- "Your next delivery: March 20, 2026" with all products listed
- "Add another product" linking to the catalog (filtered to subscribable products)
- Individual subscription management (pause/cancel/swap) without affecting siblings

This is out of scope for this plan but is the natural next step.
