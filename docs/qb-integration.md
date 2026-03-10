# Lean Commerce — QuickBooks Online Integration

B2B wholesale billing integration. QuickBooks Online (QBO) is the system of record for
invoicing and ACH payment collection. Hiri owns orders; QB owns billing. Webhooks close
the loop when payment is collected.

---

## Data flow

```
1. Wholesale order placed in Hiri
       ↓
2. River job: EnsureQBCustomer
   — create QB customer if first order, update if details have changed
       ↓
3. River job: CreateQBInvoice
   — create QB invoice from order line items, due date = placed_at + 7 days
   — store qb_invoice_id on the Hiri order
       ↓
4. QB stores invoice, charges ACH on due date (QB-owned, no Hiri involvement)
       ↓
5. QB sends webhook → POST /webhooks/quickbooks
   — event: Invoice Payment Reconciled
       ↓
6. Hiri webhook handler
   — verify QB signature
   — look up order by qb_invoice_id
   — update order payment status → captured
   — enqueue River job: send payment confirmation email
       ↓
7. River job: EmailInvoicePaid → wholesale customer notified
```

Each step is independently retryable. Steps 2 and 3 are River jobs enqueued atomically
with the order placement transaction. Steps 6 and 7 are driven by QB's webhook — Hiri
is a passive receiver.

---

## OAuth2 token management

QBO uses OAuth2 with short-lived credentials:

- **Access token:** expires every 60 minutes
- **Refresh token:** expires every 100 days (rotating — each refresh issues a new one)

### Token storage schema

```sql
CREATE TABLE qb_credentials (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    realm_id         text NOT NULL,         -- QBO company ID, required on every API call
    access_token     text NOT NULL,         -- encrypted at rest
    refresh_token    text NOT NULL,         -- encrypted at rest
    access_expires_at  timestamptz NOT NULL,
    refresh_expires_at timestamptz NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_qb_tenant UNIQUE (tenant_id)  -- one QB connection per tenant
);
```

Tokens are encrypted at rest using AES-256-GCM with a key from the application's secret
store (environment variable, never in the database). The `realm_id` (QBO company ID) is
stored alongside tokens — it is required as a path parameter on every QBO API request.

### OAuth2 flow — "Connect to QuickBooks" in admin

```
Staff clicks "Connect to QuickBooks" in /admin/settings/integrations
  → GET /admin/integrations/quickbooks/connect
  → Handler generates state param (random token, stored in session)
  → Redirect to QBO authorization URL:
     https://appcenter.intuit.com/connect/oauth2
       ?client_id=...
       &redirect_uri=https://yourdomain.com/admin/integrations/quickbooks/callback
       &response_type=code
       &scope=com.intuit.quickbooks.accounting
       &state=<session_state>

Staff authorizes in QBO
  → QBO redirects to GET /admin/integrations/quickbooks/callback?code=...&state=...&realmId=...
  → Handler validates state param (CSRF protection)
  → POST to QBO token endpoint to exchange code for access + refresh tokens
  → Encrypt and store in qb_credentials
  → Redirect to /admin/settings/integrations with success message
```

### Token refresh strategy

Access tokens expire every 60 minutes. Refresh proactively — never let a River job fail
because a token expired mid-flight.

```go
// platform/quickbooks/client.go

// ValidToken returns a valid access token, refreshing if needed.
// Acquires a DB-level advisory lock to prevent concurrent refresh races.
func (c *Client) ValidToken(ctx context.Context) (string, error) {
    creds, err := c.store.GetCredentials(ctx, c.tenantID)
    if err != nil {
        return "", err
    }

    // Refresh if token expires within 5 minutes
    if time.Until(creds.AccessExpiresAt) < 5*time.Minute {
        creds, err = c.refreshToken(ctx, creds)
        if err != nil {
            return "", fmt.Errorf("qb: token refresh failed: %w", err)
        }
    }

    return c.decrypt(creds.AccessToken)
}
```

The 5-minute buffer prevents a token from expiring between the check and the API call.

**Refresh token expiry (100 days):** A River job runs daily to check refresh token age.
If the refresh token expires within 14 days, it sends an alert email to the tenant admin:
"Your QuickBooks connection expires in N days — reconnect to avoid billing interruption."
If it expires, ACH billing stops until the admin reconnects. This is logged as a critical
error and surfaced in the admin dashboard.

