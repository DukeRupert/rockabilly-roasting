# Lean Commerce — Testing Plan

The goal is production stability before deployment. This document defines the testing strategy, prioritizes what must be in place early, and defers what can wait until the codebase matures.

The plan is organized into four phases. Phase 1 is required before any other tests are worth writing. Phases 2 and 3 are required before deployment. Phase 4 is post-launch.

---

## The four layers

```
Layer 1: Unit tests          → internal/domain/        (milliseconds, no I/O)
Layer 2: Integration tests   → internal/store/ + app/  (seconds, real database)
Layer 3: Handler tests       → internal/web/           (seconds, real database + router)
Layer 4: Load tests          → k6 against staging      (minutes, production-like data)
```

Contract tests are not a separate layer — they are a property of Layer 3 handler tests that lock the JSON shape of the Svelte checkout API endpoints.

---

## Phase 1 — Foundation (do this first, before any other tests)

**Nothing else is worth building until this exists.**

### 1.1 Test database helper

Every integration and handler test needs a real PostgreSQL instance with the schema applied. This helper provides it:

```go
// internal/testutil/db.go

package testutil

// NewTestDB spins up a throwaway Postgres container, runs all migrations,
// and returns a connection pool. The container is terminated when the test ends.
func NewTestDB(t *testing.T) *pgxpool.Pool {
    t.Helper()
    ctx := context.Background()

    container, err := postgres.RunContainer(ctx,
        testcontainers.WithImage("postgres:16"),
        postgres.WithDatabase("hiri_test"),
        postgres.WithUsername("test"),
        postgres.WithPassword("test"),
    )
    require.NoError(t, err)
    t.Cleanup(func() { container.Terminate(ctx) })

    connStr, err := container.ConnectionString(ctx, "sslmode=disable")
    require.NoError(t, err)

    pool, err := pgxpool.New(ctx, connStr)
    require.NoError(t, err)

    err = goose.Up(pool, "../../db/migrations")
    require.NoError(t, err)

    return pool
}

// NewTestTx returns a transaction that automatically rolls back when the test ends.
// Use this in every integration test — no cleanup, no truncation, perfect isolation.
func NewTestTx(t *testing.T, db *pgxpool.Pool) pgx.Tx {
    t.Helper()
    tx, err := db.Begin(context.Background())
    require.NoError(t, err)
    t.Cleanup(func() { tx.Rollback(context.Background()) })
    return tx
}
```

The transaction rollback pattern is the key design decision. Each test runs inside a transaction that is always rolled back — no matter what the test does, the database is clean for the next test. No truncation scripts, no test ordering dependencies, no shared state.

**Required dependencies:**
- `github.com/testcontainers/testcontainers-go` — Postgres container management
- `github.com/pressly/goose/v3` — migration runner (same tool used in production)

### 1.2 Fixture builders

Every test needs data. If seeding data is painful, tests do not get written. Fixture builders hide the plumbing and let each test declare only what it cares about:

```go
// internal/testutil/fixtures.go

// CreateCustomer inserts a customer with sensible defaults.
// Override specific fields with functional options.
func CreateCustomer(t *testing.T, tx pgx.Tx, opts ...CustomerOption) *domain.Customer {
    t.Helper()
    cfg := &customerConfig{
        email:     fmt.Sprintf("test-%s@example.com", uuid.New().String()[:8]),
        taxExempt: false,
    }
    for _, opt := range opts { opt(cfg) }
    // ... sqlc insert, convert to domain type, return
}

func WithTaxExempt(reason string) CustomerOption {
    return func(c *customerConfig) {
        c.taxExempt = true
        c.taxExemptReason = &reason
    }
}

// CreateOrder inserts a complete valid order.
func CreateOrder(t *testing.T, tx pgx.Tx, customerID uuid.UUID, opts ...OrderOption) *domain.Order {
    t.Helper()
    cfg := &orderConfig{
        status:        domain.OrderStatusConfirmed,
        paymentStatus: domain.PaymentStatusCaptured,
        subtotal:      10000,
        shippingTotal: 800,
        taxTotal:      880,
        total:         11680,
        currency:      "usd",
    }
    for _, opt := range opts { opt(cfg) }
    // ...
}

func WithPaymentStatus(s domain.PaymentStatus) OrderOption {
    return func(o *orderConfig) { o.paymentStatus = s }
}

func WithSubtotal(cents int) OrderOption {
    return func(o *orderConfig) { o.subtotal = cents }
}

// StaffActor returns a domain.StaffActor for use in service method calls.
func StaffActor(role domain.StaffRole) domain.StaffActor {
    return domain.StaffActor{
        ID:   uuid.New(),
        Name: "Test Staff",
        Role: role,
    }
}
```

