# Lean Commerce — Audit Logging

Actors logged: **staff**, **system/background jobs**, and **customers** (for self-service mutations with support or dispute value). Customer actions on their own data are audited when the action is irreversible or commonly disputed.

Capture: **action + resource reference + after snapshot**.

Write strategy: **same transaction as the domain change**. A change without an audit record is worse than a failed change.

---

## Schema

```sql
CREATE TYPE audit_actor_type AS ENUM ('staff', 'system', 'customer');

CREATE TABLE audit_log (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    occurred_at   timestamptz NOT NULL DEFAULT now(),

    -- Who did it
    actor_type    audit_actor_type NOT NULL,
    actor_id      uuid,             -- staff.id, customers.id, or null for system events
    actor_name    text,             -- denormalized: name/email at time of action
                                    -- (accounts may be deactivated/renamed later)

    -- What they did
    action        text NOT NULL,    -- namespaced verb: 'order.refunded',
                                    --   'product.price_updated', 'subscription.cancelled'

    -- What they did it to
    resource_type text NOT NULL,    -- 'order', 'product', 'subscription', etc.
    resource_id   uuid NOT NULL,

    -- State after the change
    after         jsonb,            -- snapshot of the resource post-change
                                    -- null for destructive actions (soft deletes)

    -- Context
    request_id    text,             -- correlates to application log for this request
    ip_address    text,             -- null for system actions
    reason        text,             -- optional human note (e.g. "customer requested refund")
    metadata      jsonb             -- catch-all for action-specific context
                                    -- e.g. {"refund_amount": 1500, "refund_reason": "damaged"}
);

-- Query patterns:
-- "What happened to order X?"
CREATE INDEX idx_audit_resource ON audit_log (resource_type, resource_id, occurred_at DESC);

-- "What did staff member Y do this week?"
CREATE INDEX idx_audit_actor ON audit_log (actor_type, actor_id, occurred_at DESC);

-- "Show me all refunds in the last 30 days"
CREATE INDEX idx_audit_action ON audit_log (action, occurred_at DESC);
```

### Design decisions called out

**`actor_name` is denormalized.** Staff accounts can be deactivated and names can change. If you store only `actor_id` and later query `JOIN staff ON actor_id = staff.id`, a deactivated or renamed staff member produces confusing results. Storing the name at the time of action makes the audit record self-contained and human-readable in perpetuity.

**`action` is a namespaced verb string, not an enum.** Enums require migrations to extend. New audited actions ship without schema changes — just a new constant in application code. The namespace prefix (`order.`, `product.`, `subscription.`) makes the log queryable by domain area without parsing.

**`after` is nullable.** Soft deletes and cancellations produce a meaningful audit record even if the post-state is "this thing no longer exists in its previous form." In those cases, `after` can be null and `metadata` carries the relevant context.

**No `before` column.** The previous state is always queryable: find the most recent audit row for this resource where `occurred_at < this_event.occurred_at`. That query is cheap with the resource index. Before snapshots would double storage for a query pattern that's rarely needed.

---

## Action namespace conventions

Actions follow `resource.verb` format. Consistency here is what makes the log queryable — a team convention, enforced by defining all actions as constants.

