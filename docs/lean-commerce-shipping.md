# Lean Commerce — Shipping

Shipping is split into two independent concerns with no coupling between them:

1. **Rate calculation at checkout** — internal rule-based logic. No external service. Flat rate with an optional free shipping threshold, configured per merchant.
2. **Label generation in the admin** — optional staff action after an order is placed. Delegated to an external service (Shippo or EasyPost) via a provider interface. Stores the label URL and tracking number on the shipment record.

These two concerns do not share code or schema beyond the `shipments` table that connects them to an order.

---

## Scope

**In scope:**
- Flat rate shipping calculation at checkout
- Free shipping threshold (order subtotal ≥ threshold → shipping free)
- Shipping configuration per merchant (rate, threshold, currency)
- Label generation via external provider (Shippo or EasyPost)
- Storing label URL and tracking number on the shipment record
- Provider abstraction — swapping providers requires no checkout or order code changes

**Out of scope:**
- Live carrier rate quotes at checkout
- Shipping zones or destination-based rates
- Tracking update polling
- Automated tracking emails (can be added as a River job later)
- Returns / RMA

---

## Part 1 — Rate calculation at checkout

### Shipping configuration

Shipping rules are merchant-level configuration, not hardcoded. A merchant can set their flat rate and free shipping threshold in the admin. These values are read at checkout to calculate the shipping total.

```sql
CREATE TABLE shipping_config (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    flat_rate_cents         int  NOT NULL DEFAULT 0,
    free_shipping_threshold int,          -- cents; null = no free shipping threshold
    currency                text NOT NULL DEFAULT 'usd',
    updated_at              timestamptz NOT NULL DEFAULT now(),
    updated_by              uuid         -- staff.id of last editor
);
```

A single row. This is not a multi-tenant table — Lean Commerce is a single-merchant platform. If multi-tenant support is added later, `merchant_id` becomes the partition key.

`free_shipping_threshold` is nullable — null means free shipping is never applied regardless of order size. A merchant who wants free shipping always sets `flat_rate_cents = 0` and leaves the threshold null.

### Calculation logic

```go
// domain/shipping.go

type ShippingConfig struct {
    FlatRateCents         int
    FreeShippingThreshold *int  // nil = no threshold
    Currency              string
}

// Calculate returns the shipping total in cents for a given order subtotal.
// Free shipping applies when threshold is set and subtotal meets or exceeds it.
func (c ShippingConfig) Calculate(subtotalCents int) int {
    if c.FreeShippingThreshold != nil && subtotalCents >= *c.FreeShippingThreshold {
        return 0
    }
    return c.FlatRateCents
}
```

This is the only place shipping rate logic lives. It is a pure function on a domain type — no database access, no HTTP calls, testable without any infrastructure.

The checkout service calls it:

```go
// app/checkout.go

func (s *CheckoutService) CalculateShipping(
    ctx  context.Context,
    cart *domain.Cart,
) (int, error) {
    cfg, err := s.shippingConfig.Get(ctx)
    if err != nil { return 0, err }
    return cfg.Calculate(cart.Subtotal), nil
}
```

### Checkout display

The storefront shows the shipping total as part of the cart summary. If the order qualifies for free shipping, display "Free" rather than "$0.00". If a free shipping threshold exists and the cart hasn't reached it, display how much more the customer needs to spend:

```
Subtotal    $74.00
Shipping    $8.00   (Spend $26.00 more for free shipping)
Tax         $5.92
────────────────
Total       $87.92
```

The threshold messaging is computed in the handler and passed to the templ template as a plain string — the template does not compute it.

### Order record

The shipping total calculated at checkout is frozen on the order, same as tax:

```sql
-- Already in orders table from domain model
-- Confirming the columns are present:
-- shipping_total  int  NOT NULL DEFAULT 0
```

No additional columns needed on `orders` for Part 1.

---

## Part 2 — Label generation

### Provider abstraction

Label generation is behind an interface. The application calls the interface; the concrete implementation (Shippo or EasyPost) is injected at startup. Swapping providers is a one-line change in `cmd/server/main.go`.

```go
// platform/shipping/provider.go

type LabelRequest struct {
    // Sender (merchant's ship-from address)
    FromName    string
    FromStreet1 string
    FromCity    string
    FromState   string
    FromZip     string
    FromCountry string

    // Recipient (customer's ship-to address)
    ToName      string
    ToStreet1   string
    ToCity      string
    ToState     string
    ToZip       string
    ToCountry   string

    // Parcel
    WeightOz    float64
    LengthIn    float64
    WidthIn     float64
    HeightIn    float64

    // Service
    ServiceCode string  // e.g. "usps_priority", "fedex_ground"
    Reference   string  // order number, for carrier reference field
}

type LabelResult struct {
    TrackingNumber string
    LabelURL       string  // URL to printable PDF or PNG label
    CarrierName    string
    ServiceName    string
    RateCents      int     // what was charged for the label
    Currency       string
}

type LabelProvider interface {
    CreateLabel(ctx context.Context, req LabelRequest) (*LabelResult, error)
    SupportedServices(ctx context.Context) ([]string, error)
}
```