Build fixture functions for every entity as you write tests for that entity — not all upfront. The pattern is what matters; the functions accumulate over time.

### 1.3 Assertion helpers

Common assertions that appear in every service test should be helpers, not repeated code:

```go
// internal/testutil/assertions.go

// LastAuditEntry returns the most recent audit log entry for a resource.
// Fails the test if no entry exists.
func LastAuditEntry(t *testing.T, tx pgx.Tx, resourceType string, resourceID uuid.UUID) *domain.AuditEntry {
    t.Helper()
    // ... query audit_log ORDER BY occurred_at DESC LIMIT 1
    require.NotNil(t, entry, "expected audit entry for %s %s", resourceType, resourceID)
    return entry
}

// PendingRiverJobs returns all River jobs of a given kind enqueued in this transaction.
func PendingRiverJobs(t *testing.T, tx pgx.Tx, kind string) []river.Job {
    t.Helper()
    // ... query river_job WHERE kind = $1 AND state = 'available'
    return jobs
}

// AssertNoAuditEntry fails the test if an audit entry exists for the resource.
// Use this to verify that failed operations produce no audit trail.
func AssertNoAuditEntry(t *testing.T, tx pgx.Tx, resourceType string, resourceID uuid.UUID) {
    t.Helper()
    // ...
    assert.Nil(t, entry, "expected no audit entry for %s %s", resourceType, resourceID)
}
```

**Phase 1 is complete when:** you can write `db := testutil.NewTestDB(t)`, seed a customer and an order in three lines, and roll back cleanly. Everything else builds on this.

---

## Phase 2 — Critical path tests (required before deployment)

These are the tests that, if absent, mean you cannot trust the system in production.

### 2.1 Domain unit tests — `internal/domain/`

No database required. Fast, comprehensive, the cheapest correctness guarantee available.

**`internal/domain/discount_test.go`** — test every branch of `Discount.Evaluate`:

| Scenario | Expected outcome |
|---|---|
| Inactive discount | `ErrDiscountInactive` |
| Expired discount | `ErrDiscountExpired` |
| Not yet active | `ErrDiscountNotYetActive` |
| Minimum order not met | `ErrMinimumOrderNotMet` |
| Already used by customer | `ErrAlreadyUsedByCustomer` |
| Coupon already redeemed | `ErrCouponAlreadyRedeemed` |
| Coupon issued to different customer | `ErrCouponNotForCustomer` |
| Percentage discount — correct calculation | `amount = subtotal * value / 100` |
| Fixed amount — capped at subtotal | `amount = min(value, subtotal)` |
| Free shipping — amount equals shipping total | `amount = shippingTotal, freeShipping = true` |
| Expiry boundary — exactly at expiry | `ErrDiscountExpired` |
| Expiry boundary — one second before | success |

**`internal/domain/shipping_test.go`** — test `ShippingConfig.Calculate`:

| Scenario | Expected outcome |
|---|---|
| Subtotal below threshold | flat rate returned |
| Subtotal exactly at threshold | zero returned |
| Subtotal above threshold | zero returned |
| Threshold is nil | always flat rate |
| Flat rate is zero, no threshold | always zero |

**`internal/domain/order_test.go`** — test state machine transitions and total invariants:

- Every valid status transition succeeds
- Every invalid transition returns an error
- `total == subtotal - discountTotal + shippingTotal + taxTotal` holds across all combinations
- Tax-exempt order: `taxTotal == 0` and total formula still holds
- Free shipping discount: `shippingTotal == 0`, `discountTotal == 0` (free shipping is not in discountTotal)

### 2.2 Service integration tests — `internal/app/`

These tests exercise the full service method including audit records, River job enqueueing, and database side effects. Use `NewTestDB` + `NewTestTx`.

**`internal/app/orders_test.go`**

