# Lean Commerce — Tax

Tax calculation is handled by Stripe Tax for standard customers. Tax-exempt customers (B2B resellers, nonprofits, government entities) are marked exempt by staff after offline verification — no certificate workflow, no customer-facing exemption process.

---

## Scope

**In scope:**
- Tax calculation at checkout via Stripe Tax
- Tax-exempt customers (admin-controlled, all-or-nothing)
- Tax display on invoices and order history
- Audit trail for exemption grant and revocation

**Out of scope:**
- Exemption certificate storage
- Certificate expiry tracking
- Per-state exemption rules
- Customer-facing exemption workflow
- Tax reporting and remittance (handled in Stripe dashboard)

Geographic scope: US only.

---

## The two paths through checkout

Every order takes exactly one tax path, determined before the Stripe PaymentIntent is created:

```
Is the customer tax exempt?
    │
    ├── Yes → disable Stripe Tax on PaymentIntent
    │          tax_total = 0
    │          copy tax_exempt_reason onto order
    │
    └── No  → enable Stripe Tax on PaymentIntent
               Stripe calculates tax based on buyer address + product codes
               tax_total = amount returned by Stripe on payment confirmation
               stripe_tax_id stored for reconciliation
```

The decision is made once and frozen on the order. A subsequent change to the customer's exemption status does not alter past orders.

---

## Schema

### Customer additions

```sql
ALTER TABLE customers
    ADD COLUMN tax_exempt        bool NOT NULL DEFAULT false,
    ADD COLUMN tax_exempt_reason text;
```

`tax_exempt_reason` is freeform text set by the admin at the time of granting exemption. Examples: "Reseller certificate on file", "501(c)(3) nonprofit", "Government entity". Not an enum — the admin records whatever is accurate for the relationship.

### Order additions

```sql
ALTER TABLE orders
    ADD COLUMN tax_total         int  NOT NULL DEFAULT 0,  -- cents
    ADD COLUMN tax_exempt        bool NOT NULL DEFAULT false,
    ADD COLUMN tax_exempt_reason text,
    ADD COLUMN stripe_tax_id     text;
```

| Column | Exempt order | Non-exempt order |
|---|---|---|
| `tax_total` | `0` | Stripe-calculated amount in cents |
| `tax_exempt` | `true` | `false` |
| `tax_exempt_reason` | Copied from customer at order time | `null` |
| `stripe_tax_id` | `null` | Stripe tax calculation ID |

`tax_exempt_reason` is copied from the customer record onto the order at the time of purchase. This makes the order self-contained — if the exemption reason is later updated or revoked, the order still reflects why it was exempt when placed.

---

## Domain types

```go
// domain/customer.go — additions
type Customer struct {
    // ... existing fields ...
    TaxExempt       bool
    TaxExemptReason *string  // nil if not exempt
}

// domain/order.go — additions
type Order struct {
    // ... existing fields ...
    TaxTotal        int      // cents; 0 for exempt orders
    TaxExempt       bool
    TaxExemptReason *string  // copied from customer at order time; nil if not exempt
    StripeTaxID     *string  // nil for exempt orders; Stripe reference for reconciliation
}
```

---

## Stripe Tax integration

Stripe Tax is configured in the Stripe dashboard — merchant address, automatic tax enabled, product tax codes assigned to catalog items. No tax rate management lives in the application codebase.

### PaymentIntent creation

The Go backend creates the PaymentIntent with `automatic_tax` enabled or disabled based on the customer's exemption status:

```go
// app/checkout.go

func (s *CheckoutService) CreatePaymentIntent(
    ctx     context.Context,
    tx      pgx.Tx,
    cart    *domain.Cart,
    customer *domain.Customer,
) (*stripe.PaymentIntent, error) {

    params := &stripe.PaymentIntentParams{
        Amount:   stripe.Int64(int64(cart.Subtotal + cart.ShippingTotal)),
        Currency: stripe.String(cart.Currency),
    }

    if customer.TaxExempt {
        params.AutomaticTaxParams = &stripe.PaymentIntentAutomaticTaxParams{
            Enabled: stripe.Bool(false),
        }
    } else {
        params.AutomaticTaxParams = &stripe.PaymentIntentAutomaticTaxParams{
            Enabled: stripe.Bool(true),
        }
    }

    return paymentintent.New(params)
}
```

### Order finalization

The `payment_intent.succeeded` webhook triggers order finalization. The PaymentIntent returned by Stripe includes the final tax amount when Stripe Tax is enabled. The order is created with that amount frozen:

```go
// app/orders.go — called from the webhook River worker

func (s *OrderService) FinalizeFromPayment(
    ctx      context.Context,
    tx       pgx.Tx,
    cart     *domain.Cart,
    customer *domain.Customer,
    pi       *stripe.PaymentIntent,
) (*domain.Order, error) {

    taxTotal := 0
    var stripeTaxID *string

    if !customer.TaxExempt && pi.AutomaticTax != nil {
        taxTotal = int(pi.AutomaticTax.Amount)
        stripeTaxID = &pi.AutomaticTax.TaxCalculation
    }

    return s.orders.Create(ctx, tx, store.CreateOrderParams{
        CustomerID:      customer.ID,
        Subtotal:        cart.Subtotal,
        ShippingTotal:   cart.ShippingTotal,
        TaxTotal:        taxTotal,
        Total:           cart.Subtotal + cart.ShippingTotal + taxTotal,
        TaxExempt:       customer.TaxExempt,
        TaxExemptReason: customer.TaxExemptReason,
        StripeTaxID:     stripeTaxID,
    })
}
```

The `Total` on the order is always `Subtotal + ShippingTotal + TaxTotal`. For exempt customers this simplifies to `Subtotal + ShippingTotal` since `TaxTotal = 0`.

---

## Tax exemption management

### Granting exemption

Staff navigates to a customer record in the admin and sets `tax_exempt = true` with a reason. This is a direct admin action — no customer request flow, no certificate upload.

The service method:

```go
// app/customers.go

func (s *CustomerService) GrantTaxExemption(
    ctx    context.Context,
    tx     pgx.Tx,
    id     uuid.UUID,
    reason string,
    actor  domain.StaffActor,
) (*domain.Customer, error) {

    updated, err := s.customers.SetTaxExempt(ctx, tx, id, true, reason)
    if err != nil { return nil, err }

    s.audit.Record(ctx, tx, audit.AuditEntry{
        ActorType:    audit.ActorStaff,
        ActorID:      &actor.ID,
        ActorName:    actor.Name,
        Action:       audit.AuditCustomerTaxExemptionGranted,
        ResourceType: "customer",
        ResourceID:   id,
        After:        updated,
        Reason:       reason,
    })

    return updated, nil
}
```

### Revoking exemption

```go
func (s *CustomerService) RevokeTaxExemption(
    ctx   context.Context,
    tx    pgx.Tx,
    id    uuid.UUID,
    reason string,
    actor domain.StaffActor,
) (*domain.Customer, error) {

    updated, err := s.customers.SetTaxExempt(ctx, tx, id, false, "")
    if err != nil { return nil, err }

    s.audit.Record(ctx, tx, audit.AuditEntry{
        ActorType:    audit.ActorStaff,
        ActorID:      &actor.ID,
        ActorName:    actor.Name,
        Action:       audit.AuditCustomerTaxExemptionRevoked,
        ResourceType: "customer",
        ResourceID:   id,
        After:        updated,
        Reason:       reason,
    })

    return updated, nil
}
```

The `reason` parameter on revocation records the staff member's explanation — "certificate expired", "customer relationship ended", etc. This goes into the audit entry `reason` field, not into `tax_exempt_reason` (which is cleared on revocation).

### Audit actions

Add to `platform/audit/actions.go`:

```go
AuditCustomerTaxExemptionGranted = "customer.tax_exemption_granted"
AuditCustomerTaxExemptionRevoked = "customer.tax_exemption_revoked"
```

Both are audited staff actions — `GrantTaxExemption` and `RevokeTaxExemption` each write an audit record inside the same transaction as the customer update.

---

## Invoice display

Every order displays a tax line. The line is never omitted — its presence or absence is not determined by whether tax is zero.

**Exempt customer:**
```
Subtotal         $450.00
Shipping           $12.00
Tax                 $0.00   Tax exempt — Reseller certificate on file
────────────────────────────────────────
Total            $462.00
```

**Standard customer:**
```
Subtotal         $450.00
Shipping           $12.00
Tax                $36.00
────────────────────────────────────────
Total            $498.00
```

The parenthetical text on exempt orders comes from `order.tax_exempt_reason`. If `tax_exempt_reason` is null on an exempt order (edge case: reason was not recorded at time of grant), display "Tax exempt" without further detail.

Non-exempt orders display the tax amount only — no jurisdiction breakdown on the invoice. The full breakdown is available in the Stripe dashboard via `stripe_tax_id` for merchants who need it.

---

## What this design defers

These are deliberate exclusions, all addable later without breaking changes to the current schema:

**Per-state exemption** — the current design is all-or-nothing. Per-state exemption would add a `customer_tax_exemptions` table with `(customer_id, state_code, exempt_reason)` and change the checkout path to look up exemption by the buyer's shipping state. The `tax_exempt` boolean on `customers` would be retired or repurposed as a global override.

**Certificate storage** — add a `tax_exempt_certificate_url` column to `customers` pointing to a file in object storage. The upload workflow is an admin action. No changes to checkout logic.

**Certificate expiry** — add `tax_exempt_expires_at` to `customers`. A scheduled River job checks for upcoming expirations and notifies staff. Expired exemptions are not automatically revoked — staff confirms and revokes manually.

**Customer-facing exemption request** — add a customer-submitted request flow: customer submits certificate, River job notifies staff, staff reviews and grants or denies. The grant path is the same `GrantTaxExemption` service method.