---

## QB customer sync

### Hiri → QB customer mapping

| Hiri field | QB Customer field |
|---|---|
| `customers.company_name` | `DisplayName` (must be unique in QB) |
| `customers.first_name` | `GivenName` |
| `customers.last_name` | `FamilyName` |
| `customers.email` | `PrimaryEmailAddr.Address` |
| `customers.phone` | `PrimaryPhone.FreeFormNumber` |
| billing address | `BillAddr` |

### QB customer ID storage

```sql
ALTER TABLE customers
    ADD COLUMN qb_customer_id text,       -- QB's internal customer ID
    ADD COLUMN qb_synced_at   timestamptz; -- last successful sync timestamp
```

### EnsureQBCustomer River job

Runs after every wholesale order placement. Idempotent — safe to retry.

```go
type EnsureQBCustomerArgs struct {
    CustomerID uuid.UUID `json:"customer_id"`
    OrderID    uuid.UUID `json:"order_id"` // for chaining to CreateQBInvoice
}

func (w *EnsureQBCustomerWorker) Work(ctx context.Context, job *river.Job[EnsureQBCustomerArgs]) error {
    customer, err := w.customers.GetByID(ctx, job.Args.CustomerID)
    if err != nil {
        return err
    }

    if customer.QBCustomerID == nil {
        // First order — create QB customer
        qbID, err := w.qb.CreateCustomer(ctx, customer)
        if err != nil {
            return fmt.Errorf("qb: create customer: %w", err)
        }
        if err := w.customers.SetQBCustomerID(ctx, customer.ID, qbID); err != nil {
            return err
        }
        customer.QBCustomerID = &qbID
    } else {
        // Existing customer — sync if details have changed since last sync
        if customer.UpdatedAt.After(customer.QBSyncedAt) {
            if err := w.qb.UpdateCustomer(ctx, *customer.QBCustomerID, customer); err != nil {
                return fmt.Errorf("qb: update customer: %w", err)
            }
            if err := w.customers.SetQBSyncedAt(ctx, customer.ID, time.Now()); err != nil {
                return err
            }
        }
    }

    // Chain to invoice creation
    _, err = w.river.Insert(ctx, CreateQBInvoiceArgs{
        OrderID:      job.Args.OrderID,
        QBCustomerID: *customer.QBCustomerID,
    }, nil)
    return err
}
```

Chaining: `EnsureQBCustomer` inserts `CreateQBInvoice` after it succeeds. This keeps the
jobs sequenced without a single large job — each step is independently retryable.

### Customer sync on profile update

When a wholesale customer's details change in Hiri (name, email, address), a River job
is enqueued to sync the update to QB. This is separate from the order flow.

```go
type SyncQBCustomerArgs struct {
    CustomerID uuid.UUID `json:"customer_id"`
}
```

Enqueued from `app/customers.go: Update` whenever a wholesale customer record is saved,
using River's unique job feature to deduplicate — if a sync is already pending for this
customer, don't enqueue another one.

```go
// Deduplicate: only one pending sync per customer at a time
riverClient.Insert(ctx, SyncQBCustomerArgs{CustomerID: id}, &river.InsertOpts{
    UniqueOpts: river.UniqueOpts{
        ByArgs: true,
        ByState: []rivertype.JobState{
            rivertype.JobStateAvailable,
            rivertype.JobStateRunning,
        },
    },
})
```

---

## QB invoice creation

### Hiri order → QB invoice mapping

| Hiri field | QB Invoice field |
|---|---|
| `orders.id` (formatted) | `DocNumber` (visible reference) |
| `qb_customer_id` | `CustomerRef.value` |
| `placed_at + 7 days` | `DueDate` |
| order line items | `Line[]` (SalesItemLineDetail) |
| `orders.shipping_total` | `Line[]` (shipping service item) |
| `orders.tax_total` | `TxnTaxDetail` (if applicable) |

`DocNumber` is set to the Hiri order ID (or a formatted version like `ORD-00123`) so
the client can cross-reference invoices in QB with orders in Hiri without guesswork.

### QB invoice ID storage