```
TestOrderService_Refund
  ✓ refunds a captured order — payment status updated, audit record written, River job enqueued
  ✓ returns ErrOrderNotRefundable for an order with payment_status = awaiting
  ✓ returns ErrOrderNotRefundable for an already-refunded order
  ✓ failed refund writes no audit record and enqueues no job
  ✓ refund amount is frozen correctly on order_discounts

TestOrderService_PlaceOrder
  ✓ standard order — totals correct, tax applied, shipping applied
  ✓ tax-exempt customer — tax_total = 0, tax_exempt = true frozen on order
  ✓ order with percentage discount — discount_total correct, total correct
  ✓ order with free shipping discount — shipping_total = 0, discount_total = 0
  ✓ order with coupon code — coupon marked redeemed in same transaction
  ✓ order placement enqueues order confirmation River job
```

**`internal/app/checkout_test.go`**

```
TestCheckoutService_ApplyCouponCode
  ✓ valid code returns AppliedDiscount
  ✓ expired code returns ErrDiscountExpired
  ✓ code issued to different customer returns ErrCouponNotForCustomer
  ✓ already-redeemed code returns ErrCouponAlreadyRedeemed
  ✓ customer who already used this discount returns ErrAlreadyUsedByCustomer

TestCheckoutService_CouponRedemptionRace
  ✓ two concurrent transactions attempting to redeem the same code — exactly one succeeds
  (This is the most important test in the checkout suite)
```

The race test requires two real database connections attempting the update simultaneously:

```go
func TestCouponRedemptionRace(t *testing.T) {
    db := testutil.NewTestDB(t)

    // Seed a single-use coupon
    discount := testutil.CreateDiscount(t, db)
    coupon   := testutil.CreateCouponCode(t, db, discount.ID)

    results := make([]error, 2)
    var wg sync.WaitGroup

    for i := 0; i < 2; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            tx, _ := db.Begin(context.Background())
            defer tx.Rollback(context.Background())

            err := store.MarkCouponRedeemed(context.Background(), tx, coupon.ID, uuid.New())
            results[idx] = err
            if err == nil { tx.Commit(context.Background()) }
        }(i)
    }
    wg.Wait()

    successes := 0
    for _, err := range results {
        if err == nil { successes++ }
    }
    assert.Equal(t, 1, successes, "exactly one redemption should succeed")
}
```

**`internal/app/subscriptions_test.go`**

```
TestSubscriptionService_Renew
  ✓ creates a new order with correct totals
  ✓ updates next_order_at on the subscription
  ✓ writes audit record with SystemActor
  ✓ enqueues payment capture River job
  ✓ idempotent — running Renew twice does not create two orders
```

**`internal/app/customers_test.go`**

```
TestCustomerService_GrantTaxExemption
  ✓ sets tax_exempt = true and records reason
  ✓ writes audit record with action customer.tax_exemption_granted
  ✓ reason is present in audit entry

TestCustomerService_RevokeTaxExemption
  ✓ sets tax_exempt = false, clears reason
  ✓ writes audit record with action customer.tax_exemption_revoked
```

### 2.3 Webhook handler tests — `internal/web/webhooks_test.go`

Webhooks are where money errors happen. Test these before anything else in the handler layer.

```
TestWebhookHandler_Stripe
  ✓ rejects request with invalid signature — 401, no database write
  ✓ rejects request with valid signature but expired timestamp — 401
  ✓ accepts valid request — 200, webhook_events row created, River job enqueued
  ✓ duplicate delivery (same event_id) — 200, no new row, no new job
  ✓ concurrent duplicate deliveries — exactly one row and one job created
  ✓ valid request with database failure — 500, no partial state
```

The concurrent duplicate test is critical — it validates that the `ON CONFLICT DO NOTHING` pattern holds under real concurrency, not just sequential calls.

### 2.4 Auth and permission handler tests — `internal/web/auth_test.go`

Systematic coverage of every protection boundary. One test per route group × actor type combination.

```
TestAuthMiddleware
  ✓ request with no session cookie → 401
  ✓ request with expired session → 401
  ✓ request with invalid token → 401
  ✓ customer token on staff route → 401
  ✓ valid customer session on customer route → passes through

TestPermissionMiddleware
  ✓ admin can access all staff routes
  ✓ fulfillment staff can access fulfillment routes
  ✓ fulfillment staff cannot access refund routes → 403
  ✓ finance staff can access refund routes
  ✓ support staff cannot modify pricing → 403

TestCustomerOwnership
  ✓ customer can access their own order → 200
  ✓ customer cannot access another customer's order → 404 (not 403)
  ✓ customer cannot access another customer's subscription → 404
```

