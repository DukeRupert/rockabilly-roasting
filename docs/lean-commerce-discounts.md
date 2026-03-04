# Lean Commerce — Discounts

Discounts reduce the amount a customer pays. This document covers the domain model, calculation logic, redemption mechanics, and schema for the discount system. Implementation is deferred — this document exists to ensure the surrounding schema (orders, line items, cart) is designed to accommodate discounts without breaking changes when the time comes.

---

## Scope

**In scope:**
- Percentage off order subtotal
- Fixed amount off order subtotal
- Free shipping (overrides calculated shipping to zero)
- Automatic discounts (applied without a code, based on rules)
- Single-use coupon codes (unique code, redeemable once per customer)
- Conditions: minimum order value, expiry date, one use per customer

**Out of scope (deferred):**
- Line-item level discounts (percentage or fixed off specific products)
- Buy X get Y
- Tiered discounts
- Multi-use codes with a shared redemption limit
- Bulk code generation
- Customer group targeting
- Stackable discounts

---

## Core concepts

### Discount vs. coupon code

These are two distinct things that are often conflated:

- A **discount** is the rule: type, value, conditions, active window. It exists independently of any code.
- A **coupon code** is a redemption token that unlocks a discount. A discount may have zero codes (automatic) or many codes (one per customer).

This separation means the same discount logic (percentage off, minimum order, expiry) applies whether the discount is triggered automatically or by a code. The calculation path is identical — only the trigger differs.

```
Automatic discount:  evaluated against every cart automatically
Coupon code:         customer enters a code → code resolves to a discount → same evaluation
```

### Discount application

A discount produces an `AppliedDiscount` — a value object that records what was reduced and why. This is what gets frozen onto the order at purchase time.

---

## Schema

### `discounts` table

The discount rule. One row per discount.

```sql
CREATE TYPE discount_type AS ENUM (
    'percentage',     -- reduce subtotal by N%
    'fixed_amount',   -- reduce subtotal by N cents
    'free_shipping'   -- set shipping total to zero
);

CREATE TABLE discounts (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                text NOT NULL,          -- internal name, admin-facing only
    description         text,                   -- optional customer-facing description
    type                discount_type NOT NULL,
    value               int NOT NULL DEFAULT 0, -- percent (0-100) or cents; 0 for free_shipping
    minimum_order_cents int,                    -- null = no minimum
    starts_at           timestamptz,            -- null = active immediately
    expires_at          timestamptz,            -- null = never expires
    active              bool NOT NULL DEFAULT true,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
```

`value` interpretation by type:
- `percentage`: integer 0–100 (10 = 10% off)
- `fixed_amount`: cents (1000 = $10.00 off)
- `free_shipping`: ignored (value = 0)

### `coupon_codes` table

One row per unique code. A code belongs to exactly one discount.

```sql
CREATE TABLE coupon_codes (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    discount_id     uuid NOT NULL REFERENCES discounts(id),
    code            text NOT NULL,
    customer_id     uuid REFERENCES customers(id), -- null = any customer can use it
    redeemed_at     timestamptz,                   -- null = not yet redeemed
    redeemed_by     uuid REFERENCES customers(id), -- customer who redeemed it
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_coupon_code UNIQUE (code)
);

CREATE INDEX idx_coupon_codes_discount ON coupon_codes (discount_id);
CREATE INDEX idx_coupon_codes_customer ON coupon_codes (customer_id);
```

`customer_id` on a coupon code means the code is issued to a specific customer — only they can redeem it. `customer_id = null` means any authenticated customer can redeem it (first-come-first-served, one use per customer enforced by redemption check).

### `order_discounts` table

The frozen record of which discount was applied to an order and what it reduced. An order may have at most one discount applied (stacking is out of scope).

```sql
CREATE TABLE order_discounts (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id        uuid NOT NULL REFERENCES orders(id),
    discount_id     uuid NOT NULL REFERENCES discounts(id),
    coupon_code_id  uuid REFERENCES coupon_codes(id), -- null for automatic discounts
    type            discount_type NOT NULL,            -- denormalized at application time
    value           int NOT NULL,                      -- denormalized at application time
    amount_cents    int NOT NULL,                      -- actual reduction applied in cents
    applied_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_order_discount UNIQUE (order_id)    -- one discount per order
);
```

