# Lean Commerce — Tax

Tax calculation supports three modes configured at the store level: flat rate, Stripe Tax, or no tax. The mode is set once in `store_settings` and applies to all B2C orders. B2B (wholesale) orders always skip tax regardless of the store's tax mode.

---

## Current status (Rockabilly Roasting)

**Configured: WA Sales Tax flat-rate 8.8%, ship-to-WA only. Effectively dormant until a taxable SKU is added.**

- Migration `037_enable_wa_sales_tax.sql` sets `store_settings` to `flat_rate` / `0.0880` / `"WA Sales Tax"` and marks every existing product `tax_exempt = true`. Today's catalog is bagged coffee, which falls under WA's food-for-home-consumption exemption — so B2C WA customers still see $0 tax on every current line item.
- The flat-rate calculator is jurisdiction-gated (`Jurisdiction: "WA"`, hardcoded in `app/checkout.go`): buyers shipping outside WA are never taxed, regardless of product flags.
- B2B (wholesale) remains exempt unconditionally.

**Migration is not auto-applied on server boot.** Only River's internal migrations run at startup (see `cmd/server/main.go`). Schema migrations require `mage db:migrate` (or `goose up`) against the target database. Until that's run, `store_settings.tax_mode` stays at its previous value (likely `'none'`) and no tax is computed.

**To light it up for real** (when merch or equipment lands):
1. `mage db:migrate` in prod if not already applied.
2. For each taxable product: `UPDATE products SET tax_exempt = false WHERE id = ...;` — no admin form field yet.
3. WA buyers will then see 8.8% on those line items at checkout; out-of-state buyers still won't.

---

## Tax modes

| Mode | Description | Use case |
|------|-------------|----------|
| `flat_rate` | Fixed percentage applied to taxable line items (optional jurisdiction gate by ship-to state) | Single-jurisdiction merchants (e.g., WA-only) |
| `stripe_tax` | Stripe calculates based on buyer address + product codes | Multi-state / nexus-tracking merchants (not yet implemented — falls back to `none`) |
| `none` | No tax calculated | B2B-only merchants, tax-exempt businesses |

**Rockabilly Roasting:** `flat_rate` at 8.8% (WA Sales Tax), gated to ship-to-WA, for B2C; no tax on B2B.

---

## Schema

### Store-level configuration

```sql
CREATE TABLE store_settings (
    id         bool PRIMARY KEY DEFAULT true CHECK (id = true),  -- singleton
    tax_mode   text NOT NULL DEFAULT 'none'
                   CHECK (tax_mode IN ('stripe_tax', 'flat_rate', 'none')),
    tax_rate   numeric(6,4),    -- e.g. 0.0875 for 8.75%; null unless flat_rate
    tax_label  text,            -- e.g. "WA Sales Tax"; shown at checkout
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
```

`tax_rate` is stored as a decimal fraction (0.0875, not 8.75). Arithmetic is cleaner and unambiguous.

### Product-level tax exemption

```sql
ALTER TABLE products ADD COLUMN tax_exempt bool NOT NULL DEFAULT false;
```

Some products are non-taxable (e.g., food items for home consumption in WA). The `tax_exempt` flag on products is checked during tax calculation — exempt products contribute $0 tax regardless of the rate.

### Customer additions (pre-existing)

```sql
-- Already on customers table
tax_exempt        bool NOT NULL DEFAULT false
tax_exempt_reason text
```

### Order additions (pre-existing)

```sql
-- Already on orders table
tax_total         int  NOT NULL DEFAULT 0   -- cents
tax_exempt        bool NOT NULL DEFAULT false
tax_exempt_reason text
stripe_tax_id     text
```

---

## Domain types

```go
// domain/tax.go

type TaxMode string
const (
    TaxModeStripeTax TaxMode = "stripe_tax"
    TaxModeFlatRate  TaxMode = "flat_rate"
    TaxModeNone      TaxMode = "none"
)

type TaxConfig struct {
    Mode  TaxMode
    Rate  float64  // decimal fraction, e.g. 0.0875
    Label string   // e.g. "WA Sales Tax"
}

type TaxLineItem struct {
    LineIndex int
    Subtotal  int   // cents
    TaxExempt bool  // from product.tax_exempt
}

type TaxResult struct {
    TaxTotal  int
    Label     string
    Breakdown []TaxLineBreakdown
}
```

