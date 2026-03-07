# Lean Commerce — Package Structure

Single binary deployment: HTTP server and River workers run in the same process. All application code lives under `internal/` — the Go toolchain enforces that nothing outside this module can import it.

**Frontend strategy:**
- **Storefront and admin:** server-rendered via `templ`. Pages, layouts, and components live alongside their handlers in `ui/`.
- **Checkout flow:** a single Svelte component, compiled to a JS bundle and embedded in the Go binary via `go:embed`. It communicates with Go via four narrow JSON endpoints in `web/checkout.go`. No SvelteKit, no separate frontend deployment.

---

## Directory tree

```
lean-commerce/
├── cmd/
│   └── server/
│       └── main.go              ← wires everything together, starts HTTP + River
│
├── internal/
│   ├── domain/                  ← pure types; imports nothing from this project
│   │   ├── order.go
│   │   ├── customer.go
│   │   ├── catalog.go
│   │   ├── subscription.go
│   │   ├── fulfillment.go
│   │   ├── shipping.go          ← ShippingConfig, Shipment, ShipmentStatus, ShippingConfig.Calculate()
│   │   ├── discount.go          ← Discount, CouponCode, AppliedDiscount, Discount.Evaluate()
│   │   └── audit.go
│   │
│   ├── store/                   ← all database access; imports domain/
│   │   ├── db.go                ← pgx pool setup, transaction helpers
│   │   ├── orders.go
│   │   ├── customers.go
│   │   ├── catalog.go
│   │   ├── subscriptions.go
│   │   ├── fulfillment.go
│   │   ├── sessions.go
│   │   ├── audit.go
│   │   ├── webhooks.go
│   │   ├── shipping.go          ← ShippingConfig store + Shipments store
│   │   └── discounts.go         ← Discounts store + CouponCodes store
│   │
│   ├── app/                     ← business logic; imports domain/, store/, platform/
│   │   ├── orders.go
│   │   ├── customers.go
│   │   ├── catalog.go
│   │   ├── subscriptions.go
│   │   ├── fulfillment.go       ← includes CreateShipmentLabel
│   │   ├── checkout.go          ← CalculateShipping, ApplyCouponCode, EvaluateAutomaticDiscounts
│   │   └── auth.go
│   │
│   ├── web/                     ← HTTP handlers + middleware; imports app/, platform/, domain/, ui/
│   │   ├── router.go            ← route registration for storefront, admin, and API
│   │   ├── middleware.go        ← logging, session, permission, metrics middleware
│   │   ├── orders.go            ← renders ui/storefront/orders or ui/admin/orders
│   │   ├── customers.go
│   │   ├── catalog.go
│   │   ├── subscriptions.go
│   │   ├── fulfillment.go       ← includes label generation handler
│   │   ├── auth.go
│   │   ├── webhooks.go
│   │   ├── discounts.go         ← admin discount + coupon code management handlers
│   │   ├── checkout.go          ← 5 JSON endpoints consumed by the Svelte checkout component
│   │   └── respond.go           ← shared response helpers (JSON, templ render, errors)
│   │
│   ├── ui/                      ← all templ templates; imports domain/ only
│   │   ├── layouts/
│   │   │   ├── storefront.templ ← outer HTML shell for storefront pages
│   │   │   └── admin.templ      ← outer HTML shell for admin pages
│   │   ├── storefront/          ← customer-facing page templates
│   │   │   ├── catalog.templ    ← product listing, product detail
│   │   │   ├── cart.templ
│   │   │   ├── checkout.templ   ← page shell that mounts the Svelte component
│   │   │   ├── orders.templ     ← customer order history, order detail
│   │   │   ├── account.templ    ← addresses, subscription management
│   │   │   └── auth.templ       ← login, register, reset password pages
│   │   ├── admin/               ← staff-facing page templates
│   │   │   ├── orders.templ     ← order list, order detail, fulfillment actions
│   │   │   ├── catalog.templ    ← product editor, variant pricing
│   │   │   ├── customers.templ  ← customer list, customer detail (includes tax exemption)
│   │   │   ├── fulfillment.templ ← includes label generation form + shipment record
│   │   │   ├── subscriptions.templ
│   │   │   ├── discounts.templ  ← discount list, editor, coupon code management
│   │   │   └── dashboard.templ  ← metrics panels, charts
│   │   └── components/          ← shared templ components used by both surfaces
│   │       ├── pagination.templ
│   │       ├── flash.templ      ← success/error flash messages
│   │       ├── table.templ
│   │       └── forms.templ      ← shared form field components
│   │
│   ├── jobs/                    ← River worker definitions; imports app/, platform/, domain/
│   │   ├── workers.go           ← worker registration
│   │   ├── subscription_renewal.go
│   │   ├── payment_retry.go
│   │   ├── order_confirmation.go
│   │   ├── cart_expiry.go
│   │   ├── abandoned_cart.go
│   │   └── session_prune.go
│   │
│   └── platform/                ← cross-cutting infrastructure
│       ├── audit/
│       │   ├── writer.go        ← AuditWriter, AuditEntry
│       │   └── actions.go       ← action name constants
│       ├── metrics/
│       │   └── registry.go      ← prometheus.Registry + all metric definitions
│       ├── sessions/
│       │   └── sessions.go      ← session lookup, creation, revocation
│       ├── auth/
│       │   ├── context.go       ← CustomerFromContext, StaffFromContext
│       │   └── permissions.go   ← rolePermissions map, HasPermission
│       ├── ratelimit/
│       │   ├── limiter.go       ← Limiter, LimitConfig, middleware
│       │   ├── store.go         ← Store interface
│       │   ├── memory.go        ← InMemoryStore
│       │   └── redis.go         ← RedisStore (migration path)
│       ├── shipping/
│       │   ├── provider.go      ← LabelProvider interface, LabelRequest, LabelResult
│       │   ├── shippo.go        ← ShippoProvider
│       │   └── easypost.go      ← EasyPostProvider
│       ├── email/
│       │   ├── provider.go      ← Sender interface, Message, TemplatedMessage, SendResult
│       │   ├── postmark.go      ← PostmarkSender (Postmark API)
│       │   └── test_sender.go   ← TestSender (captures emails for tests)
│       └── logging/
│           └── logging.go       ← logger setup, LoggerFromContext, field constants
│
│   ├── emailtemplates/            ← email rendering; imports nothing from this project
│   │   ├── renderer.go           ← Renderer, data structs, embed.FS, Render(name, data)
│   │   ├── renderer_test.go
│   │   ├── html/                 ← HTML email templates (inline CSS)
│   │   │   ├── order_confirm.html
│   │   │   ├── subscription_confirm.html
│   │   │   ├── invoice_sent.html
│   │   │   ├── magic_link.html
│   │   │   ├── wholesale_approved.html
│   │   │   └── wholesale_application.html
│   │   └── text/                 ← plain-text email templates
│   │       ├── order_confirm.txt
│   │       ├── subscription_confirm.txt
│   │       ├── invoice_sent.txt
│   │       ├── magic_link.txt
│   │       ├── wholesale_approved.txt
│   │       └── wholesale_application.txt
│
├── ui/                          ← Svelte checkout component (separate from internal/ui/)
│   └── checkout/
│       ├── src/
│       │   ├── App.svelte       ← root component, owns checkout state machine
│       │   ├── steps/
│       │   │   ├── Cart.svelte
│       │   │   ├── Address.svelte
│       │   │   ├── Payment.svelte   ← mounts Stripe Elements
│       │   │   └── Confirmation.svelte
│       │   └── lib/
│       │       ├── api.ts       ← typed fetch wrappers for the 4 Go JSON endpoints
│       │       └── stripe.ts    ← Stripe Elements initialisation
│       ├── package.json
│       ├── vite.config.ts       ← outputs to static/checkout/
│       └── tsconfig.json
│
├── static/                      ← compiled static assets, embedded via go:embed
│   └── checkout/
│       ├── checkout.js          ← compiled Svelte bundle (output of vite build)
│       └── checkout.css
│
├── db/
│   └── migrations/
│       ├── 001_customers.sql
│       ├── 002_sessions.sql
│       ├── 003_staff.sql
│       ├── 004_catalog.sql
│       ├── 005_pricing.sql
│       ├── 006_orders.sql
│       ├── 007_fulfillment.sql
│       ├── 008_subscriptions.sql
│       ├── 009_webhook_events.sql
│       ├── 010_audit_log.sql
│       └── 011_river.sql        ← River's own migration (generated by river migrate-get)
│
└── go.mod
```

