# Lean Commerce — B2B Wholesale

This document covers the domain model, schema, service layer design, and payment flow for the wholesale B2B features of Lean Commerce. It builds on the existing domain model, customer groups, price lists, and order infrastructure — adding the wholesale-specific concerns that distinguish B2B from B2C.

---

## Scope

### Tier 1 — Launch features

- Wholesale customer accounts (approved, separate from retail)
- Product visibility (public / wholesale / restricted to customer group)
- Price lists (wholesale pricing assigned per customer or group)
- Minimum order quantities (per variant, enforced at cart validation)
- Quick order form (dense table UI, the primary wholesale ordering interface)
- Invoice-after payment (order placed without upfront payment, invoiced by admin)
- Purchase order number (customer's internal PO reference on every order)

### Tier 2 — Post-launch

- Standing orders (scheduled repeat orders, B2B equivalent of subscriptions)
- Volume discounts (quantity-based price tiers per variant)
- Customer groups (shared price list, visibility, and shipping across multiple accounts)
- Partial fulfillment invoicing (multiple invoices per order for split shipments)

### Deferred

- Sales rep ordering
- Preorders
- Proforma invoices
- Accounting integrations (Xero, QuickBooks)
- Multi-currency

---

## Wholesale customer accounts

### Account type distinction

Wholesale customers are a distinct account type from retail customers. They share the `customers` table but are differentiated by an `account_type` column and require admin approval before they can place orders.

```sql
ALTER TABLE customers
    ADD COLUMN account_type   text NOT NULL DEFAULT 'retail'
        CHECK (account_type IN ('retail', 'wholesale')),
    ADD COLUMN wholesale_status text
        CHECK (wholesale_status IN ('pending', 'approved', 'suspended')),
    ADD COLUMN company_name     text,
    ADD COLUMN phone            text,
    ADD COLUMN website          text,
    ADD COLUMN wholesale_notes  text,  -- internal admin notes
    ADD COLUMN approved_at      timestamptz,
    ADD COLUMN approved_by      uuid REFERENCES staff(id);
```

`wholesale_status` is only meaningful when `account_type = 'wholesale'`. A retail customer has `wholesale_status = null`.

**Account states:**

```
pending   → application submitted, awaiting admin review
approved  → can log in to wholesale portal and place orders
suspended → account exists but ordering is blocked (non-payment, account review)
```

### Application flow

Wholesale customers apply via a public signup form — not the retail registration flow. The form collects: company name, contact name, email, phone, website, and a brief message. On submission:

1. A `customer` row is created with `account_type = 'wholesale'`, `wholesale_status = 'pending'`
2. A River job notifies staff via email
3. The applicant receives a confirmation email: "We've received your wholesale application and will be in touch within 2 business days."
4. Admin reviews in the staff panel and approves or declines
5. On approval: `wholesale_status = 'approved'`, `approved_at`, `approved_by` set, welcome email sent with login credentials

Declined applications are not deleted — `wholesale_status` remains `pending` with an internal note. The admin can revisit.

### Authentication

Wholesale customers authenticate through the same session system as retail customers. The session middleware distinguishes them by `account_type` on the customer record. The wholesale portal routes check `customer.account_type == 'wholesale' && customer.wholesale_status == 'approved'` — a pending or suspended wholesale customer gets a specific message, not a generic 401.

```go
// web/middleware.go

func RequireApprovedWholesale(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        customer := auth.CustomerFromContext(r.Context())
        if customer.AccountType != domain.AccountTypeWholesale {
            http.Redirect(w, r, "/", http.StatusSeeOther)
            return
        }
        switch customer.WholesaleStatus {
        case domain.WholesaleStatusPending:
            web.Render(w, r, ui.WholesalePending(customer))
            return
        case domain.WholesaleStatusSuspended:
            web.Render(w, r, ui.WholesaleSuspended(customer))
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

---

## Product visibility

### Schema

```sql
ALTER TABLE products
    ADD COLUMN visibility text NOT NULL DEFAULT 'public'
        CHECK (visibility IN ('public', 'wholesale', 'restricted'));

CREATE TABLE product_group_visibility (
    product_id        uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    customer_group_id uuid NOT NULL REFERENCES customer_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (product_id, customer_group_id)
);
```

### Visibility rules

| Visibility | Unauthenticated | Retail customer | Wholesale (no group) | Wholesale (with matching group) |
|---|---|---|---|---|
| `public` | ✓ visible | ✓ visible | ✓ visible | ✓ visible |
| `wholesale` | ✗ hidden | ✗ hidden | ✓ visible | ✓ visible |
| `restricted` | ✗ hidden | ✗ hidden | ✗ hidden | ✓ visible |

A `restricted` product with no `product_group_visibility` rows is visible to nobody except staff — a valid state for a product being prepared for a not-yet-onboarded client. The admin UI shows a warning badge: "No groups assigned — this product is currently invisible to all customers."

Direct URL access to a hidden product returns 404 — not 403. No information is leaked about whether the product exists.

### Domain types

```go
// domain/catalog.go

type ProductVisibility string
const (
    ProductVisibilityPublic     ProductVisibility = "public"
    ProductVisibilityWholesale  ProductVisibility = "wholesale"
    ProductVisibilityRestricted ProductVisibility = "restricted"
)
```

### Store query pattern

Every product listing query gates on visibility. One function encapsulates the rule and is called from all catalog store methods:

```go
// store/catalog.go

type VisibilityContext struct {
    CustomerID  *uuid.UUID
    IsWholesale bool
    GroupIDs    []uuid.UUID
}

// visibleProductsFilter returns a SQL WHERE fragment and args for visibility gating.
// Embed this in every query that lists or searches products.
func visibleProductsFilter(ctx VisibilityContext, productAlias string) (string, []any) {
    p := productAlias
    if ctx.CustomerID == nil || !ctx.IsWholesale {
        return fmt.Sprintf("%s.visibility = 'public'", p), nil
    }
    if len(ctx.GroupIDs) == 0 {
        return fmt.Sprintf("%s.visibility IN ('public', 'wholesale')", p), nil
    }
    return fmt.Sprintf(`(
        %s.visibility IN ('public', 'wholesale')
        OR (
            %s.visibility = 'restricted'
            AND EXISTS (
                SELECT 1 FROM product_group_visibility pgv
                WHERE pgv.product_id = %s.id
                AND pgv.customer_group_id = ANY($1)
            )
        )
    )`, p, p, p), []any{ctx.GroupIDs}
}
```

The `VisibilityContext` is built once per request in the handler from the authenticated customer's session and group memberships, then passed through to all store calls in that request.

---

## Price lists

The `price_sets` and `prices` tables from the domain model already support per-variant pricing. The wholesale extension adds the concept of a named price list that is assigned to a customer or customer group.

### Schema additions

```sql
CREATE TABLE wholesale_price_lists (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name            text NOT NULL,          -- e.g. "Café Partner Pricing", "Distributor Tier 1"
    description     text,
    discount_percent numeric(5,2),          -- optional: apply % off all standard prices
    currency        text NOT NULL DEFAULT 'usd',
    active          bool NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- Assign a price list to a customer (overrides group-level assignment)
ALTER TABLE customers
    ADD COLUMN wholesale_price_list_id uuid REFERENCES wholesale_price_lists(id);

-- Assign a price list to a customer group
ALTER TABLE customer_groups
    ADD COLUMN wholesale_price_list_id uuid REFERENCES wholesale_price_lists(id);

-- Per-variant price overrides within a price list
-- When present, this price is used instead of discount_percent calculation
CREATE TABLE wholesale_price_list_prices (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    price_list_id   uuid NOT NULL REFERENCES wholesale_price_lists(id) ON DELETE CASCADE,
    variant_id      uuid NOT NULL REFERENCES variants(id) ON DELETE CASCADE,
    unit_price      int NOT NULL,           -- cents; explicit price for this variant
    CONSTRAINT uq_price_list_variant UNIQUE (price_list_id, variant_id)
);
```

### Price resolution order

When calculating the price a wholesale customer pays for a variant:

```
1. Customer has a personal price list?
      → Check for per-variant override in wholesale_price_list_prices
      → If found: use that price
      → If not found and discount_percent set: standard_price * (1 - discount_percent/100)
      → If not found and no discount_percent: standard price

2. Customer's group has a price list?
      → Same resolution as above

3. No price list assigned:
      → Standard variant price
```

This resolution lives in a pure domain function:

```go
// domain/pricing.go

type WholesalePriceList struct {
    ID              uuid.UUID
    DiscountPercent *decimal.Decimal
    Overrides       map[uuid.UUID]int // variant_id → unit_price cents
}

func (pl *WholesalePriceList) PriceFor(variantID uuid.UUID, standardPrice int) int {
    if pl == nil {
        return standardPrice
    }
    if override, ok := pl.Overrides[variantID]; ok {
        return override
    }
    if pl.DiscountPercent != nil {
        discount := int(decimal.NewFromInt(int64(standardPrice)).
            Mul(*pl.DiscountPercent).
            Div(decimal.NewFromInt(100)).
            IntPart())
        return standardPrice - discount
    }
    return standardPrice
}
```

---

## Minimum order quantities

### Schema

```sql
ALTER TABLE variants
    ADD COLUMN wholesale_min_qty  int,      -- null = no minimum
    ADD COLUMN wholesale_multiple int;      -- null = no multiple constraint
                                            -- e.g. must order in multiples of 5
```

`wholesale_min_qty` is the minimum units per variant per order.
`wholesale_multiple` constrains quantities to multiples — a roaster selling by the pound who wants orders in 5-lb increments sets `wholesale_multiple = 5`.

### Validation

Cart validation for wholesale orders runs after every quantity change and again at checkout submission:

```go
// domain/wholesale.go

type MOQViolation struct {
    VariantID   uuid.UUID
    VariantName string
    Ordered     int
    Minimum     int
    Multiple    int // 0 = no multiple constraint
}

func ValidateWholesaleCart(items []CartItem, variants []domain.Variant) []MOQViolation {
    var violations []MOQViolation
    for _, item := range items {
        v := variantByID(variants, item.VariantID)
        if v.WholesaleMinQty != nil && item.Quantity < *v.WholesaleMinQty {
            violations = append(violations, MOQViolation{
                VariantID:   v.ID,
                VariantName: v.Name,
                Ordered:     item.Quantity,
                Minimum:     *v.WholesaleMinQty,
            })
            continue
        }
        if v.WholesaleMultiple != nil && item.Quantity % *v.WholesaleMultiple != 0 {
            violations = append(violations, MOQViolation{
                VariantID:   v.ID,
                VariantName: v.Name,
                Ordered:     item.Quantity,
                Multiple:    *v.WholesaleMultiple,
            })
        }
    }
    return violations
}
```

Violations are shown inline in the quick order form — red border on the offending quantity input, helper text showing the requirement. Checkout submission is blocked until all violations are resolved.

Error messages follow the voice guidelines:

- Minimum violation: "Minimum order for Ethiopia Yirgacheffe is 5 lbs."
- Multiple violation: "Ethiopia Yirgacheffe must be ordered in multiples of 5 lbs."

---

## Quick order form

The quick order form is the primary ordering interface for wholesale customers. It is a dense HTML table — not a product grid — with quantity inputs inline.

### Data shape

The handler for the quick order page loads all visible variants for the customer in a single query, grouped by product, with wholesale pricing applied:

```go
// app/wholesale.go

type QuickOrderRow struct {
    ProductID   uuid.UUID
    ProductName string
    VariantID   uuid.UUID
    VariantName string  // e.g. "12 oz", "3.0 lbs"
    SKU         string
    UnitPrice   int     // wholesale price in cents
    MinQty      *int
    Multiple    *int
    StockLevel  int
    InStock     bool
}

func (s *WholesaleService) QuickOrderRows(
    ctx  context.Context,
    tx   pgx.Tx,
    visCtx store.VisibilityContext,
    priceList *domain.WholesalePriceList,
) ([]QuickOrderRow, error) {
    variants, err := s.catalog.ListVisibleVariants(ctx, tx, visCtx)
    if err != nil { return nil, err }

    rows := make([]QuickOrderRow, len(variants))
    for i, v := range variants {
        rows[i] = QuickOrderRow{
            ProductID:   v.ProductID,
            ProductName: v.ProductName,
            VariantID:   v.ID,
            VariantName: v.Name,
            SKU:         v.SKU,
            UnitPrice:   priceList.PriceFor(v.ID, v.StandardPrice),
            MinQty:      v.WholesaleMinQty,
            Multiple:    v.WholesaleMultiple,
            StockLevel:  v.StockLevel,
            InStock:     v.StockLevel > 0 || v.AllowBackorder,
        }
    }
    return rows, nil
}
```

### UI behavior

The quick order table renders server-side via templ. Quantity inputs are Alpine-driven for instant subtotal calculation — no round-trip to the server until "Add to cart" is clicked.

```
Product                | Size    | Price    | Min | Qty        | Subtotal
───────────────────────────────────────────────────────────────────────────
Ethiopia Yirgacheffe   | 12 oz   | $14.00   | —   | [  0     ] | —
                       | 3.0 lbs | $42.00   | 5   | [  5     ] | $210.00
                       | 5.0 lbs | $65.00   | 5   | [  0     ] | —
Cloud 9 Espresso       | 12 oz   | $14.00   | —   | [  0     ] | —
                       | 3.0 lbs | $40.50   | 5   | [ 10     ] | $405.00
───────────────────────────────────────────────────────────────────────────
                                                    Order total: $615.00
                                               [Add to cart →]
```

"Add to cart" sends all non-zero quantities to `POST /wholesale/cart/bulk-add`, which upserts cart items and runs MOQ validation server-side. On validation failure, the form re-renders with inline errors (htmx 422 swap). On success, the customer is redirected to cart review.

---

## Purchase order number

```sql
ALTER TABLE orders
    ADD COLUMN customer_po_number text,     -- customer's internal PO reference
    ADD COLUMN internal_note      text;     -- admin internal note, not visible to customer
```

`customer_po_number` is collected during wholesale checkout. It is:

- Displayed on the order confirmation page
- Included on invoices
- Searchable in the admin order list
- Never editable by the customer after order placement (editable by admin only)

The wholesale checkout is a simplified two-step flow (no Stripe at order time):

1. **Review** — line items with inline quantity editing and remove buttons (htmx), subtotals, PO number field, order notes. Customers can adjust quantities or remove items directly on the review page without navigating back to the quick order form via `POST /wholesale/cart/update` and `POST /wholesale/cart/remove`.
2. **Confirm** — order is placed with `payment_status = 'pending_invoice'`

No payment is collected at this step. The order enters the fulfillment queue immediately.

Wholesale uses a dedicated `wholesale_cart_id` cookie, separate from the retail `cart_id` cookie, to prevent cart collision when a customer visits both the retail catalog and the wholesale portal.

---

## Invoice-after payment flow

This is the most significant divergence from the B2C payment model.

### New payment status values

```sql
-- Extend the payment_status enum
ALTER TYPE payment_status ADD VALUE 'pending_invoice' AFTER 'awaiting';
ALTER TYPE payment_status ADD VALUE 'invoiced'        AFTER 'pending_invoice';
ALTER TYPE payment_status ADD VALUE 'partially_paid'  AFTER 'invoiced';
-- 'captured' is reused for "paid in full"
-- 'overdue' is new
ALTER TYPE payment_status ADD VALUE 'overdue'         AFTER 'partially_paid';
```

### Invoice schema

```sql
CREATE TYPE invoice_status AS ENUM (
    'draft',
    'sent',
    'partially_paid',
    'paid',
    'void'
);

CREATE TABLE invoices (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id        uuid NOT NULL REFERENCES orders(id),
    number          text NOT NULL UNIQUE,   -- human-readable: INV-1042
    status          invoice_status NOT NULL DEFAULT 'draft',
    subtotal        int NOT NULL,           -- cents
    shipping        int NOT NULL DEFAULT 0,
    tax_total       int NOT NULL DEFAULT 0,
    total           int NOT NULL,
    amount_paid     int NOT NULL DEFAULT 0,
    amount_due      int GENERATED ALWAYS AS (total - amount_paid) STORED,
    due_date        date,
    notes           text,                   -- visible to customer on invoice
    internal_note   text,                   -- staff only
    sent_at         timestamptz,
    paid_at         timestamptz,
    voided_at       timestamptz,
    created_by      uuid REFERENCES staff(id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_invoices_order ON invoices (order_id);

CREATE TABLE invoice_lines (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id  uuid NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    variant_id  uuid REFERENCES variants(id),
    description text NOT NULL,             -- denormalized product name + variant
    quantity    int NOT NULL,
    unit_price  int NOT NULL,              -- cents
    total       int NOT NULL               -- cents
);

CREATE TABLE invoice_payments (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id      uuid NOT NULL REFERENCES invoices(id),
    amount          int NOT NULL,          -- cents
    method          text NOT NULL,         -- 'stripe', 'ach', 'check', 'cash', 'other'
    reference       text,                  -- check number, ACH trace, Stripe charge ID
    note            text,
    recorded_by     uuid REFERENCES staff(id),
    paid_at         timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_invoice_payments_invoice ON invoice_payments (invoice_id);
```

### Invoice workflow

```
Order placed (payment_status = 'pending_invoice')
    │
    ├─▶ Admin creates invoice from order
    │       → invoice_lines copied from order line items
    │       → status = 'draft'
    │
    ├─▶ Admin sends invoice
    │       → status = 'sent', sent_at = now()
    │       → email sent to customer with payment link (Stripe) or bank details
    │       → order.payment_status = 'invoiced'
    │
    ├─▶ Customer pays via Stripe link
    │       → Stripe webhook → invoice_payments row created automatically
    │       → invoice.amount_paid updated
    │       → if amount_paid >= total: invoice.status = 'paid', order.payment_status = 'captured'
    │       → if amount_paid < total: invoice.status = 'partially_paid',
    │                                 order.payment_status = 'partially_paid'
    │
    └─▶ Admin records manual payment (ACH, check)
            → invoice_payments row created by staff
            → same status update logic as above
```

### Service method — create invoice

```go
// app/invoices.go

func (s *InvoiceService) CreateFromOrder(
    ctx     context.Context,
    tx      pgx.Tx,
    orderID uuid.UUID,
    dueDate *time.Time,
    notes   string,
    actor   domain.StaffActor,
) (*domain.Invoice, error) {

    order, err := s.orders.GetByID(ctx, tx, orderID)
    if err != nil { return nil, err }

    if order.PaymentStatus != domain.PaymentStatusPendingInvoice {
        return nil, ErrOrderNotInvoiceable
    }

    number, err := s.invoices.NextNumber(ctx, tx)
    if err != nil { return nil, err }

    invoice, err := s.invoices.Create(ctx, tx, store.CreateInvoiceParams{
        OrderID:   orderID,
        Number:    number,
        Subtotal:  order.Subtotal,
        Shipping:  order.ShippingTotal,
        TaxTotal:  order.TaxTotal,
        Total:     order.Total,
        DueDate:   dueDate,
        Notes:     notes,
        CreatedBy: actor.ID,
    })
    if err != nil { return nil, err }

    // Copy order line items to invoice lines
    for _, line := range order.LineItems {
        err = s.invoices.CreateLine(ctx, tx, store.CreateInvoiceLineParams{
            InvoiceID:   invoice.ID,
            VariantID:   &line.VariantID,
            Description: line.ProductName + " — " + line.VariantName,
            Quantity:    line.Quantity,
            UnitPrice:   line.UnitPrice,
            Total:       line.Total,
        })
        if err != nil { return nil, err }
    }

    s.audit.Record(ctx, tx, audit.AuditEntry{
        ActorType:    audit.ActorStaff,
        ActorID:      &actor.ID,
        ActorName:    actor.Name,
        Action:       audit.AuditInvoiceCreated,
        ResourceType: "invoice",
        ResourceID:   invoice.ID,
        After:        invoice,
        Metadata:     map[string]any{"order_id": orderID},
    })

    return invoice, nil
}
```

### Service method — record payment

```go
// app/invoices.go

func (s *InvoiceService) RecordPayment(
    ctx       context.Context,
    tx        pgx.Tx,
    invoiceID uuid.UUID,
    amount    int,
    method    string,
    reference string,
    note      string,
    actor     domain.StaffActor,
) (*domain.Invoice, error) {

    invoice, err := s.invoices.GetByID(ctx, tx, invoiceID)
    if err != nil { return nil, err }

    if invoice.Status == domain.InvoiceStatusPaid ||
       invoice.Status == domain.InvoiceStatusVoid {
        return nil, ErrInvoiceNotPayable
    }

    _, err = s.invoices.CreatePayment(ctx, tx, store.CreatePaymentParams{
        InvoiceID:  invoiceID,
        Amount:     amount,
        Method:     method,
        Reference:  reference,
        Note:       note,
        RecordedBy: actor.ID,
    })
    if err != nil { return nil, err }

    // Recalculate amount paid and update status
    updated, err := s.invoices.RecalculatePaymentStatus(ctx, tx, invoiceID)
    if err != nil { return nil, err }

    // Sync order payment status
    orderStatus := domain.PaymentStatusPartiallyPaid
    if updated.Status == domain.InvoiceStatusPaid {
        orderStatus = domain.PaymentStatusCaptured
    }
    _, err = s.orders.UpdatePaymentStatus(ctx, tx, invoice.OrderID, orderStatus)
    if err != nil { return nil, err }

    s.audit.Record(ctx, tx, audit.AuditEntry{
        ActorType: audit.ActorStaff,
        ActorID:   &actor.ID,
        ActorName: actor.Name,
        Action:    audit.AuditInvoicePaymentRecorded,
        ResourceType: "invoice",
        ResourceID:   invoiceID,
        After:        updated,
        Metadata: map[string]any{
            "amount": amount,
            "method": method,
        },
    })

    return updated, nil
}
```

---

## Standing orders (Tier 2)

Standing orders are the B2B equivalent of subscriptions — a recurring order generated automatically on a schedule. A café that orders 20 lbs of Ethiopia every two weeks sets it once; the system generates the order, the admin invoices and ships.

### Schema

```sql
CREATE TYPE standing_order_frequency AS ENUM (
    'weekly', 'biweekly', 'monthly'
);

CREATE TABLE standing_orders (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id     uuid NOT NULL REFERENCES customers(id),
    name            text,                   -- customer's label e.g. "Bi-weekly house blend"
    frequency       standing_order_frequency NOT NULL,
    next_order_date date NOT NULL,
    active          bool NOT NULL DEFAULT true,
    shipping_address_id uuid REFERENCES addresses(id),
    customer_po_prefix  text,              -- auto-append sequence to PO: "RRC-2024-"
    notes           text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE standing_order_items (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    standing_order_id uuid NOT NULL REFERENCES standing_orders(id) ON DELETE CASCADE,
    variant_id        uuid NOT NULL REFERENCES variants(id),
    quantity          int NOT NULL CHECK (quantity > 0),
    CONSTRAINT uq_standing_order_variant UNIQUE (standing_order_id, variant_id)
);
```

### Generation

A scheduled River job runs daily and generates orders for standing orders whose `next_order_date` is today:

```go
// jobs/standing_orders.go

// GenerateStandingOrdersArgs is the daily scheduled job that creates
// wholesale orders from active standing orders due today.
type GenerateStandingOrdersArgs struct{}

func (GenerateStandingOrdersArgs) Kind() string { return "generate_standing_orders" }
```

The job calls `WholesaleService.GenerateStandingOrder` for each due standing order, which creates an order with `payment_status = 'pending_invoice'`, copies the standing order items as line items, and advances `next_order_date` by the frequency interval.

Generated orders include `standing_order_id` in their metadata — the admin can see which standing order produced each order.

---

## Volume discounts (Tier 2)

Volume discounts reduce the unit price of a variant based on quantity ordered. They apply on top of the wholesale price list — the discounted unit price replaces the price list price for that variant when the threshold is met.

### Schema

```sql
CREATE TABLE volume_discount_tiers (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    variant_id      uuid NOT NULL REFERENCES variants(id) ON DELETE CASCADE,
    min_quantity    int NOT NULL,
    discount_percent numeric(5,2) NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_volume_tier UNIQUE (variant_id, min_quantity)
);
```

### Resolution

```go
// domain/pricing.go

// VolumeDiscountedPrice returns the unit price after applying the highest
// applicable volume discount tier for the given quantity.
// basePrice is already wholesale price-list adjusted.
func VolumeDiscountedPrice(basePrice int, quantity int, tiers []domain.VolumeDiscountTier) int {
    best := 0 // highest applicable discount percent * 100
    for _, tier := range tiers {
        if quantity >= tier.MinQuantity && tier.DiscountPercent > best {
            best = tier.DiscountPercent
        }
    }
    if best == 0 {
        return basePrice
    }
    discount := int(decimal.NewFromInt(int64(basePrice)).
        Mul(decimal.NewFromInt(int64(best))).
        Div(decimal.NewFromInt(10000)).
        IntPart())
    return basePrice - discount
}
```

Volume discount tiers are loaded alongside variant data when building quick order rows — the price shown in the table already reflects the volume discount when a qualifying quantity is entered (Alpine re-calculates client-side; server validates on submission).

---

## Audit actions

Add to `platform/audit/actions.go`:

```go
// Wholesale customer management
AuditWholesaleApplicationApproved = "wholesale.application_approved"
AuditWholesaleApplicationDeclined = "wholesale.application_declined"
AuditWholesaleAccountSuspended    = "wholesale.account_suspended"

// Product visibility
AuditProductVisibilityUpdated     = "product.visibility_updated"

// Invoices
AuditInvoiceCreated               = "invoice.created"
AuditInvoiceSent                  = "invoice.sent"
AuditInvoiceVoided                = "invoice.voided"
AuditInvoicePaymentRecorded       = "invoice.payment_recorded"

// Standing orders
AuditStandingOrderCreated         = "standing_order.created"
AuditStandingOrderUpdated         = "standing_order.updated"
AuditStandingOrderDeactivated     = "standing_order.deactivated"
```

---

## Migrations

```
015_wholesale_customers.sql   ← account_type, wholesale_status, company_name, phone,
                                 website, wholesale_notes, approved_at, approved_by,
                                 wholesale_price_list_id on customers
016_product_visibility.sql    ← visibility on products, product_group_visibility table
017_wholesale_pricing.sql     ← wholesale_price_lists, wholesale_price_list_prices,
                                 wholesale_price_list_id on customer_groups
018_wholesale_variants.sql    ← wholesale_min_qty, wholesale_multiple on variants,
                                 volume_discount_tiers table
019_orders_wholesale.sql      ← customer_po_number, internal_note on orders,
                                 pending_invoice + invoiced + partially_paid + overdue
                                 payment status enum values
020_invoices.sql              ← invoices, invoice_lines, invoice_payments tables
021_standing_orders.sql       ← standing_orders, standing_order_items tables
```

---

## Package placement

| Concern | Location |
|---|---|
| `ProductVisibility`, `WholesalePriceList`, `MOQViolation`, `VolumeDiscountTier` types | `internal/domain/wholesale.go` |
| `WholesalePriceList.PriceFor()`, `ValidateWholesaleCart()`, `VolumeDiscountedPrice()` | `internal/domain/wholesale.go` |
| `Invoice`, `InvoiceLine`, `InvoicePayment`, `InvoiceStatus` types | `internal/domain/invoice.go` |
| Visibility, pricing, variant store methods | `internal/store/catalog.go` |
| Invoice store methods | `internal/store/invoices.go` |
| Standing order store methods | `internal/store/standing_orders.go` |
| `WholesaleService.QuickOrderRows`, cart validation | `internal/app/wholesale.go` |
| `InvoiceService.CreateFromOrder`, `RecordPayment` | `internal/app/invoices.go` |
| `WholesaleService.GenerateStandingOrder` | `internal/app/wholesale.go` |
| `GenerateStandingOrdersArgs` worker | `internal/jobs/standing_orders.go` |
| `RequireApprovedWholesale` middleware | `internal/web/middleware.go` |
| Wholesale portal handlers | `internal/web/wholesale.go` |
| Invoice admin handlers | `internal/web/invoices.go` |
| Quick order form | `internal/ui/wholesale/quick_order.templ` |
| Invoice admin views | `internal/ui/admin/invoices.templ` |
| Wholesale customer management | `internal/ui/admin/wholesale.templ` |

---

## What this document does not cover

- **Email templates** for invoice delivery, application confirmation, approval notification — these are River job outputs, content TBD
- **Stripe payment link generation** for invoice payment — Stripe supports payment links tied to a PaymentIntent; the invoice sends this link to the customer
- **Overdue invoice detection** — a scheduled River job that checks `due_date < today` and `status != 'paid'` and updates `order.payment_status = 'overdue'`; straightforward addition to the standing orders scheduler
- **Sales rep ordering** — deferred; would add a `placed_by_staff_id` column to orders and a rep-scoped customer visibility join
- **Accounting integrations** — the invoice and payment schema is designed to export cleanly to Xero or QuickBooks; the integration is a River job pushing data to their APIs