---

## Phase 3 — Coverage hardening (complete before first public launch)

Phase 3 fills in the remaining handler tests, adds contract tests for the Svelte API, and runs the first load tests. Less urgent than Phase 2 but required before public traffic.

### 3.1 Remaining handler tests

Once the auth and webhook handlers are covered, extend to business operation handlers:

```
Orders:
  ✓ GET /orders/{id} — customer can retrieve own order
  ✓ GET /orders/{id} — returns correct totals including discount and tax
  ✓ POST /admin/orders/{id}/refund — success, 403 without permission, 422 on invalid state

Subscriptions:
  ✓ POST /subscriptions/{id}/cancel — customer can cancel own subscription
  ✓ POST /subscriptions/{id}/pause — validates pause is allowed in current state

Checkout API (contract tests — lock the JSON shape):
  ✓ GET /api/checkout/cart — response matches CartResponse shape
  ✓ POST /api/checkout/address — validates required fields, returns 422 on invalid address
  ✓ POST /api/checkout/payment-intent — returns client_secret field
  ✓ POST /api/checkout/confirm — creates order, returns OrderResponse shape
  ✓ POST /api/checkout/coupon — returns AppliedDiscountResponse or error shape
```

For contract tests, serialize the response body to a golden JSON file on first run and compare on subsequent runs:

```go
func assertResponseShape(t *testing.T, body []byte, goldenFile string) {
    t.Helper()
    goldenPath := filepath.Join("testdata", goldenFile)

    if *update { // -update flag regenerates golden files
        os.WriteFile(goldenPath, body, 0644)
        return
    }

    golden, err := os.ReadFile(goldenPath)
    require.NoError(t, err)

    // Compare JSON structure (keys present), not values
    assertSameJSONShape(t, golden, body)
}
```

Golden files are committed to the repository. A response shape change fails CI and requires a deliberate `go test -update` to accept it.

### 3.2 Background job tests — `internal/jobs/`

Workers are thin by design — they open a transaction and call a service method. The service method is already tested. What needs testing at the job layer is the wiring:

```
TestSubscriptionRenewalWorker
  ✓ calls SubscriptionService.Renew with correct args
  ✓ uses SystemActor (not a fabricated staff actor)
  ✓ job is idempotent — running twice produces one order

TestPaymentRetryWorker
  ✓ retries payment for a failed subscription
  ✓ marks subscription as past_due after max retries exceeded
  ✓ dead-letters the job after max attempts

TestSessionPruneWorker
  ✓ deletes expired sessions
  ✓ does not delete active sessions
```

### 3.3 Load tests — k6

Run against a staging instance seeded with production-like data volumes (10,000 customers, 50,000 orders, 500 active subscriptions).

**Scenario 1 — Storefront browse (`k6/scenarios/storefront_browse.js`)**
500 concurrent users browsing catalog, viewing product pages, adding to cart.
Pass criteria: p99 latency < 200ms, zero 5xx responses.

**Scenario 2 — Checkout flow (`k6/scenarios/checkout_flow.js`)**
50 concurrent customers completing full checkout including coupon code application.
Pass criteria: p99 latency < 1s, all orders created with correct totals, zero 5xx.

**Scenario 3 — Subscription renewal spike (`k6/scenarios/subscription_renewal_spike.js`)**
Simulate River processing 1,000 subscription renewals simultaneously (as would happen after a scheduled task fires).
Pass criteria: all renewals complete within 5 minutes, no duplicate orders, job queue depth returns to zero, database connection pool not exhausted.

**Scenario 4 — Coupon race (`k6/scenarios/coupon_race.js`)**
100 concurrent requests attempting to redeem the same single-use coupon code.
Pass criteria: exactly 1 redemption succeeds, 99 return `ErrCouponAlreadyRedeemed`, no 5xx responses.

This scenario is the load test equivalent of the unit-level race test. If it fails, the optimistic locking is not working correctly under real HTTP concurrency.

---

## Phase 4 — Post-launch (after stable production traffic exists)

These are valuable but not blocking for launch. Add them once you have real traffic patterns to learn from.

### 4.1 Smoke tests

A small suite of end-to-end tests that run against production (or a production mirror) after every deployment:

```
✓ homepage loads
✓ product page renders with correct price
✓ customer can log in
✓ customer can view order history
✓ admin can log in
✓ /metrics endpoint is reachable on internal port
```