---

## Import graph

The allowed import directions. An arrow means "may import":

```
cmd/server
    │
    ├──▶ internal/web
    ├──▶ internal/jobs
    ├──▶ internal/platform/*
    └──▶ internal/store

internal/web      ──▶ internal/app
                  ──▶ internal/platform/*
                  ──▶ internal/domain
                  ──▶ internal/ui

internal/ui       ──▶ internal/domain
                  (imports nothing else from this project —
                   templates receive domain types as parameters)

internal/jobs     ──▶ internal/app
                  ──▶ internal/platform/*
                  ──▶ internal/emailtemplates
                  ──▶ internal/domain

internal/emailtemplates ──▶ (nothing from this project — pure templates + stdlib)

internal/app      ──▶ internal/store
                  ──▶ internal/platform/audit
                  ──▶ internal/platform/metrics
                  ──▶ internal/domain

internal/store    ──▶ internal/domain

internal/platform ──▶ internal/domain  (audit only, for snapshot types)

internal/domain   ──▶ (nothing from this project)

ui/checkout/      ──▶ Go JSON endpoints only (runtime HTTP, not a Go import)
                  (compiled independently; output embedded in static/)
```

**The rule stated plainly:** dependencies flow inward. `domain/` is the center — nothing from this project flows into it. `store/` and `platform/` import only `domain/`. `app/` imports `store/` and `platform/`. `web/` and `jobs/` import `app/`. `ui/` imports only `domain/`. `cmd/server` wires everything together.