Concrete implementations live in `platform/shipping/`:

```
platform/shipping/
    provider.go      ← LabelProvider interface, LabelRequest, LabelResult
    shippo.go        ← ShippoProvider
    easypost.go      ← EasyPostProvider
```

Neither `ShippoProvider` nor `EasyPostProvider` is referenced outside `platform/shipping/` and `cmd/server/main.go`. The rest of the application depends only on the `LabelProvider` interface.

### Shipment record

Label generation produces a shipment record linked to an order. An order may have more than one shipment (partial fulfillment, reshipment after loss), so this is a separate table, not columns on `orders`.

```sql
CREATE TYPE shipment_status AS ENUM (
    'pending',    -- order placed, no label yet
    'label_created', -- label generated, not yet handed to carrier
    'in_transit', -- carrier has scanned the package
    'delivered',  -- carrier confirms delivery
    'exception'   -- carrier exception (lost, returned, damaged)
);

CREATE TABLE shipments (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id        uuid NOT NULL REFERENCES orders(id),
    status          shipment_status NOT NULL DEFAULT 'pending',

    -- Label details (populated when label is generated)
    provider        text,           -- 'shippo' | 'easypost'
    tracking_number text,
    label_url       text,           -- URL to printable label
    carrier_name    text,
    service_name    text,
    label_cost_cents int,           -- what the merchant was charged
    label_currency  text,

    -- Parcel details (recorded at label creation for reference)
    weight_oz       numeric(8,2),
    length_in       numeric(8,2),
    width_in        numeric(8,2),
    height_in       numeric(8,2),

    created_at      timestamptz NOT NULL DEFAULT now(),
    label_created_at timestamptz,
    shipped_at      timestamptz,    -- when handed to carrier
    delivered_at    timestamptz,

    created_by      uuid            -- staff.id who generated the label
);

CREATE INDEX idx_shipments_order ON shipments (order_id);
```

### Label generation service method

Label generation is a staff action — it lives in `app/fulfillment.go` and is called from the admin handler.

```go
// app/fulfillment.go

func (s *FulfillmentService) CreateShipmentLabel(
    ctx     context.Context,
    tx      pgx.Tx,
    orderID uuid.UUID,
    req     domain.LabelRequest,
    actor   domain.StaffActor,
) (*domain.Shipment, error) {

    // 1. Verify order exists and is in a shippable state
    order, err := s.orders.GetByID(ctx, tx, orderID)
    if err != nil { return nil, err }
    if order.FulfillmentStatus == domain.FulfillmentStatusShipped {
        return nil, ErrOrderAlreadyShipped
    }

    // 2. Call provider (outside the transaction — external HTTP call)
    // Note: provider call happens before tx write intentionally.
    // If the provider succeeds but the DB write fails, the label exists
    // in the provider's system. The staff member can retrieve it manually
    // via the provider dashboard using the order number in the Reference field.
    result, err := s.labelProvider.CreateLabel(ctx, req)
    if err != nil { return nil, fmt.Errorf("create label: %w", err) }

    // 3. Persist the shipment record
    shipment, err := s.shipments.Create(ctx, tx, store.CreateShipmentParams{
        OrderID:         orderID,
        Provider:        result.CarrierName,
        TrackingNumber:  result.TrackingNumber,
        LabelURL:        result.LabelURL,
        CarrierName:     result.CarrierName,
        ServiceName:     result.ServiceName,
        LabelCostCents:  result.RateCents,
        LabelCurrency:   result.Currency,
        WeightOz:        req.WeightOz,
        LengthIn:        req.LengthIn,
        WidthIn:         req.WidthIn,
        HeightIn:        req.HeightIn,
        CreatedBy:       actor.ID,
    })
    if err != nil { return nil, err }

    // 4. Update order fulfillment status
    _, err = s.orders.UpdateFulfillmentStatus(ctx, tx, orderID, domain.FulfillmentStatusShipped)
    if err != nil { return nil, err }

    // 5. Audit — same tx
    s.audit.Record(ctx, tx, audit.AuditEntry{
        ActorType:    audit.ActorStaff,
        ActorID:      &actor.ID,
        ActorName:    actor.Name,
        Action:       audit.AuditShipmentLabelCreated,
        ResourceType: "shipment",
        ResourceID:   shipment.ID,
        After:        shipment,
        Metadata: map[string]any{
            "order_id":        orderID,
            "tracking_number": result.TrackingNumber,
            "provider":        result.CarrierName,
        },
    })

    return shipment, nil
}
```

### External call outside the transaction

The provider API call happens before the database write, outside the transaction. This is intentional — a long-running HTTP call inside a database transaction holds a connection and a lock for the duration of the call.

The failure mode is: provider succeeds, DB write fails. The label exists in the provider's system but not in the application. This is recoverable — the staff member can look up the label in the Shippo or EasyPost dashboard using the order number stored in the `Reference` field of the label request. It is logged as an error with full context.