`type`, `value`, and `amount_cents` are denormalized onto `order_discounts` at the time of application. If the discount is later deactivated or its value changed, the historical record on the order is unaffected.

`amount_cents` is the actual reduction applied — for `percentage` and `fixed_amount` this is the subtotal reduction; for `free_shipping` this is the shipping total that was waived.

### Orders table additions

```sql
ALTER TABLE orders ADD COLUMN discount_total int NOT NULL DEFAULT 0;
-- The sum of all discount reductions applied to this order (subtotal discounts only).
-- Free shipping discount is reflected in shipping_total = 0, not here.
```

`Total` on an order is always:
```
total = subtotal - discount_total + shipping_total + tax_total
```

Free shipping sets `shipping_total = 0`; `discount_total` reflects only subtotal reductions.

---

## Domain types

```go
// domain/discount.go

type DiscountType string
const (
    DiscountTypePercentage  DiscountType = "percentage"
    DiscountTypeFixedAmount DiscountType = "fixed_amount"
    DiscountTypeFreeShipping DiscountType = "free_shipping"
)

type Discount struct {
    ID                 uuid.UUID
    Name               string
    Description        *string
    Type               DiscountType
    Value              int          // percent or cents; 0 for free_shipping
    MinimumOrderCents  *int         // nil = no minimum
    StartsAt           *time.Time   // nil = active immediately
    ExpiresAt          *time.Time   // nil = never expires
    Active             bool
    CreatedAt          time.Time
    UpdatedAt          time.Time
}

type CouponCode struct {
    ID          uuid.UUID
    DiscountID  uuid.UUID
    Code        string
    CustomerID  *uuid.UUID   // nil = any customer
    RedeemedAt  *time.Time
    RedeemedBy  *uuid.UUID
    CreatedAt   time.Time
}

// AppliedDiscount is the result of evaluating a discount against a cart.
// It is a value object — it does not persist on its own.
// It becomes an order_discounts row when the order is placed.
type AppliedDiscount struct {
    Discount      *Discount
    CouponCode    *CouponCode  // nil for automatic discounts
    AmountCents   int          // actual reduction
    FreeShipping  bool         // true if shipping_total should be set to zero
}
```

---

## Calculation logic

Discount calculation is a pure domain function. It takes a cart and a discount, applies conditions, and returns an `AppliedDiscount` or an error explaining why the discount cannot be applied.

```go
// domain/discount.go

var (
    ErrDiscountInactive      = errors.New("discount is not active")
    ErrDiscountExpired       = errors.New("discount has expired")
    ErrDiscountNotYetActive  = errors.New("discount is not yet active")
    ErrMinimumOrderNotMet    = errors.New("order does not meet minimum value for this discount")
    ErrCouponAlreadyRedeemed = errors.New("coupon code has already been redeemed")
    ErrCouponNotForCustomer  = errors.New("coupon code is not valid for this customer")
    ErrAlreadyUsedByCustomer = errors.New("this discount has already been used by this customer")
)

// Evaluate checks whether a discount is applicable to a cart and returns
// the AppliedDiscount if valid, or an error describing why it cannot be applied.
// now is passed explicitly to make the function testable without time.Now().
func (d *Discount) Evaluate(
    cart       *Cart,
    code       *CouponCode,   // nil for automatic discounts
    customerID uuid.UUID,
    usedBefore bool,           // true if this customer has already redeemed this discount
    now        time.Time,
) (*AppliedDiscount, error) {

    // Active check
    if !d.Active {
        return nil, ErrDiscountInactive
    }

    // Time window checks
    if d.StartsAt != nil && now.Before(*d.StartsAt) {
        return nil, ErrDiscountNotYetActive
    }
    if d.ExpiresAt != nil && now.After(*d.ExpiresAt) {
        return nil, ErrDiscountExpired
    }

    // Minimum order check
    if d.MinimumOrderCents != nil && cart.Subtotal < *d.MinimumOrderCents {
        return nil, ErrMinimumOrderNotMet
    }

    // One use per customer check
    if usedBefore {
        return nil, ErrAlreadyUsedByCustomer
    }

    // Coupon code checks (only when a code is being redeemed)
    if code != nil {
        if code.RedeemedAt != nil {
            return nil, ErrCouponAlreadyRedeemed
        }
        if code.CustomerID != nil && *code.CustomerID != customerID {
            return nil, ErrCouponNotForCustomer
        }
    }

    // Calculate the actual reduction
    applied := &AppliedDiscount{
        Discount:   d,
        CouponCode: code,
    }

    switch d.Type {
    case DiscountTypePercentage:
        applied.AmountCents = (cart.Subtotal * d.Value) / 100
    case DiscountTypeFixedAmount:
        applied.AmountCents = min(d.Value, cart.Subtotal) // never reduce below zero
    case DiscountTypeFreeShipping:
        applied.AmountCents = cart.ShippingTotal          // the amount being waived
        applied.FreeShipping = true
    }

    return applied, nil
}
```

