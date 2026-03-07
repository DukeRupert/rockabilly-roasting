# Lean Commerce — Discounts

Discounts reduce the amount a customer pays. This document covers the domain model, calculation logic, redemption mechanics, and checkout integration.

---

## Scope

**Implemented:**
- Percentage off order subtotal
- Fixed amount off order subtotal
- Single-use coupon codes (unique code, redeemable once)
- Conditions: minimum order value, expiry date, active flag
- Atomic coupon redemption with race-condition safety
- Coupon apply/remove during checkout (B2C only)
- Tax calculated on post-discount subtotal

**Out of scope (deferred):**
- Free shipping discount type
- Automatic discounts (applied without a code, based on rules)
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

These are two distinct things:

- A **discount** is the rule: type, value, conditions, active window. It exists independently of any code.
- A **coupon code** is a redemption token that unlocks a discount. A discount may have zero codes (automatic) or many codes.

### Discount types

| Type | `value` interpretation | Example |
|------|----------------------|---------|
| `percentage` | Integer 0-100 (percent) | `10` = 10% off |
| `fixed_amount` | Cents | `1000` = $10.00 off |

---

## Schema

### `discounts` table

```sql
CREATE TABLE discounts (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                text NOT NULL,
    description         text,
    type                text NOT NULL CHECK (type IN ('percentage', 'fixed_amount')),
    value               int NOT NULL DEFAULT 0,
    minimum_order_cents int,
    starts_at           timestamptz,
    expires_at          timestamptz,
    active              bool NOT NULL DEFAULT true,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
```

### `coupon_codes` table

```sql
CREATE TABLE coupon_codes (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    discount_id         uuid NOT NULL REFERENCES discounts(id),
    code                text NOT NULL,
    customer_id         uuid REFERENCES customers(id),
    redeemed_at         timestamptz,
    redeemed_by         uuid REFERENCES customers(id),
    redeemed_by_order_id uuid REFERENCES orders(id) ON DELETE SET NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_coupon_code UNIQUE (code)
);
```

`redeemed_by_order_id` links the coupon redemption to the specific order, enabling lookup of which order used a given code.

### Cart columns

```sql
-- On carts table
applied_discount_id    uuid REFERENCES discounts(id),
applied_coupon_code_id uuid REFERENCES coupon_codes(id)
```

The cart stores the applied discount/coupon while the customer is shopping. These are cleared when the order is placed.

### Orders table

```sql
-- On orders table
discount_total int NOT NULL DEFAULT 0  -- cents
```

`Total` on an order is always:
```
total = subtotal - discount_total + shipping_total + tax_total
```

---

## Domain types

```go
// domain/discount.go

type DiscountType string
const (
    DiscountTypePercentage  DiscountType = "percentage"
    DiscountTypeFixedAmount DiscountType = "fixed_amount"
)

type Discount struct {
    ID                uuid.UUID
    Name              string
    Description       *string
    Type              DiscountType
    Value             int
    MinimumOrderCents *int
    StartsAt          *time.Time
    ExpiresAt         *time.Time
    Active            bool
    CreatedAt         time.Time
    UpdatedAt         time.Time
}

type CouponCode struct {
    ID                uuid.UUID
    DiscountID        uuid.UUID
    Code              string
    CustomerID        *uuid.UUID
    RedeemedAt        *time.Time
    RedeemedBy        *uuid.UUID
    RedeemedByOrderID *uuid.UUID
    CreatedAt         time.Time
}
```

---

## Calculation logic

Discount calculation is a pure function in `app/checkout.go`:

```go
func calculateDiscount(d *domain.Discount, subtotal int) int {
    switch d.Type {
    case domain.DiscountTypePercentage:
        return subtotal * d.Value / 100
    case domain.DiscountTypeFixedAmount:
        if d.Value > subtotal {
            return subtotal
        }
        return d.Value
    default:
        return 0
    }
}
```

Fixed amount discounts are capped at the subtotal — never reduce below zero.

---

## Checkout flow

