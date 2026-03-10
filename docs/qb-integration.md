# Lean Commerce — QuickBooks Online Integration

B2B wholesale billing integration. QuickBooks Online (QBO) is the system of record for
invoicing and ACH payment collection. Hiri owns orders; QB owns billing. Webhooks close
the loop when payment is collected.

---

## Data flow

### Path A: Automated ACH billing (default)

```
1. Wholesale order placed in Hiri
       ↓
2. River job: EnsureQBCustomer
   — find existing QB customer by company name / email, or create new
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

### Path B: Manual payment (check, cash, etc.)

When a customer pays outside the automated ACH flow (check at delivery, cash, etc.),
staff records the payment manually in the Hiri admin. The system syncs it to QB so both
systems stay in sync and ACH won't double-bill.

```
1. Staff records payment in admin → POST /admin/invoices/{id}/payment
   — amount, method (check/cash/other), reference (check #), optional note
       ↓
2. Hiri updates invoice status (partially_paid or paid)
   — syncs order.payment_status to match
   — enqueues River job: SyncQBPayment (atomically in same transaction)
       ↓
3. River job: SyncQBPayment
   — reads order to get qb_invoice_id
   — reads customer to get qb_customer_id
   — calls QB Payment API to record payment against the QB invoice
       ↓
4. QB invoice balance reduced (or zeroed out)
   — if fully paid, ACH will NOT fire (nothing to collect)
   — QB books reflect the manual payment with method and reference
       ↓
5. If QB later sends a webhook for this invoice (balance now 0):
   — Hiri sees order already "captured" → idempotency no-op
```

**Why record the payment in QB instead of voiding the invoice?** QB is the system of
record for B2B accounting. Voiding the invoice would erase the sale from the books.
Recording the payment preserves the invoice, the payment method, and the amount — the
accountant sees a complete picture in QB without needing to cross-reference Hiri.

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

### EnsureQBCustomer River job — IMPLEMENTED (find-or-create)

Runs after every wholesale order placement. Idempotent — safe to retry.

**Key design decision:** Many wholesale clients already exist in QuickBooks before Hiri
is deployed. The worker uses a **find-or-create** pattern to avoid duplicate QB customers:

1. Query QB by `DisplayName` (company name) — unique in QB, most reliable match
2. If no match, query QB by `PrimaryEmailAddr` (email) — fallback
3. If found, **link** the existing QB customer to the Hiri customer record
4. If not found, **create** a new QB customer in QB

This means the first wholesale order for a pre-existing client links to their existing QB
record rather than creating a duplicate. The audit trail distinguishes linked vs created
(`qb.customer_linked` vs `qb.customer_created`).

```
EnsureQBCustomer flow:
  customer.QBCustomerID == nil?
    ├─ YES → FindCustomer(displayName, email)
    │         ├─ found    → link existing QB customer (audit: qb.customer_linked)
    │         └─ not found → CreateCustomer (audit: qb.customer_created)
    └─ NO  → customer changed since last sync?
              ├─ YES → UpdateCustomer (audit: qb.customer_synced)
              └─ NO  → skip (no-op)
  then → chain to CreateQBInvoice
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

## Platform package structure — IMPLEMENTED

```
internal/platform/quickbooks/
    provider.go        — Client interface, domain types (Invoice, Payment, Credentials, etc.)
    client.go          — QBClient: HTTP client, token management, AES-256-GCM encryption
    auth.go            — OAuth2 flow: authorization URL, token exchange, refresh
    customers.go       — FindCustomer, CreateCustomer, UpdateCustomer
    invoices.go        — CreateInvoice, GetInvoice
    payments.go        — CreatePayment (record payment against QB invoice)
    webhook.go         — VerifySignature (HMAC-SHA256), WebhookPayload types
    errors.go          — QB API error types, retry classification

internal/jobs/
    qb_ensure_customer.go       — EnsureQBCustomerWorker (find-or-create + chain to invoice)
    qb_create_invoice.go        — CreateQBInvoiceWorker (with product descriptions)
    qb_process_invoice_update.go — ProcessQBInvoiceUpdateWorker (webhook → payment captured)
    qb_sync_customer.go         — SyncQBCustomerWorker (profile update sync)
    qb_sync_payment.go          — SyncQBPaymentWorker (manual payment → QB Payment API)

internal/store/
    qb_credentials.go  — QBCredentialStore (GetByTenantID, Upsert, Delete)

internal/web/
    webhook_qb.go      — POST /webhooks/quickbooks handler
    admin_settings.go   — Settings page + OAuth connect/callback/disconnect handlers

internal/ui/admin/
    settings.templ      — Admin settings page template with QB integration panel
```

### QBClient interface

```go
// platform/quickbooks/provider.go

type Client interface {
    FindCustomer(ctx context.Context, displayName, email string) (*QBCustomer, error)
    CreateCustomer(ctx context.Context, c *domain.Customer) (qbCustomerID string, err error)
    UpdateCustomer(ctx context.Context, qbID string, c *domain.Customer) error
    CreateInvoice(ctx context.Context, p InvoiceParams) (*Invoice, error)
    GetInvoice(ctx context.Context, qbInvoiceID string) (*Invoice, error)
    CreatePayment(ctx context.Context, p PaymentParams) (*Payment, error)
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

## Admin UI — IMPLEMENTED

### `/admin/settings`

Settings page with QuickBooks integration panel. Accessible via the sidebar "Settings"
link (already wired in the admin layout).

**Routes:**

| Method | Path | Handler |
|---|---|---|
| GET | `/admin/settings` | Settings page with QB status |
| GET | `/admin/settings/integrations/quickbooks/connect` | Initiates OAuth2 flow |
| GET | `/admin/settings/integrations/quickbooks/callback` | OAuth2 callback |
| POST | `/admin/settings/integrations/quickbooks/disconnect` | Remove QB connection |

**Files:**
- Template: `internal/ui/admin/settings.templ`
- Handler: `internal/web/admin_settings.go`

**Connection states:**

When disconnected:
- Green "Connect to QuickBooks" button redirects to QBO OAuth2 authorization
- If `QB_CLIENT_ID` env var is not set, shows a message that QB is not configured

When connected:
- Shows Realm ID (company ID), refresh token expiry with urgency colors:
  - Green text: >30 days remaining
  - Amber text: 14–30 days remaining
  - Red text + warning banner: <14 days remaining
- "Reconnect" button (same OAuth flow, upserts new tokens)
- "Disconnect" button with confirmation dialog

**OAuth2 CSRF protection:** Uses HMAC-SHA256 signed cookie for state parameter
validation. The signing key is derived from `QB_TOKEN_ENCRYPTION_KEY`.

### Future enhancements (not yet built)
- Fetch company name from QB CompanyInfo endpoint on connect
- Recent sync log (last 20 QB API calls with status)
- Manual "Re-sync all customers" button

---

## Audit actions — IMPLEMENTED

Defined in `internal/platform/audit/actions.go`:

```go
AuditQBCustomerCreated    = "qb.customer_created"    // new customer created in QB
AuditQBCustomerLinked     = "qb.customer_linked"     // existing QB customer linked to Hiri customer
AuditQBCustomerSynced     = "qb.customer_synced"     // customer details updated in QB
AuditQBInvoiceCreated     = "qb.invoice_created"     // invoice created in QB
AuditQBPaymentSynced      = "qb.payment_synced"      // manual payment recorded in QB
AuditOrderPaymentCaptured = "order.payment_captured"  // order marked paid (ACH or manual)
```

`order.payment_captured` is actor=system, metadata includes `qb_invoice_id` and
`payment_method: "ach"`. This is the audit trail entry that proves payment was received
and from which source.

`qb.customer_linked` vs `qb.customer_created` — the metadata includes `"linked": true`
when an existing QB customer was found, making it easy to audit how many customers were
linked vs newly created.

`qb.payment_synced` — metadata includes `qb_payment_id`, `amount_cents`, and `method`.
This proves the manual payment was successfully recorded in QB.

---

## Migration — IMPLEMENTED

Migration `031_quickbooks.sql` (in `db/migrations/`):

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
QB_REDIRECT_URI=https://yourdomain.com/admin/settings/integrations/quickbooks/callback

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

## Still TODO

- **Wholesale checkout integration** — enqueue `EnsureQBCustomer` job atomically with
  wholesale order placement in the checkout flow
- **EmailInvoicePaid worker** — sends payment confirmation email when ACH clears
- **Refresh token expiry alert job** — periodic River job that checks refresh token age
  and sends alert email to tenant admin when <14 days remain
- **Admin sync log** — recent QB API calls with status on the settings detail page
- **Manual re-sync button** — enqueue SyncQBCustomer for all wholesale customers
- **Fetch company name** from QB CompanyInfo endpoint on OAuth connect
- **Overdue invoice detection** — periodic job to flag orders where QB invoice is past
  due and no payment has been received (neither ACH nor manual)

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