```go
// Order actions
const (
    AuditOrderCreated         = "order.created"
    AuditOrderStatusChanged   = "order.status_changed"
    AuditOrderRefunded        = "order.refunded"
    AuditOrderCancelled       = "order.cancelled"
    AuditOrderNoteAdded       = "order.note_added"
    AuditOrderFulfilled       = "order.fulfilled"
    AuditOrderShipped         = "order.shipped"
)

// Product / pricing actions
const (
    AuditProductCreated       = "product.created"
    AuditProductUpdated       = "product.updated"
    AuditProductArchived      = "product.archived"
    AuditVariantPriceUpdated  = "variant.price_updated"
)

// Subscription actions
const (
    AuditSubscriptionCreated  = "subscription.created"
    AuditSubscriptionPaused   = "subscription.paused"   // staff or customer actor
    AuditSubscriptionResumed  = "subscription.resumed"  // staff or customer actor
    AuditSubscriptionCancelled= "subscription.cancelled" // staff or customer actor
    AuditSubscriptionRenewed  = "subscription.renewed"          // system actor
    AuditSubscriptionRenewalFailed = "subscription.renewal_failed" // system actor
)

// Customer actions — staff-initiated mutations on customer accounts
const (
    AuditCustomerGroupChanged    = "customer.group_changed"
    AuditCustomerDeactivated     = "customer.deactivated"
    AuditCustomerAddressAdded    = "customer.address_added"    // customer actor
    AuditCustomerAddressUpdated  = "customer.address_updated"  // customer actor
    AuditCustomerAddressDeleted  = "customer.address_deleted"  // customer actor
)

// Wholesale actions — staff-initiated
const (
    AuditWholesaleApplicationApproved = "wholesale.application_approved"
    AuditWholesaleApplicationRejected = "wholesale.application_rejected"
    AuditWholesalePriceListUpdated    = "wholesale.price_list_updated"
)

// Invoice actions — staff-initiated
const (
    AuditInvoiceSent            = "invoice.sent"
    AuditInvoicePaymentRecorded = "invoice.payment_recorded"
    AuditInvoiceVoided          = "invoice.voided"
)

// Coupon actions — staff-initiated
const (
    AuditCouponCreated          = "coupon.created"
    AuditCouponDeactivated      = "coupon.deactivated"
)

// Shipment actions — system actor (River job)
const (
    AuditShipmentLabelCreated   = "shipment.label_created"
)

// Auth events — security-relevant, always audited
const (
    AuditAuthStaffLogin         = "auth.staff_login"          // staff actor
    AuditAuthStaffLoginFailed   = "auth.staff_login_failed"   // system actor (no valid staff yet)
    AuditAuthMagicLinkRequested = "auth.magic_link_requested" // system actor
    AuditAuthStaffLogout        = "auth.staff_logout"         // staff actor
)

// Staff management actions
const (
    AuditStaffCreated           = "staff.created"
    AuditStaffRoleChanged       = "staff.role_changed"
    AuditStaffDeactivated       = "staff.deactivated"
)
```

---

## The AuditEntry type and writer

The audit writer is a thin struct with a single method. It takes an open transaction and appends a row. It never opens or commits its own transaction — the caller controls transaction scope.