The alternative failure mode (DB write succeeds, provider call fails) does not occur because the provider call happens first.

### Admin UI flow

The admin order detail page shows a "Generate Label" button when the order's fulfillment status is not yet `shipped`. Clicking it opens a form with parcel dimensions and service selection. On submit:

1. Handler calls `FulfillmentService.CreateShipmentLabel`
2. On success: order detail page refreshes via htmx swap showing the new shipment record with tracking number and a link to the label PDF
3. On failure: flash error message, no state change

The label URL is rendered as a direct link in the admin — staff clicks it to open the printable label in a new tab. No label rendering happens inside the application.

---

## Audit actions

Add to `platform/audit/actions.go`:

```go
AuditShipmentLabelCreated = "shipment.label_created"
```

Shipping config changes are also audited:

```go
AuditShippingConfigUpdated = "shipping_config.updated"
```

---

## Domain type additions

```go
// domain/shipping.go

type ShippingConfig struct {
    FlatRateCents         int
    FreeShippingThreshold *int   // nil = no free shipping threshold
    Currency              string
}

func (c ShippingConfig) Calculate(subtotalCents int) int {
    if c.FreeShippingThreshold != nil && subtotalCents >= *c.FreeShippingThreshold {
        return 0
    }
    return c.FlatRateCents
}

type ShipmentStatus string
const (
    ShipmentStatusPending      ShipmentStatus = "pending"
    ShipmentStatusLabelCreated ShipmentStatus = "label_created"
    ShipmentStatusInTransit    ShipmentStatus = "in_transit"
    ShipmentStatusDelivered    ShipmentStatus = "delivered"
    ShipmentStatusException    ShipmentStatus = "exception"
)

type Shipment struct {
    ID              uuid.UUID
    OrderID         uuid.UUID
    Status          ShipmentStatus
    Provider        string
    TrackingNumber  string
    LabelURL        string
    CarrierName     string
    ServiceName     string
    LabelCostCents  int
    LabelCurrency   string
    WeightOz        float64
    LengthIn        float64
    WidthIn         float64
    HeightIn        float64
    CreatedBy       uuid.UUID
    CreatedAt       time.Time
    LabelCreatedAt  *time.Time
    ShippedAt       *time.Time
    DeliveredAt     *time.Time
}
```

---

## Package placement

| Concern | Location |
|---|---|
| `ShippingConfig`, `Shipment`, `ShipmentStatus` types | `internal/domain/shipping.go` |
| `ShippingConfig.Calculate()` | `internal/domain/shipping.go` |
| `LabelProvider` interface, `LabelRequest`, `LabelResult` | `internal/platform/shipping/provider.go` |
| `ShippoProvider` | `internal/platform/shipping/shippo.go` |
| `EasyPostProvider` | `internal/platform/shipping/easypost.go` |
| Shipping config store | `internal/store/shipping.go` |
| Shipments store | `internal/store/shipments.go` |
| `CheckoutService.CalculateShipping` | `internal/app/checkout.go` |
| `FulfillmentService.CreateShipmentLabel` | `internal/app/fulfillment.go` |
| Admin label generation handler | `internal/web/fulfillment.go` |
| Shipping config admin handler | `internal/web/admin/shipping.go` |
| Checkout rate display | `internal/ui/storefront/checkout.templ` |
| Admin shipment detail | `internal/ui/admin/fulfillment.templ` |

The `LabelProvider` is injected into `FulfillmentService` at startup in `cmd/server/main.go`. Which concrete provider is used is a configuration choice — read from an environment variable:

```go
// cmd/server/main.go

var labelProvider shipping.LabelProvider
switch cfg.ShippingProvider {
case "shippo":
    labelProvider = shipping.NewShippoProvider(cfg.ShippoAPIKey)
case "easypost":
    labelProvider = shipping.NewEasyPostProvider(cfg.EasyPostAPIKey)
default:
    log.Fatal("unknown shipping provider: ", cfg.ShippingProvider)
}
```

---

## What this design defers

**Tracking updates** — the `shipment_status` column and `in_transit`/`delivered`/`exception` enum values are present in the schema but nothing populates them yet. A future River job could poll the provider's tracking API on a schedule and update the status. The schema is ready; the job is not implemented.

**Tracking emails** — when a label is created, a tracking number is stored. Emailing it to the customer is a River job (`jobs/shipment_notification.go`) enqueued from `FulfillmentService.CreateShipmentLabel` inside the same transaction. Not implemented now but the hook point is the service method.

**Shipping zones** — rate calculation currently has no concept of destination. Adding zones means adding a `shipping_zones` table with state/region rules and changing `ShippingConfig.Calculate` to accept a destination parameter. The interface is small enough that this is a contained change.

**Returns / RMA** — a return shipment is a new `shipments` row with a direction field (`outbound` / `return`). No schema changes needed beyond adding the direction column.