```sql
ALTER TABLE orders
    ADD COLUMN qb_invoice_id   text,        -- QB's internal invoice ID
    ADD COLUMN qb_invoice_no   text,        -- QB's human-readable invoice number
    ADD COLUMN qb_synced_at    timestamptz; -- when invoice was created in QB
```

### CreateQBInvoice River job

```go
type CreateQBInvoiceArgs struct {
    OrderID      uuid.UUID `json:"order_id"`
    QBCustomerID string    `json:"qb_customer_id"`
}

func (w *CreateQBInvoiceWorker) Work(ctx context.Context, job *river.Job[CreateQBInvoiceArgs]) error {
    order, err := w.orders.GetByID(ctx, job.Args.OrderID)
    if err != nil {
        return err
    }

    // Idempotency: if invoice already created (job retry), skip
    if order.QBInvoiceID != nil {
        return nil
    }

    invoice, err := w.qb.CreateInvoice(ctx, QBInvoiceParams{
        CustomerID: job.Args.QBCustomerID,
        DocNumber:  formatOrderRef(order.ID),
        DueDate:    order.PlacedAt.Add(7 * 24 * time.Hour),
        Lines:      orderLinesToQBLines(order.Items),
        Shipping:   order.ShippingTotal,
    })
    if err != nil {
        return fmt.Errorf("qb: create invoice: %w", err)
    }

    return w.orders.SetQBInvoice(ctx, order.ID, invoice.ID, invoice.DocNumber)
}
```

Idempotency check on retry: if `qb_invoice_id` is already set, the job was already
completed — return nil. This prevents duplicate invoices if River retries after a
successful QB call but before the DB write completes.

---

## QB webhook handler

QBO sends webhooks for invoice events. The relevant event:
`com.intuit.quickbooks.accounting.Invoice` with `operation: Update` when payment is
applied and the invoice status becomes `Paid`.

### Webhook verification

QBO signs webhook payloads using HMAC-SHA256 with a verifier token (separate from OAuth
credentials, configured in the QBO developer portal). Verify before processing:

```go
// POST /webhooks/quickbooks
func handleQBWebhook(deps *Deps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
        if err != nil {
            w.WriteHeader(http.StatusBadRequest)
            return
        }

        // Verify signature
        sig := r.Header.Get("intuit-signature")
        if !verifyQBSignature(sig, body, deps.Config.QBWebhookVerifierToken) {
            w.WriteHeader(http.StatusUnauthorized)
            return
        }

        // Always return 200 quickly — QB retries on non-2xx
        w.WriteHeader(http.StatusOK)

        // Process asynchronously
        var payload QBWebhookPayload
        if err := json.Unmarshal(body, &payload); err != nil {
            slog.Error("qb webhook: unmarshal failed", "error", err)
            return
        }

        for _, notification := range payload.EventNotifications {
            for _, entity := range notification.DataChangeEvent.Entities {
                if entity.Name == "Invoice" && entity.Operation == "Update" {
                    deps.River.Insert(r.Context(), ProcessQBInvoiceUpdateArgs{
                        QBInvoiceID: entity.ID,
                        RealmID:     notification.RealmID,
                    }, nil)
                }
            }
        }
    }
}
```

Return 200 immediately and process via River job. QB retries on non-2xx — returning
200 before processing prevents duplicate delivery on slow handlers.

### HMAC verification

```go
func verifyQBSignature(signature string, body []byte, verifierToken string) bool {
    mac := hmac.New(sha256.New, []byte(verifierToken))
    mac.Write(body)
    expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
    return hmac.Equal([]byte(signature), []byte(expected))
}
```

### ProcessQBInvoiceUpdate River job