`ui/checkout/` (the Svelte component) is not a Go package — it has no Go import relationship. It communicates with the backend at runtime via HTTP. Its only compile-time relationship is that `vite build` outputs into `static/`, which `go:embed` picks up.

---

## What lives where — package by package

### `internal/domain/`

Pure Go types. No database logic, no HTTP logic, no business rules. The only imports are standard library types (`time`, `uuid`).

```go
// domain/order.go
package domain

type OrderStatus string
const (
    OrderStatusPending    OrderStatus = "pending"
    OrderStatusConfirmed  OrderStatus = "confirmed"
    OrderStatusComplete   OrderStatus = "complete"
    OrderStatusCancelled  OrderStatus = "cancelled"
)

type PaymentStatus string
const (
    PaymentStatusAwaiting  PaymentStatus = "awaiting"
    PaymentStatusCaptured  PaymentStatus = "captured"
    PaymentStatusRefunded  PaymentStatus = "refunded"
)

type Order struct {
    ID              uuid.UUID
    Number          string
    CustomerID      uuid.UUID
    Status          OrderStatus
    PaymentStatus   PaymentStatus
    FulfillmentStatus FulfillmentStatus
    Subtotal        int   // cents
    DiscountTotal   int
    ShippingTotal   int
    TaxTotal        int
    Total           int
    Currency        string
    CreatedAt       time.Time
    UpdatedAt       time.Time
}
```

Domain types are the shared vocabulary of the entire application. Because `domain/` imports nothing from the project, it can be imported by anyone without creating cycles.

**What does NOT go here:** business rules (`func (o *Order) CanRefund() bool` — that goes in `app/`), database queries, HTTP request/response types.

---

### `internal/store/`

All database access. One file per domain area. Every function accepts a `pgx.Tx` or `*pgxpool.Pool` — the store never manages transaction boundaries, it only executes queries within them.

```go
// store/orders.go
package store

type OrderStore struct {
    db *pgxpool.Pool
}

// Get returns an order scoped to a customer — enforces ownership.
// customerID is required; passing uuid.Nil is a programming error.
func (s *OrderStore) Get(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID) (*domain.Order, error)

// GetByID returns an order without customer scoping — for staff use only.
// Callers must ensure the requesting actor has staff permissions.
func (s *OrderStore) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Order, error)

// UpdateStatus updates order status fields and returns the updated order.
func (s *OrderStore) UpdateStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.OrderStatus) (*domain.Order, error)
```

The naming convention makes the scoping contract explicit: `Get` = customer-scoped, `GetByID` = staff-scoped. A code reviewer can immediately see when a staff-only function is being called and verify the handler has the right permission middleware.

**`store/db.go`** contains the pgx pool setup and transaction helpers:

```go
// store/db.go
package store

// Tx runs fn inside a transaction. Commits on success, rolls back on error or panic.
func Tx(ctx context.Context, db *pgxpool.Pool, fn func(pgx.Tx) error) error {
    tx, err := db.Begin(ctx)
    if err != nil { return err }
    defer tx.Rollback(ctx)
    if err := fn(tx); err != nil { return err }
    return tx.Commit(ctx)
}
```

This helper is used by handlers and workers — it keeps transaction boilerplate out of every call site.

---

### `internal/app/`

Business logic. Services own the rules: what constitutes a valid refund, what happens when a subscription renews, what side effects follow placing an order.

Each service receives its dependencies by injection — stores, the audit writer, the metrics registry, the River client. No service constructs its own database connection or opens its own transaction.

```go
// app/orders.go
package app

type OrderService struct {
    orders    *store.OrderStore
    payments  *store.PaymentStore
    audit     *audit.AuditWriter
    metrics   *metrics.Registry
    river     *river.Client
}

func (s *OrderService) Refund(
    ctx     context.Context,
    tx      pgx.Tx,
    orderID uuid.UUID,
    amount  int,
    reason  string,
    actor   domain.StaffActor,
) (*domain.Order, error) {
    // 1. Validate
    order, err := s.orders.GetByID(ctx, tx, orderID)
    if err != nil { return nil, err }
    if order.PaymentStatus != domain.PaymentStatusCaptured {
        return nil, ErrOrderNotRefundable
    }

    // 2. Domain write
    updated, err := s.orders.ApplyRefund(ctx, tx, orderID, amount)
    if err != nil { return nil, err }

    // 3. Audit — same tx
    s.audit.Record(ctx, tx, audit.AuditEntry{
        ActorType:    audit.ActorStaff,
        ActorID:      &actor.ID,
        ActorName:    actor.Name,
        Action:       audit.AuditOrderRefunded,
        ResourceType: "order",
        ResourceID:   orderID,
        After:        updated,
        Reason:       reason,
        Metadata:     map[string]any{"refund_amount": amount},
    })

    // 4. Enqueue notification — same tx
    s.river.InsertTx(ctx, tx, jobs.OrderRefundedArgs{OrderID: orderID}, nil)

    // 5. Metrics — outside tx, fire and forget
    s.metrics.OrderRefundsTotal.WithLabelValues(updated.Currency).Inc()

    return updated, nil
}
```

**Errors defined in `app/`** are sentinel values that handlers can inspect to produce the right HTTP status:

```go
// app/errors.go
package app

var (
    ErrNotFound           = errors.New("not found")
    ErrOrderNotRefundable = errors.New("order is not in a refundable state")
    ErrInvalidCredentials = errors.New("invalid credentials")
    ErrEmailNotVerified   = errors.New("email address not verified")
)
```

Handlers switch on these errors to produce 404, 422, 401, etc. The app layer never produces HTTP status codes directly.

---

### `internal/web/`

HTTP handlers and middleware. Handlers are thin — they parse the request, call a service, and render a response. No business logic lives here.

**`web/router.go`** registers all routes and applies middleware:

```go
// web/router.go
package web

func NewRouter(deps *Deps) http.Handler {
    mux := http.NewServeMux()

    // Middleware applied to all routes
    stack := alice.New(
        deps.Metrics.Middleware(),      // outermost — catches everything
        deps.Logging.Middleware(),
    )

    // Public routes — no session required
    mux.Handle("POST /auth/customer/login",
        deps.RateLimiter.Limit("customer.login.ip", ipKey)(
        deps.RateLimiter.Limit("customer.login.email", emailKey)(
            handleCustomerLogin(deps))))

    // Customer routes — session required
    customerAuth := alice.New(deps.Sessions.CustomerMiddleware())
    mux.Handle("GET /orders/{id}",
        customerAuth.Then(handleGetOrder(deps)))
    mux.Handle("POST /subscriptions/{id}/cancel",
        customerAuth.Then(handleCancelSubscription(deps)))

    // Staff routes — session + permission required
    staffAuth := alice.New(deps.Sessions.StaffMiddleware())
    mux.Handle("POST /admin/orders/{id}/refund",
        staffAuth.Append(
            deps.Auth.RequirePermission(auth.PermIssueRefunds),
        ).Then(handleIssueRefund(deps)))

    mux.Handle("GET /metrics", deps.Metrics.Handler()) // internal port only

    return stack.Then(mux)
}
```