`CalculateFlatRateTax` is a pure function in the domain package — no DB, no external calls. Takes line items, rate, customer exemption status, and label. Returns a deterministic `TaxResult`. Fully unit tested.

---

## TaxCalculator interface

```go
// platform/tax/tax.go

type TaxCalculator interface {
    Calculate(ctx context.Context, order TaxOrder) (domain.TaxResult, error)
}
```

Three implementations:
- `FlatRateCalculator` — delegates to `domain.CalculateFlatRateTax`
- `NoneCalculator` — returns zero tax
- `StripeTaxCalculator` — stub (returns `NoneCalculator` behavior until implemented)

### Calculator selection

```go
// app/checkout.go

func taxCalculatorForConfig(cfg *domain.TaxConfig, isWholesale bool) tax.TaxCalculator {
    if isWholesale {
        return &tax.NoneCalculator{}
    }
    switch cfg.Mode {
    case domain.TaxModeFlatRate:
        return &tax.FlatRateCalculator{Rate: cfg.Rate, Label: cfg.Label}
    case domain.TaxModeStripeTax:
        return &tax.NoneCalculator{} // stub
    default:
        return &tax.NoneCalculator{}
    }
}
```

**Key rule:** wholesale always gets `NoneCalculator` regardless of store config.

---

## Checkout flow

Tax is calculated at two points:

1. **Payment intent creation** — tax total is included in the Stripe charge amount and returned to the frontend for display
2. **Order confirmation** — tax is recalculated and stored on the order record

Tax is calculated after discounts — the taxable amount is the discounted subtotal per line item, not the original price.

### Payment intent response

```json
{
    "client_secret": "pi_..._secret_...",
    "amount": 4459,
    "currency": "usd",
    "subtotal": 3600,
    "discount_total": 360,
    "discount_name": "10% Off",
    "coupon_code": "SAVE10",
    "tax_total": 283,
    "tax_label": "WA Sales Tax"
}
```

The frontend displays the tax line only when `tax_total > 0`. Discount fields are included when a coupon is applied to the cart (see `lean-commerce-discounts.md` for the coupon flow).

### Tax display at checkout

```
Subtotal          $36.00
Discount          -$3.60    (if applicable)
Shipping           $8.95
WA Sales Tax       $3.24    (only when tax_total > 0)
─────────────────────────
Total             $44.59
```

B2B checkout shows no tax line — wholesale buyers expect this.

---

## Tax exemption management

### Customer-level exemption

Staff navigates to a customer record in the admin and sets `tax_exempt = true` with a reason. This is a direct admin action — no customer request flow, no certificate upload. When a tax-exempt customer checks out, the tax calculator returns $0 regardless of line items.

### Product-level exemption

Staff sets `tax_exempt = true` on individual products via the admin product form. Tax-exempt products contribute $0 tax in any cart, even for non-exempt customers.

### Exemption priority

1. **Customer exempt** → all items $0 tax (short-circuits entirely)
2. **Product exempt** → that line item $0 tax, other items taxed normally
3. **B2B (wholesale)** → all items $0 tax (enforced at calculator selection)

---

## Invoice display

The `tax_label` is stored on the order as a snapshot — it's what appears on receipts and invoices. If the store later changes their tax label, historical orders still show the correct label at time of purchase.

**Tax-exempt customer:**
```
Subtotal         $450.00
Shipping           $12.00
Tax                 $0.00   Tax exempt — Reseller certificate on file
────────────────────────────────────────
Total            $462.00
```

**Standard customer (flat rate):**
```
Subtotal         $450.00
Shipping           $12.00
WA Sales Tax       $39.38
────────────────────────────────────────
Total            $501.38
```

---

## What this design defers

- **Stripe Tax implementation** — the `StripeTaxCalculator` is a stub. When a merchant needs address-based calculation, implement `Calculate()` to call the Stripe Tax API. Nothing else in checkout changes.
- **Per-line tax display** — `TaxResult.Breakdown` captures per-line tax amounts but the UI currently shows only the total. Line-level display can be added without backend changes.
- **Tax on shipping** — currently only line item subtotals are taxed. If shipping needs to be taxed, add a shipping line item to the tax calculation input.
- **Certificate storage / expiry** — addable without schema changes to tax calculation.