`usedBefore` is determined by the service layer via a store query before calling `Evaluate` — the domain function receives a bool rather than making a database call.

---

## Service layer

### Applying a coupon code at checkout

```go
// app/checkout.go

func (s *CheckoutService) ApplyCouponCode(
    ctx        context.Context,
    tx         pgx.Tx,
    cart       *domain.Cart,
    code       string,
    customerID uuid.UUID,
) (*domain.AppliedDiscount, error) {

    // 1. Resolve the code
    coupon, err := s.coupons.GetByCode(ctx, tx, code)
    if err != nil { return nil, app.ErrDiscountNotFound }

    // 2. Load the discount
    discount, err := s.discounts.GetByID(ctx, tx, coupon.DiscountID)
    if err != nil { return nil, err }

    // 3. Check prior usage by this customer
    usedBefore, err := s.discounts.CustomerHasUsed(ctx, tx, discount.ID, customerID)
    if err != nil { return nil, err }

    // 4. Evaluate — pure domain logic
    applied, err := discount.Evaluate(cart, coupon, customerID, usedBefore, time.Now())
    if err != nil { return nil, err }

    return applied, nil
}
```

The applied discount is held in the cart session until checkout is confirmed — it is not persisted until the order is placed.

### Evaluating automatic discounts

```go
// app/checkout.go

func (s *CheckoutService) EvaluateAutomaticDiscounts(
    ctx        context.Context,
    tx         pgx.Tx,
    cart       *domain.Cart,
    customerID uuid.UUID,
) (*domain.AppliedDiscount, error) {

    // Load all active automatic discounts (no coupon code)
    discounts, err := s.discounts.ListActive(ctx, tx)
    if err != nil { return nil, err }

    now := time.Now()
    for _, d := range discounts {
        usedBefore, err := s.discounts.CustomerHasUsed(ctx, tx, d.ID, customerID)
        if err != nil { return nil, err }

        applied, err := d.Evaluate(cart, nil, customerID, usedBefore, now)
        if err != nil {
            continue // this discount doesn't apply — try the next
        }
        return applied, nil // return the first applicable discount
    }

    return nil, nil // no automatic discount applies
}
```

Only one automatic discount applies per order (stacking is out of scope). The first applicable discount wins. Order of evaluation is by `created_at` ascending — oldest discount wins in the case of multiple eligible discounts.

### Finalizing discount on order placement

When the order is placed, the applied discount (if any) is written to `order_discounts` and the coupon code is marked redeemed — all in the same transaction as the order creation:

```go
// app/orders.go

func (s *OrderService) PlaceOrder(
    ctx     context.Context,
    tx      pgx.Tx,
    cart    *domain.Cart,
    applied *domain.AppliedDiscount, // nil if no discount
    customer *domain.Customer,
    ...
) (*domain.Order, error) {

    discountTotal := 0
    shippingTotal := cart.ShippingTotal

    if applied != nil {
        if applied.FreeShipping {
            shippingTotal = 0
        } else {
            discountTotal = applied.AmountCents
        }
    }

    order, err := s.orders.Create(ctx, tx, store.CreateOrderParams{
        CustomerID:    customer.ID,
        Subtotal:      cart.Subtotal,
        DiscountTotal: discountTotal,
        ShippingTotal: shippingTotal,
        TaxTotal:      ...,
        Total:         cart.Subtotal - discountTotal + shippingTotal + taxTotal,
        ...
    })
    if err != nil { return nil, err }

    // Persist the discount record
    if applied != nil {
        couponID := (*uuid.UUID)(nil)
        if applied.CouponCode != nil {
            couponID = &applied.CouponCode.ID
        }

        err = s.discounts.RecordApplication(ctx, tx, store.RecordDiscountParams{
            OrderID:       order.ID,
            DiscountID:    applied.Discount.ID,
            CouponCodeID:  couponID,
            Type:          applied.Discount.Type,
            Value:         applied.Discount.Value,
            AmountCents:   applied.AmountCents,
        })
        if err != nil { return nil, err }

        // Mark coupon code redeemed
        if applied.CouponCode != nil {
            err = s.coupons.MarkRedeemed(ctx, tx, applied.CouponCode.ID, customer.ID)
            if err != nil { return nil, err }
        }
    }

    return order, nil
}
```

Order creation, discount record, and coupon redemption are all in the same transaction. If any step fails, none of them commit — no partially-applied discounts.

---

## Concurrency and the redemption race condition

Single-use coupon codes have a race condition: two concurrent checkouts by the same customer could both pass the `RedeemedAt = nil` check and both attempt to redeem the code. The unique constraint on `coupon_codes.code` does not prevent this.

The fix is an optimistic update with a condition:

```sql
-- store/coupons.go
UPDATE coupon_codes
SET    redeemed_at = now(), redeemed_by = $1
WHERE  id = $2
AND    redeemed_at IS NULL   -- only succeeds if not yet redeemed
RETURNING id
```

If this query returns zero rows, the code was redeemed by a concurrent request. The service returns `ErrCouponAlreadyRedeemed` and the transaction rolls back. No distributed lock needed — the database enforces it atomically.

---

## Audit actions

Add to `platform/audit/actions.go`:

```go
AuditDiscountCreated  = "discount.created"
AuditDiscountUpdated  = "discount.updated"
AuditDiscountDeactivated = "discount.deactivated"
```

Discount application is recorded in `order_discounts` rather than the audit log — the order record is the authoritative source of what discount was applied and at what value. The audit log records admin actions on the discount configuration itself.

---

## Package placement

| Concern | Location |
|---|---|
| `Discount`, `CouponCode`, `AppliedDiscount` types | `internal/domain/discount.go` |
| `Discount.Evaluate()` | `internal/domain/discount.go` |
| Discount and coupon store | `internal/store/discounts.go` |
| `CheckoutService.ApplyCouponCode` | `internal/app/checkout.go` |
| `CheckoutService.EvaluateAutomaticDiscounts` | `internal/app/checkout.go` |
| Discount finalization in `PlaceOrder` | `internal/app/orders.go` |
| Admin discount management handlers | `internal/web/discounts.go` |
| Coupon code entry in checkout | `internal/ui/storefront/checkout.templ` (Svelte component) |
| Admin discount list and editor | `internal/ui/admin/discounts.templ` |

---

## What this design defers

**Line-item discounts** — applying a discount to specific products or collections requires the `AppliedDiscount` to carry line-item-level reductions rather than a single `AmountCents`. The `order_discounts` table would need a child `order_discount_lines` table. The `Evaluate` function signature expands to receive line items. This is a contained schema addition.

**Multi-use codes with a shared limit** — add `max_redemptions int` and `redemption_count int` to `coupon_codes`. The optimistic update pattern extends to check `redemption_count < max_redemptions`. No structural changes.

**Bulk code generation** — a River job that generates N unique codes for a discount and inserts them into `coupon_codes`. The table already supports it.

**Stacking** — remove the `UNIQUE (order_id)` constraint on `order_discounts` and change `discount_total` on orders to sum multiple rows. The calculation logic needs a stacking policy (e.g. percentage discounts apply to the already-reduced subtotal). Non-trivial but the schema is the only blocker.

**Customer group targeting** — add a `customer_group_id` foreign key to `discounts`. The `Evaluate` function receives the customer's group memberships and checks eligibility. No changes to the redemption or finalization path.