```go
type AuditActorType string

const (
    AuditActorStaff    AuditActorType = "staff"
    AuditActorSystem   AuditActorType = "system"
    AuditActorCustomer AuditActorType = "customer"
)

type AuditEntry struct {
    ActorType    AuditActorType
    ActorID      *uuid.UUID   // pointer — nil for pure system events
    ActorName    string       // staff name, customer email, or "system"
    Action       string
    ResourceType string
    ResourceID   uuid.UUID
    After        any          // will be marshalled to jsonb; nil is fine
    RequestID    string
    IPAddress    string
    Reason       string
    Metadata     map[string]any
}

type AuditWriter struct {
    // no fields needed — it writes to whatever tx it's given
}

func (w *AuditWriter) Record(ctx context.Context, tx pgx.Tx, entry AuditEntry) error {
    afterJSON, err := json.Marshal(entry.After)
    if err != nil {
        return fmt.Errorf("audit: marshal after snapshot: %w", err)
    }

    _, err = tx.Exec(ctx, `
        INSERT INTO audit_log (
            actor_type, actor_id, actor_name,
            action, resource_type, resource_id,
            after, request_id, ip_address, reason, metadata
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
        entry.ActorType, entry.ActorID, entry.ActorName,
        entry.Action, entry.ResourceType, entry.ResourceID,
        afterJSON, entry.RequestID, entry.IPAddress,
        entry.Reason, entry.Metadata,
    )
    return err
}
```

---

## Usage pattern: service layer

The audit write is always inside the same transaction as the domain change. The service receives both the transaction and an actor (resolved by the handler from the request context), and passes both into the repository and audit writer.

```go
// OrderService.Refund — illustrates the pattern
func (s *OrderService) Refund(
    ctx     context.Context,
    tx      pgx.Tx,
    orderID uuid.UUID,
    amount  int,       // cents
    reason  string,
    actor   StaffActor,
) error {
    // 1. Domain write — inside tx
    order, err := s.orders.ApplyRefund(ctx, tx, orderID, amount)
    if err != nil {
        return err
    }

    // 2. Audit write — same tx
    return s.audit.Record(ctx, tx, AuditEntry{
        ActorType:    AuditActorStaff,
        ActorID:      &actor.ID,
        ActorName:    actor.Name,
        Action:       AuditOrderRefunded,
        ResourceType: "order",
        ResourceID:   orderID,
        After:        order,   // post-refund order snapshot
        RequestID:    RequestIDFromContext(ctx),
        IPAddress:    actor.IPAddress,
        Reason:       reason,
        Metadata:     map[string]any{"refund_amount": amount},
    })
}
```

The handler opens the transaction, calls the service, and commits or rolls back:

```go
func handleIssueRefund(deps *Deps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        actor := StaffFromContext(r.Context()) // set by session middleware

        tx, err := deps.DB.Begin(r.Context())
        if err != nil { /* handle */ }
        defer tx.Rollback(r.Context())

        err = deps.Orders.Refund(r.Context(), tx, orderID, amount, reason, actor)
        if err != nil { /* handle */ }

        if err = tx.Commit(r.Context()); err != nil { /* handle */ }
        // Audit record committed atomically with the refund.
        // If Commit fails, both are rolled back — no orphaned audit entry.
    }
}
```

---

## System actor pattern

Background jobs (River workers) don't have a session or an IP address. They use a sentinel system actor:

```go
var SystemActor = AuditEntry{
    ActorType: AuditActorSystem,
    ActorID:   nil,
    ActorName: "system",
}
```

River workers that modify audited domain state follow the same pattern — they open a transaction, do the domain write, append to audit_log, commit. The actor is always `SystemActor`.

```go
// SubscriptionRenewalWorker.Work
func (w *SubscriptionRenewalWorker) Work(ctx context.Context, job *river.Job[RenewalArgs]) error {
    tx, err := w.db.Begin(ctx)
    // ...
    sub, err := w.subscriptions.Renew(ctx, tx, job.Args.SubscriptionID)
    // ...
    w.audit.Record(ctx, tx, AuditEntry{
        ActorType:    AuditActorSystem,
        ActorID:      nil,
        ActorName:    "system",
        Action:       AuditSubscriptionRenewed,
        ResourceType: "subscription",
        ResourceID:   job.Args.SubscriptionID,
        After:        sub,
        Metadata:     map[string]any{"river_job_id": job.ID},
    })
    return tx.Commit(ctx)
}
```

Including `river_job_id` in metadata means you can correlate an audit entry to the exact River job that produced it — useful when diagnosing a renewal that went wrong.

---

## Querying the log

Three query patterns cover almost all operational needs:

**"What happened to this order?"**

```sql
SELECT actor_name, action, after, occurred_at, reason
FROM audit_log
WHERE resource_type = 'order' AND resource_id = $1
ORDER BY occurred_at ASC;
```

**"What did this staff member do today?"**

```sql
SELECT action, resource_type, resource_id, occurred_at, metadata
FROM audit_log
WHERE actor_id = $1
  AND occurred_at >= now() - interval '24 hours'
ORDER BY occurred_at DESC;
```

**"All refunds in the last 30 days"**

```sql
SELECT actor_name, resource_id, metadata->>'refund_amount' AS amount,
       occurred_at, reason
FROM audit_log
WHERE action = 'order.refunded'
  AND occurred_at >= now() - interval '30 days'
ORDER BY occurred_at DESC;
```

---

## Customer actor pattern

Customer self-service actions use `AuditActorCustomer`. The handler resolves the customer from the session, same as staff actors are resolved. `actor_name` stores the customer's email — it's the most useful identifier for support queries and is already denormalized for the same reason as staff names.

```go
// web/middleware.go — retail session middleware sets this in context
type CustomerActor struct {
    ID        uuid.UUID
    Email     string
    IPAddress string
}