**`web/respond.go`** centralizes response rendering and error translation:

```go
// web/respond.go
package web

func JSON(w http.ResponseWriter, status int, v any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(v)
}

// Error translates app-layer errors to HTTP responses.
func Error(w http.ResponseWriter, r *http.Request, err error) {
    switch {
    case errors.Is(err, app.ErrNotFound):
        JSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
    case errors.Is(err, app.ErrOrderNotRefundable):
        JSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
    case errors.Is(err, app.ErrInvalidCredentials):
        JSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
    default:
        LoggerFromContext(r.Context()).Error("unhandled error", "err", err)
        JSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
    }
}
```

A single `Error` function means the error-to-status mapping is defined once, not scattered across every handler.

**`web/respond.go`** also contains the templ render helper — a thin wrapper that sets `Content-Type: text/html` and streams the templ component output:

```go
func Render(w http.ResponseWriter, r *http.Request, component templ.Component) {
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    if err := component.Render(r.Context(), w); err != nil {
        slog.ErrorContext(r.Context(), "render failed", "err", err)
    }
}
```

Handlers never call `component.Render` directly — they call `web.Render`, which ensures consistent content type and error handling.

---

### `internal/ui/`

All templ templates. Imports `domain/` for the types it renders — nothing else from this project. Templates are pure view logic: they receive data, they render HTML, they do nothing else.

The package is split into three sub-directories:

**`ui/layouts/`** — the outer HTML shells. Every page passes through a layout. Layouts own the `<html>`, `<head>`, navigation, and footer. Storefront and admin have separate layouts because they have different navigation, CSS, and authentication context.

```
// ui/layouts/storefront.templ
templ StorefrontLayout(title string, customer *domain.Customer) {
    <!DOCTYPE html>
    <html lang="en">
    <head>
        <title>{ title }</title>
        <link rel="stylesheet" href="/static/app.css"/>
    </head>
    <body>
        @storefrontNav(customer)
        { children... }
        @storefrontFooter()
    </body>
    </html>
}
```

**`ui/storefront/`** — customer-facing page templates. One file per domain area. Each file contains the page-level template plus any sub-components used only by that page.

```
// ui/storefront/catalog.templ
templ ProductListPage(products []domain.Product, pagination domain.Pagination) {
    @layouts.StorefrontLayout("Shop", nil) {
        <div class="product-grid">
            for _, p := range products {
                @productCard(p)
            }
        </div>
        @components.Pagination(pagination)
    }
}

// productCard is a private component — lowercase, only used within this file
templ productCard(p domain.Product) {
    <a href={ templ.URL("/products/" + p.Slug) }>
        ...
    </a>
}
```

**`ui/storefront/checkout.templ`** is the page shell that mounts the Svelte component. It renders server-side (session, cart summary for no-JS fallback) then hands off to the Svelte bundle:

```
// ui/storefront/checkout.templ
templ CheckoutPage(cart *domain.Cart) {
    @layouts.StorefrontLayout("Checkout", nil) {
        // Mount point for the Svelte component
        <div id="checkout-app" data-cart-id={ cart.ID.String() }></div>

        // Embed the compiled Svelte bundle
        <script type="module" src="/static/checkout/checkout.js"></script>
    }
}
```

The `data-cart-id` attribute passes server-side context to the Svelte component without an extra API call on mount.

**`ui/admin/`** — staff-facing page templates. Same pattern. Admin templates are more data-dense — tables, status badges, action buttons.

**`ui/components/`** — shared components used by both storefront and admin: pagination, flash messages, form fields, tables. These are small, stateless, and have no page-level context.

A key property of `internal/ui/`: **handlers pass domain types directly to templates**. There are no separate "view model" or "DTO" structs between the handler and the template. If a template needs a subset of an order's fields, it receives `*domain.Order` and uses what it needs. This keeps the handler thin and avoids a parallel type hierarchy.

---

### `ui/checkout/` and `static/`

The Svelte checkout component lives outside `internal/` because it is not a Go package — it is a JavaScript project that happens to live in the same repository.

**Build relationship:**

```
ui/checkout/src/   →   vite build   →   static/checkout/checkout.js
                                         static/checkout/checkout.css
```

The `static/` directory is embedded into the Go binary at compile time:

```go
// cmd/server/main.go
//go:embed static
var staticFiles embed.FS

// In router setup:
mux.Handle("/static/", http.FileServerFS(staticFiles))
```

This means the production binary is fully self-contained — no separate static file server, no CDN required for the checkout bundle. `vite build` must run before `go build`. A `Makefile` target handles this:

```makefile
.PHONY: build
build:
    cd ui/checkout && npm run build
    go build ./cmd/server
```

**The four Go endpoints the Svelte component calls:**

```
GET  /api/checkout/cart              ← current cart contents + totals
POST /api/checkout/address           ← validate + save shipping address
POST /api/checkout/payment-intent    ← create Stripe PaymentIntent, return client_secret
POST /api/checkout/confirm           ← finalize order after Stripe confirms payment
```

All four live in `web/checkout.go`. They are JSON endpoints — `respond.JSON` not `web.Render`. They require a customer session (guest or authenticated). They are the only JSON endpoints on the storefront; everything else is server-rendered HTML.

**The Svelte component receives its initial state** from `data-*` attributes on the mount point, not from an API call on load. The checkout page handler pre-renders the cart ID and any server-side validation state into the HTML. This avoids a loading flash on mount and keeps the initial render fast.

---

### `internal/jobs/`

River worker definitions. One file per job type. Workers call app-layer services — they do not touch the database directly.

```go
// jobs/subscription_renewal.go
package jobs

type SubscriptionRenewalArgs struct {
    SubscriptionID uuid.UUID `json:"subscription_id"`
}

func (SubscriptionRenewalArgs) Kind() string { return "subscription_renewal" }

type SubscriptionRenewalWorker struct {
    subscriptions *app.SubscriptionService
    db            *pgxpool.Pool
}

func (w *SubscriptionRenewalWorker) Work(ctx context.Context, job *river.Job[SubscriptionRenewalArgs]) error {
    return store.Tx(ctx, w.db, func(tx pgx.Tx) error {
        return w.subscriptions.Renew(ctx, tx, job.Args.SubscriptionID, domain.SystemActor)
    })
}
```

Workers open a transaction via `store.Tx`, call a service method, and let the service handle audit logging and metric increments. The worker itself is thin — it's the boundary between River's job execution and the application's business logic.

**`jobs/workers.go`** registers all workers with River:

```go
// jobs/workers.go
package jobs

func RegisterWorkers(workers *river.Workers, deps *Deps) {
    river.AddWorker(workers, &SubscriptionRenewalWorker{deps.Subscriptions, deps.DB})
    river.AddWorker(workers, &PaymentRetryWorker{deps.Payments, deps.DB})
    river.AddWorker(workers, &OrderConfirmationWorker{deps.Notifications, deps.DB})
    river.AddWorker(workers, &CartExpiryWorker{deps.Carts, deps.DB})
    river.AddWorker(workers, &SessionPruneWorker{deps.DB})
}
```

---

### `internal/platform/`

The eight infrastructure concerns, each in its own sub-package. These packages are imported by `app/`, `web/`, and `jobs/` — never the other way around.

```
platform/
  audit/        ← AuditWriter, AuditEntry, action constants
  metrics/      ← Registry, all metric definitions, HTTP middleware
  sessions/     ← session creation, lookup, expiry, middleware
  auth/         ← context helpers, permission map, RequirePermission middleware
  ratelimit/    ← Limiter, Store interface, InMemoryStore, RedisStore
  shipping/     ← LabelProvider interface, EasyPostProvider
  email/        ← Sender interface, PostmarkSender, TestSender
  logging/      ← slog setup, LoggerFromContext, field name constants
```

Each sub-package has a single coherent responsibility. `platform/auth/` knows about permissions and roles. `platform/sessions/` knows about token hashing and database lookups. They do not know about each other — the composition happens in `web/middleware.go` and `web/router.go` where the middleware chain is assembled.

---

### `cmd/server/main.go`

Wires everything together. The only job of `main.go` is construction and startup — no business logic, no routing logic, no SQL.