These are not comprehensive — they are a deployment health check. If they fail, the deployment is rolled back automatically.

### 4.2 Mutation testing

Once unit test coverage is high, run `go-mutesting` to verify the tests actually catch bugs rather than just executing code. Mutation testing introduces small code changes (flip a `>` to `>=`, remove a condition) and verifies the test suite catches them. Low mutation score means tests that pass without actually asserting anything meaningful.

Start with `internal/domain/` — this is where mutation testing pays off most for the cost.

### 4.3 Chaos testing

Inject failures into the staging environment:
- Kill the database mid-request — does the application recover gracefully?
- Introduce 500ms latency on Stripe API calls — does checkout degrade gracefully?
- Kill River workers mid-job — do jobs resume correctly or are they stuck?

These are manual experiments, not automated tests. Run them once before launch and again after major infrastructure changes.

---

## Test file organization

```
internal/
  domain/
    discount_test.go
    shipping_test.go
    order_test.go
    subscription_test.go
  store/
    orders_test.go
    discounts_test.go        ← includes concurrent redemption test
    webhooks_test.go         ← includes concurrent idempotency test
  app/
    orders_test.go
    checkout_test.go         ← includes coupon race test
    subscriptions_test.go
    customers_test.go
  web/
    webhooks_test.go         ← Phase 2 priority
    auth_test.go             ← Phase 2 priority
    orders_test.go
    checkout_test.go         ← includes contract/golden file tests
    testdata/
      cart_response.json     ← golden files for contract tests
      order_response.json
      applied_discount_response.json
  testutil/
    db.go                    ← NewTestDB, NewTestTx
    fixtures.go              ← CreateCustomer, CreateOrder, CreateDiscount, etc.
    assertions.go            ← LastAuditEntry, PendingRiverJobs, AssertNoAuditEntry
    golden.go                ← assertResponseShape helper

k6/
  scenarios/
    storefront_browse.js
    checkout_flow.js
    subscription_renewal_spike.js
    coupon_race.js
  lib/
    helpers.js               ← shared k6 utilities
```

---

## CI pipeline

```yaml
# Runs on every pull request — must pass to merge
test-unit:
  runs-on: ubuntu-latest
  steps:
    - go test ./internal/domain/...
  # Target: < 10 seconds

# Runs on every pull request — must pass to merge
test-integration:
  runs-on: ubuntu-latest
  services:
    postgres: { image: postgres:16 }
  steps:
    - go test ./internal/store/... ./internal/app/... ./internal/web/...
  # Target: < 2 minutes

# Runs on merge to main only — does not block merge, but alerts on failure
test-load:
  runs-on: ubuntu-latest
  if: github.ref == 'refs/heads/main'
  steps:
    - deploy to staging
    - k6 run k6/scenarios/coupon_race.js      # highest priority
    - k6 run k6/scenarios/checkout_flow.js
    - k6 run k6/scenarios/subscription_renewal_spike.js
    - k6 run k6/scenarios/storefront_browse.js
```

---

## Priority summary

| Priority | What | Why |
|---|---|---|
| **Do first** | `testutil/db.go`, `testutil/fixtures.go`, `testutil/assertions.go` | Everything else depends on this |
| **Phase 2 — before deployment** | Domain unit tests | Cheapest correctness guarantee; catches logic bugs early |
| **Phase 2 — before deployment** | `app/checkout_test.go` coupon race test | The one concurrency problem that causes real money errors |
| **Phase 2 — before deployment** | `app/orders_test.go` — place order, refund | Core revenue path; must be correct |
| **Phase 2 — before deployment** | `web/webhooks_test.go` — idempotency | Duplicate webhook = double charge or double fulfillment |
| **Phase 2 — before deployment** | `web/auth_test.go` — permission boundaries | Every missing permission check is a security hole |
| **Phase 3 — before public launch** | Contract/golden file tests for checkout API | Svelte component will break silently without these |
| **Phase 3 — before public launch** | k6 coupon race scenario | Load-level validation of optimistic locking |
| **Phase 3 — before public launch** | k6 subscription renewal spike | Validates River under realistic job volume |
| **Phase 4 — post launch** | Smoke tests | Deployment health check |
| **Phase 4 — post launch** | Mutation testing | Verifies test suite quality |
| **Phase 4 — post launch** | Chaos testing | Infrastructure resilience |