// In a customer-initiated service call:
func (s *SubscriptionService) Cancel(
    ctx        context.Context,
    tx         pgx.Tx,
    subID      uuid.UUID,
    actor      CustomerActor,
    reason     string,
) error {
    sub, err := s.subscriptions.Cancel(ctx, tx, subID, actor.ID) // ownership enforced in store
    if err != nil {
        return err
    }
    return s.audit.Record(ctx, tx, AuditEntry{
        ActorType:    AuditActorCustomer,
        ActorID:      &actor.ID,
        ActorName:    actor.Email,
        Action:       AuditSubscriptionCancelled,
        ResourceType: "subscription",
        ResourceID:   subID,
        After:        sub,
        RequestID:    RequestIDFromContext(ctx),
        IPAddress:    actor.IPAddress,
        Reason:       reason,
    })
}
```

The store query for customer-initiated mutations always includes a `customer_id` ownership check — a customer cannot cancel another customer's subscription. The audit record is only written after that check passes.

---

## Schema migration note

The `customer` actor type requires a migration to extend the enum before any customer actor call sites are wired:

```sql
-- migration: add_customer_audit_actor
ALTER TYPE audit_actor_type ADD VALUE 'customer';
```

PostgreSQL `ADD VALUE` on an enum does not require a table rewrite and is safe on a live database. It does require its own migration — it cannot be run inside a transaction that also modifies tables using that enum.

---

## Coverage map

Full inventory of audited mutations, actors, and wiring status. Update this as call sites are added.

### Orders

| Mutation | Action | Actor | Call site | Status |
|---|---|---|---|---|
| Order placed (retail) | `order.created` | system | `app/checkout.go: PlaceOrder` | ✅ |
| Order placed (wholesale) | `order.created` | staff or customer | `app/wholesale.go: PlaceWholesaleOrder` | ✅ |
| Status changed | `order.status_changed` | staff or system | `app/orders.go: UpdateOrderStatus` | ✅ |
| Refund issued | `order.refunded` | staff | `app/orders.go: RefundOrder` | ✅ |
| Order cancelled | `order.cancelled` | staff | `app/orders.go: CancelOrder` | ✅ |
| Fulfilled | `order.fulfilled` | staff | `app/orders.go: FulfillOrder` | ✅ |
| Shipped | `order.shipped` | staff | `app/orders.go: ShipOrder` | ✅ |
| Payment status changed | `order.status_changed` | staff or system | `app/orders.go: UpdatePaymentStatus` | ✅ |
| Fulfillment status changed | `order.fulfilled` | staff | `app/orders.go: UpdateFulfillmentStatus` | ✅ |

### Products

| Mutation | Action | Actor | Call site | Status |
|---|---|---|---|---|
| Product created | `product.created` | staff | `app/catalog.go: CreateProduct` | ✅ |
| Product updated | `product.updated` | staff | `app/catalog.go: UpdateProduct` | ✅ |
| Product archived | `product.archived` | staff | `app/catalog.go: UpdateProductStatus` | ✅ |
| Product deleted | `product.deleted` | staff | `app/catalog.go: DeleteProduct` | ✅ |
| Variant created | `variant.created` | staff | `app/catalog.go: CreateVariant` | ✅ |
| Variant updated | `variant.updated` | staff | `app/catalog.go: UpdateVariant` | ✅ |
| Variant deleted | `variant.deleted` | staff | `app/catalog.go: DeleteVariant` | ✅ |
| Variant price updated | `variant.price_updated` | staff | `app/catalog.go: UpdateVariantPrice` | ⬜ wire |
| Product media added | `product_media.added` | staff | `app/catalog.go: CreateProductMedia` | ✅ |
| Product media deleted | `product_media.deleted` | staff | `app/catalog.go: DeleteProductMedia` | ✅ |

### Subscriptions

| Mutation | Action | Actor | Call site | Status |
|---|---|---|---|---|
| Created | `subscription.created` | system or staff | `app/subscriptions.go: CreateSubscription` | ✅ |
| Plan created | `subscription_plan.created` | staff | `app/subscriptions.go: CreatePlan` | ✅ |
| Plan activated/deactivated | `subscription_plan.deactivated` | staff | `app/subscriptions.go: UpdatePlanActive` | ✅ |
| Plan discount updated | `subscription_plan.updated` | staff | `app/subscriptions.go: UpdatePlanDiscount` | ✅ |
| Paused | `subscription.paused` | customer or staff | `app/subscriptions.go: PauseSubscription` | ✅ |
| Resumed | `subscription.resumed` | customer or staff | `app/subscriptions.go: ResumeSubscription` | ✅ |
| Cancelled | `subscription.cancelled` | customer or staff | `app/subscriptions.go: CancelSubscription` | ✅ |
| Renewal succeeded | `subscription.renewed` | system | `app/renewal.go: processRenewal` | ✅ |
| Renewal failed | `subscription.renewal_failed` | system | `app/renewal.go: processRenewal` | ✅ |

### Customers

| Mutation | Action | Actor | Call site | Status |
|---|---|---|---|---|
| Group changed | `customer.group_changed` | staff | `app/customers.go: UpdateCustomerGroup` | ✅ |
| Tax exemption granted | `customer.tax_exemption_granted` | staff | `app/customers.go: GrantTaxExemption` | ✅ |
| Tax exemption revoked | `customer.tax_exemption_revoked` | staff | `app/customers.go: RevokeTaxExemption` | ✅ |
| Address added | `customer.address_added` | customer | `app/customers.go: CreateAddress` | ✅ |
| Address updated | `customer.address_updated` | customer | `app/customers.go: UpdateAddress` | ✅ |
| Address deleted | `customer.address_deleted` | customer | `app/customers.go: DeleteAddress` | ✅ |

### Wholesale

| Mutation | Action | Actor | Call site | Status |
|---|---|---|---|---|
| Application approved | `wholesale.application_approved` | staff | `app/wholesale.go: ApproveApplication` | ✅ |
| Application declined | `wholesale.application_declined` | staff | `app/wholesale.go: DeclineApplication` | ✅ |
| Account suspended | `wholesale.account_suspended` | staff | `app/wholesale.go: SuspendAccount` | ✅ |
| Wholesale order placed | `order.created` | customer | `app/wholesale.go: PlaceWholesaleOrder` | ✅ |

### Invoices

| Mutation | Action | Actor | Call site | Status |
|---|---|---|---|---|
| Invoice created | `invoice.created` | staff | `app/invoices.go: CreateFromOrder` | ✅ |
| Invoice sent | `invoice.sent` | staff | `app/invoices.go: SendInvoice` | ✅ |
| Payment recorded | `invoice.payment_recorded` | staff | `app/invoices.go: RecordPayment` | ✅ |
| Invoice voided | `invoice.voided` | staff | `app/invoices.go: VoidInvoice` | ✅ |

### Discounts

| Mutation | Action | Actor | Call site | Status |
|---|---|---|---|---|
| Discount created | `discount.created` | staff | `app/discounts.go` | ⬜ wire when mutations added |
| Discount deactivated | `discount.deactivated` | staff | `app/discounts.go` | ⬜ wire when mutations added |

### Shipments

| Mutation | Action | Actor | Call site | Status |
|---|---|---|---|---|
| Label created | `shipment.label_created` | staff | `app/fulfillment.go: CreateShipmentLabel` | ✅ |

### Auth

| Event | Action | Actor | Call site | Status |
|---|---|---|---|---|
| Staff login success | `staff.login` | staff | `app/auth.go: StaffLogin` | ✅ |
| Staff logout | `staff.logout` | staff | `app/auth.go: StaffLogout` | ✅ |
| Customer login | `customer.login` | customer | `app/auth.go: CustomerLogin` | ✅ |
| Customer magic link | `customer.login` | system | `app/auth.go: CreateMagicLinkToken` | ✅ |

### Staff management

| Mutation | Action | Actor | Call site | Status |
|---|---|---|---|---|
| Staff created | `staff.created` | staff | `app/staff.go: Create` | ⬜ wire when feature built |
| Role changed | `staff.role_changed` | staff | `app/staff.go: ChangeRole` | ⬜ wire when feature built |
| Deactivated | `staff.deactivated` | staff | `app/staff.go: Deactivate` | ⬜ wire when feature built |

**Status key:** ✅ wired · ⬜ wire when feature is built · 🚫 explicitly not audited

---

## Implementation status

Infrastructure is complete. The audit writer, store, domain types, action constants, and admin UI viewer
are all in place. Most call sites are wired. Remaining ⬜ items will be wired when those features are built.

Auth events (step 5) are the only ones worth wiring before the feature is otherwise built — everything else can be wired at the same time the feature is implemented.

---

## Retention and volume

Audit log rows are append-only and never updated or deleted during normal operation. For most commerce platforms at moderate scale, Postgres handles this indefinitely without partitioning. If the table grows beyond tens of millions of rows, range partitioning by `occurred_at` (yearly or quarterly) is the right next step — it makes archival and pruning surgical without touching the query interface.

A reasonable retention policy: keep the last 2 years hot in the primary database, archive older rows to cold storage (S3 + Parquet, or a read replica). This is a future concern — design for it by keeping `occurred_at` the natural partition key from the start.

---

## What audit logging does not replace

- **Application logs** — `audit_log` records business events. It does not record request timing, error stack traces, or infrastructure events. Those belong in structured application logs (slog → Loki/CloudWatch).
- **Metrics** — `audit_log` is not the right source for "how many refunds per hour." Extract that to a metrics counter at the service layer; don't aggregate the audit table in real time.
- **Domain history** — `audit_log` is not an event sourcing log. The domain tables are the source of truth; audit_log is a supporting record of who caused changes and when.