```go
type ProcessQBInvoiceUpdateArgs struct {
    QBInvoiceID string `json:"qb_invoice_id"`
    RealmID     string `json:"realm_id"`
}

func (w *ProcessQBInvoiceUpdateWorker) Work(ctx context.Context, job *river.Job[ProcessQBInvoiceUpdateArgs]) error {
    // Fetch current invoice state from QB to confirm it's actually paid
    invoice, err := w.qb.GetInvoice(ctx, job.Args.QBInvoiceID)
    if err != nil {
        return fmt.Errorf("qb: fetch invoice: %w", err)
    }

    if invoice.Balance != 0 {
        // Not fully paid yet — partial payment, ignore
        return nil
    }

    // Look up Hiri order by QB invoice ID
    order, err := w.orders.GetByQBInvoiceID(ctx, job.Args.QBInvoiceID)
    if err != nil {
        return fmt.Errorf("qb: order lookup by invoice id: %w", err)
    }

    // Idempotency: already marked paid
    if order.PaymentStatus == domain.PaymentStatusCaptured {
        return nil
    }

    // Update order payment status in a transaction with audit record
    tx, err := w.db.Begin(ctx)
    if err != nil {
        return err
    }
    defer tx.Rollback(ctx)

    if err := w.orders.MarkPaymentCaptured(ctx, tx, order.ID, domain.PaymentMethodACH); err != nil {
        return err
    }

    w.audit.Record(ctx, tx, audit.AuditEntry{
        ActorType:    audit.AuditActorSystem,
        ActorName:    "system",
        Action:       audit.AuditOrderPaymentCaptured,
        ResourceType: "order",
        ResourceID:   order.ID,
        Metadata: map[string]any{
            "qb_invoice_id": job.Args.QBInvoiceID,
            "payment_method": "ach",
        },
    })

    if err := tx.Commit(ctx); err != nil {
        return err
    }

    // Notify customer
    _, err = w.river.Insert(ctx, jobs.EmailInvoicePaidArgs{
        OrderID:    order.ID,
        CustomerID: order.CustomerID,
    }, nil)
    return err
}
```

Fetch-then-check: always re-fetch the invoice from QB to confirm paid status before
updating Hiri. Webhooks can fire on any invoice update — partial payments, void events,
corrections — so `invoice.Balance == 0` is the authoritative signal, not the webhook
event type alone.

---

## Platform package structure

```
platform/quickbooks/
    client.go          — QBClient: HTTP client, token management, request signing
    auth.go            — OAuth2 flow: authorization URL, token exchange, refresh
    customers.go       — CreateCustomer, UpdateCustomer
    invoices.go        — CreateInvoice, GetInvoice
    webhook.go         — verifyQBSignature, QBWebhookPayload types
    errors.go          — QB API error types, retry classification

internal/jobs/
    qb_ensure_customer.go
    qb_create_invoice.go
    qb_process_invoice_update.go
    qb_sync_customer.go

web/handlers/
    webhooks/quickbooks.go   — POST /webhooks/quickbooks
    admin/integrations/qb.go — Connect/callback/disconnect handlers
```

### QBClient interface

```go
// platform/quickbooks/client.go

type QBClient interface {
    CreateCustomer(ctx context.Context, c *domain.Customer) (qbCustomerID string, err error)
    UpdateCustomer(ctx context.Context, qbID string, c *domain.Customer) error
    CreateInvoice(ctx context.Context, p QBInvoiceParams) (*QBInvoice, error)
    GetInvoice(ctx context.Context, qbInvoiceID string) (*QBInvoice, error)
}
```

Defined as an interface so a `MockQBClient` can be injected in tests — no real QB calls
needed for unit or integration tests.

---

## Error handling and retry strategy

QB API calls are River jobs — they retry automatically on failure. The retry
classification matters:

| Error type | Retry? | Strategy |
|---|---|---|
| Network timeout | Yes | River default backoff |
| QB 429 rate limit | Yes | Respect `Retry-After` header |
| QB 401 token expired | Yes | Refresh token, then retry |
| QB 400 bad request | No | Dead letter — data problem, needs investigation |
| QB 500 server error | Yes | River default backoff |

For 400 errors (bad request), log the full QB error response and move the job to River's
dead letter queue. These indicate a data mapping problem — wrong field type, missing
required field — that won't resolve on retry. Alert the admin.

### Token refresh race condition

If two River jobs run concurrently and both find an expired token, they'll both attempt
a refresh. The second refresh will fail because the first already rotated the refresh
token. Guard with a PostgreSQL advisory lock:

```go
func (c *Client) refreshToken(ctx context.Context, creds *QBCredentials) (*QBCredentials, error) {
    // Advisory lock key: hash of tenant_id
    lockKey := int64(creds.TenantID.ID()) // deterministic int from UUID

    _, err := c.db.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey)
    if err != nil {
        return nil, err
    }

    // Re-fetch after acquiring lock — another worker may have already refreshed
    creds, err = c.store.GetCredentials(ctx, c.tenantID)
    if err != nil {
        return nil, err
    }

    if time.Until(creds.AccessExpiresAt) >= 5*time.Minute {
        return creds, nil // already refreshed by another worker
    }

    // Perform refresh...
}
```

---

## Admin UI

### `/admin/settings/integrations`

Integration status panel — shows connected/disconnected state for each provider:

```
QuickBooks Online    [Connected — Rockabilly Roasting Co.]  [Disconnect]
                     Refresh token expires: 87 days
                     Last sync: 2 minutes ago
```

If disconnected:
```
QuickBooks Online    [Not connected]  [Connect to QuickBooks →]
```

### `/admin/settings/integrations/quickbooks`

Detail view after connecting:
- Realm ID, company name (fetched from QB CompanyInfo endpoint on connect)
- Token expiry dates
- Recent sync log (last 20 QB API calls with status)
- Manual "Re-sync all customers" button — enqueues SyncQBCustomer for all wholesale
  customers with `qb_customer_id IS NOT NULL` — useful after a QB data migration

---

## Audit actions to add

```go
AuditQBCustomerCreated  = "qb.customer_created"
AuditQBCustomerUpdated  = "qb.customer_synced"
AuditQBInvoiceCreated   = "qb.invoice_created"
AuditOrderPaymentCaptured = "order.payment_captured"  // new — used for ACH payment received
```

`order.payment_captured` is actor=system, metadata includes `qb_invoice_id` and
`payment_method: "ach"`. This is the audit trail entry that proves payment was received
and from which source.

---

## Migration

Migration `025_quickbooks`:

```sql
-- Token storage
CREATE TABLE qb_credentials (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    realm_id           text NOT NULL,
    access_token       text NOT NULL,
    refresh_token      text NOT NULL,
    access_expires_at  timestamptz NOT NULL,
    refresh_expires_at timestamptz NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_qb_tenant UNIQUE (tenant_id)
);

-- Customer sync tracking
ALTER TABLE customers
    ADD COLUMN qb_customer_id text,
    ADD COLUMN qb_synced_at   timestamptz;

CREATE INDEX idx_customers_qb_id ON customers (qb_customer_id)
    WHERE qb_customer_id IS NOT NULL;

-- Order invoice tracking
ALTER TABLE orders
    ADD COLUMN qb_invoice_id  text,
    ADD COLUMN qb_invoice_no  text,
    ADD COLUMN qb_synced_at   timestamptz;

CREATE INDEX idx_orders_qb_invoice ON orders (qb_invoice_id)
    WHERE qb_invoice_id IS NOT NULL;
```

---

## Environment variables

```bash
# OAuth2
QB_CLIENT_ID=...
QB_CLIENT_SECRET=...
QB_REDIRECT_URI=https://yourdomain.com/admin/integrations/quickbooks/callback

# Webhook verification
QB_WEBHOOK_VERIFIER_TOKEN=...

# Token encryption key (AES-256, 32 bytes, base64-encoded)
QB_TOKEN_ENCRYPTION_KEY=...

# QBO sandbox vs production
QB_ENVIRONMENT=sandbox  # or "production"
```

`QB_TOKEN_ENCRYPTION_KEY` is separate from the application's main secret — rotating it
requires re-encrypting stored tokens, which is a deliberate operational step.

---

## What this integration does not handle

- **ACH authorization collection** — the client collects bank details directly in QB.
  Hiri has no access to banking information and does not need to.
- **Failed ACH payments** — QB handles retry logic for failed ACH. If a payment fails
  permanently, QB will not fire a payment reconciled webhook. The invoice remains
  `pending_payment` in Hiri indefinitely. A separate alert (River job polling for
  overdue invoices) can surface these — not in scope for initial build.
- **Credit memos and refunds** — wholesale refunds are handled in QB directly. Out of
  scope for v1; can be added as a QB credit memo sync in a future iteration.
- **QB item/product sync** — Hiri sends line item descriptions and amounts as text
  strings. Hiri products are not synced to QB Items. This keeps the integration simple;
  QB's reporting works on the invoice totals, not product catalog sync.