```go
// cmd/server/main.go
package main

func main() {
    ctx := context.Background()

    // Config from environment
    cfg := config.MustLoad()

    // Infrastructure
    db       := mustConnectDB(cfg.DatabaseURL)
    logger   := logging.New(cfg.Env)
    reg      := metrics.NewRegistry()
    auditor  := audit.NewWriter()
    limiter  := ratelimit.NewLimiter(ratelimit.NewInMemoryStore())

    // Stores
    stores := &store.Stores{
        Orders:        store.NewOrderStore(db),
        Customers:     store.NewCustomerStore(db),
        Catalog:       store.NewCatalogStore(db),
        Subscriptions: store.NewSubscriptionStore(db),
        Sessions:      store.NewSessionStore(db),
        Webhooks:      store.NewWebhookStore(db),
    }

    // River client — constructed before services (services need it for InsertTx)
    workers  := river.NewWorkers()
    riverCfg := &river.Config{
        Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 50}},
        Workers: workers,
    }
    riverClient, err := river.NewClient(riverpgxv5.New(db), riverCfg)
    if err != nil { log.Fatal(err) }

    // Services
    services := &app.Services{
        Orders:        app.NewOrderService(stores.Orders, stores.Payments, auditor, reg, riverClient),
        Customers:     app.NewCustomerService(stores.Customers, auditor, reg),
        Subscriptions: app.NewSubscriptionService(stores.Subscriptions, stores.Orders, auditor, reg, riverClient),
        Auth:          app.NewAuthService(stores.Customers, stores.Staff, stores.Sessions),
    }

    // Register River workers (needs services to be constructed first)
    jobs.RegisterWorkers(workers, &jobs.Deps{
        Subscriptions: services.Subscriptions,
        DB:            db,
        // ...
    })

    // HTTP server
    router := web.NewRouter(&web.Deps{
        Services:   services,
        Metrics:    reg,
        Logging:    logger,
        RateLimiter: limiter,
        Sessions:   stores.Sessions,
    })
    httpServer := &http.Server{
        Addr:    cfg.Addr,
        Handler: router,
    }

    // Start River
    if err := riverClient.Start(ctx); err != nil { log.Fatal(err) }

    // Start HTTP
    go func() {
        if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
            log.Fatal(err)
        }
    }()

    // Graceful shutdown on SIGINT/SIGTERM
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit

    shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()

    riverClient.Stop(shutdownCtx)
    httpServer.Shutdown(shutdownCtx)
}
```

`main.go` reads like a wiring diagram. Every dependency is explicit — there is no global state, no `init()` magic, no service locator. If a dependency is missing, the code does not compile.

---

### `db/migrations/`

Plain SQL files, numbered sequentially. Managed by your migration tool of choice (Goose works well with this layout). River's migrations live here too — generated via `river migrate-get --all` and committed alongside application migrations so schema changes are in one place.

```
001_customers.sql          ← customers, customer_groups, addresses
002_sessions.sql           ← sessions, email_verifications, reset_tokens
003_staff.sql              ← staff
004_catalog.sql            ← products, variants, options, taxons, media
005_pricing.sql            ← price_sets, prices, price_lists
006_orders.sql             ← carts, orders, line_items, adjustments
007_fulfillment.sql        ← stock_locations, inventory_items, stock_levels, fulfillments
008_subscriptions.sql      ← subscription_plans, subscriptions, subscription_orders
009_webhook_events.sql     ← webhook_events
010_audit_log.sql          ← audit_log
011_shipping.sql           ← shipping_config, shipments
012_tax.sql                ← tax_exempt columns on customers + orders, stripe_tax_id on orders
013_discounts.sql          ← discounts, coupon_codes, order_discounts
014_river.sql              ← River's tables (river_job, river_leader, river_queue)
```

---

## The dependency rule, restated

Every import decision follows one rule: **dependencies point inward, never outward.**

```
cmd/         can import anything
web/         can import app/, platform/, domain/, ui/
ui/          can import domain/ only
jobs/        can import app/, platform/, domain/
app/         can import store/, platform/, domain/
store/       can import domain/
platform/    can import domain/ (audit only)
domain/      imports nothing from this project

ui/checkout/ is not a Go package — it has no Go import relationship.
             It communicates with web/ at runtime via HTTP only.
```

If you find yourself wanting to import `web/` from `app/`, or `app/` from `store/`, something has leaked to the wrong layer. The fix is always to move the thing being imported inward — usually to `domain/` if it's a type, or to a new `platform/` sub-package if it's infrastructure.

If you find yourself wanting to import `app/` or `platform/` from `ui/`, the template is doing too much — move that logic to the handler that calls the template, and pass the result in as a parameter.