### API endpoints

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/api/checkout/coupon` | Validate and apply a coupon code to the cart |
| `DELETE` | `/api/checkout/coupon` | Remove the applied coupon from the cart |
| `POST` | `/api/checkout/payment-intent` | Create Stripe PaymentIntent (includes discount in total) |
| `POST` | `/api/checkout/confirm` | Place the order (atomically redeems the coupon) |

### Apply coupon (`POST /api/checkout/coupon`)

Request:
```json
{ "cart_id": "uuid", "code": "SAVE10" }
```

Response (success):
```json
{
    "valid": true,
    "discount_name": "10% Off",
    "discount_type": "percentage",
    "discount_value": 10
}
```

Response (failure):
```json
{
    "valid": false,
    "error_message": "That code has already been used."
}
```

Validation checks (in order):
1. Coupon code exists
2. Coupon not already redeemed
3. Discount is active
4. Discount hasn't expired

On success, the cart's `applied_discount_id` and `applied_coupon_code_id` are set.

### Payment intent

The payment intent handler reads the cart's applied coupon, calculates the discount, and includes it in the charge amount:

```
charge_amount = subtotal - discount_total + tax_total
```

Tax is calculated on the **post-discount subtotal**. Each line item's taxable amount is proportionally reduced:

```go
ratio := float64(discountedSubtotal) / float64(subtotal)
taxLineItems[i].Subtotal = int(float64(taxLineItems[i].Subtotal) * ratio)
```

The response includes discount details for frontend display:

```json
{
    "client_secret": "pi_...",
    "amount": 3935,
    "subtotal": 3600,
    "discount_total": 360,
    "discount_name": "10% Off",
    "coupon_code": "SAVE10",
    "tax_total": 283,
    "tax_label": "WA Sales Tax"
}
```

### Order confirmation

The confirm handler:
1. Reads the cart's applied coupon
2. Calculates the discount amount
3. Adjusts tax line items for discounted subtotal
4. Passes `CouponCode` to `PlaceOrder`
5. `PlaceOrder` atomically redeems the coupon in the same transaction as order creation

### Checkout display

```
Subtotal          $36.00
SAVE10            -$3.60
WA Sales Tax       $2.83
---------------------
Total             $35.23
```

---

## Atomic coupon redemption

The critical race condition: two concurrent checkouts could both pass the "not yet redeemed" check and both attempt to redeem the same coupon code.

The fix is an atomic conditional update in `PlaceOrder`:

```sql
UPDATE coupon_codes
SET redeemed_at = now(),
    redeemed_by = $2,
    redeemed_by_order_id = $3
WHERE id = $1
  AND redeemed_at IS NULL
RETURNING *;
```

If this returns zero rows (`pgx.ErrNoRows`), the code was redeemed by a concurrent request. The service returns `ErrCouponAlreadyRedeemed` and the transaction rolls back. No distributed lock needed.

Two distinct error types handle the two scenarios:
- `ErrCouponAlreadyUsed` — pre-check: coupon was already redeemed before checkout started
- `ErrCouponAlreadyRedeemed` — race condition: coupon was redeemed by someone else during checkout

The coupon redemption, order creation, discount adjustment, and audit record all happen in the same transaction. If any step fails, everything rolls back.

---

## Service layer

### `CheckoutService` methods

```go
// Validate a coupon code and return its discount for preview.
func (s *CheckoutService) ApplyCoupon(ctx, tx, code string) (*domain.Discount, error)

// Look up coupon codes for cart storage.
func (s *CheckoutService) GetCouponCodeByCode(ctx, tx, code string) (*domain.CouponCode, error)
func (s *CheckoutService) GetCouponCodeByID(ctx, tx, id uuid.UUID) (*domain.CouponCode, error)

// PlaceOrder creates the order and atomically redeems the coupon.
func (s *CheckoutService) PlaceOrder(ctx, tx, PlaceOrderParams, Actor) (*domain.Order, error)
```

### `OrderService` methods

```go
// Update cart's applied discount/coupon (used by apply/remove handlers).
func (s *OrderService) UpdateCartDiscount(ctx, tx, cartID uuid.UUID, discountID, couponCodeID *uuid.UUID) (*domain.Cart, error)
```

### `DiscountService` methods

```go
// Look up a discount by ID (used to resolve cart's applied discount).
func (s *DiscountService) GetDiscount(ctx, tx, id uuid.UUID) (*domain.Discount, error)
```

---

## Package placement

| Concern | Location |
|---|---|
| `Discount`, `CouponCode` types | `internal/domain/discount.go` |
| Discount and coupon store | `internal/store/discounts.go` |
| Cart discount update | `internal/store/orders.go` (`UpdateCartDiscount`) |
| `CheckoutService.ApplyCoupon` | `internal/app/checkout.go` |
| `CheckoutService.PlaceOrder` (with coupon redemption) | `internal/app/checkout.go` |
| `OrderService.UpdateCartDiscount` | `internal/app/orders.go` |
| Coupon apply/remove handlers | `internal/web/checkout.go` |
| Admin discount list | `internal/web/discounts.go` |
| Sentinel errors | `internal/app/errors.go` |

---

## What this design defers

**Free shipping discounts** — add `DiscountTypeFreeShipping` and set `shipping_total = 0` when applied. The schema supports it; calculation logic needs a new case.

**Automatic discounts** — evaluate all active discounts against the cart without a code. Requires a `ListActive` store method and an evaluation loop. The discount types and calculation logic are reusable.

**Line-item discounts** — applying a discount to specific products requires per-line reduction amounts. Would need an `order_discount_lines` child table. The `calculateDiscount` function expands to receive line items.

**Multi-use codes with a shared limit** — add `max_redemptions int` and `redemption_count int` to `coupon_codes`. The atomic update extends to check `redemption_count < max_redemptions`.

**Bulk code generation** — a River job that generates N unique codes for a discount. The table already supports it.

**Stacking** — remove the single-discount-per-order constraint. Calculation logic needs a stacking policy (e.g., percentage discounts apply to the already-reduced subtotal).

**Customer group targeting** — add `customer_group_id` to `discounts` and check membership during validation.
