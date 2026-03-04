# Lean Commerce — Observability: Metrics & Structured Logging

Stack: **Prometheus** (metrics collection) + **Grafana** (dashboards + alerting) + **Loki** (log aggregation). All self-hosted. The application emits metrics via a `/metrics` endpoint and writes structured logs to stdout — the host's log collector (Promtail or Alloy) ships them to Loki.

Two questions this system must answer:
1. **Is the system healthy right now?** (errors, latency, uptime)
2. **What is the business doing?** (orders, renewals, revenue)

These are different data sources that complement each other. Metrics answer "how many / how fast / how often." Logs answer "what exactly happened and why."

---

## Part 1: Structured Logging

### Foundation: `slog`

Use Go's standard `log/slog` package (available since Go 1.21). It supports structured key-value output, pluggable handlers, and level filtering with no external dependency. For production, configure a JSON handler writing to stdout:

```go
// cmd/server/main.go
func initLogger(env string) *slog.Logger {
    level := slog.LevelInfo
    if env == "development" {
        level = slog.LevelDebug
    }

    handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
        Level: level,
        // Replace the default "time" key with "ts" for Loki compatibility
        ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
            if a.Key == slog.TimeKey {
                a.Key = "ts"
            }
            return a
        },
    })
    return slog.New(handler)
}
```

Stdout → Promtail/Alloy → Loki. No file rotation, no log library, no external dependency. The host process is responsible for log shipping, not the application.

---

### Standard fields

Every log line must carry a consistent base set of fields. This is what makes Loki queries useful — you can filter by `request_id` across all lines emitted during one request, or by `actor_id` to see everything one staff member triggered.

```go
// Standard fields present on every log line
const (
    FieldRequestID   = "request_id"    // unique per HTTP request
    FieldActorID     = "actor_id"      // authenticated user/staff ID
    FieldActorType   = "actor_type"    // "customer" | "staff" | "system"
    FieldMethod      = "method"        // HTTP method
    FieldPath        = "path"          // URL path — never include query params
    FieldStatus      = "status"        // HTTP response status code
    FieldDurationMS  = "duration_ms"   // request processing time
    FieldService     = "service"       // "lean-commerce" — useful in multi-app Loki
    FieldEnv         = "env"           // "production" | "staging"
)

// Business event fields — added on top of standard fields
const (
    FieldEvent        = "event"         // namespaced: "order.placed", "payment.captured"
    FieldResourceType = "resource_type"
    FieldResourceID   = "resource_id"
    FieldOrderID      = "order_id"
    FieldCustomerID   = "customer_id"
    FieldAmount       = "amount"        // always in cents
    FieldCurrency     = "currency"
)
```

---

### Request-scoped logger via context

The logger for a request is built once in middleware, enriched with request-scoped fields, and passed through context. Downstream handlers and services pull it from context — they never construct their own logger.

```go
type contextKey string
const loggerKey contextKey = "logger"

// LoggerFromContext retrieves the request-scoped logger.
// Falls back to the global logger if none is set.
func LoggerFromContext(ctx context.Context) *slog.Logger {
    if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
        return l
    }
    return slog.Default()
}

// withLogger stores a logger in context.
func withLogger(ctx context.Context, l *slog.Logger) context.Context {
    return context.WithValue(ctx, loggerKey, l)
}
```

The logging middleware wraps every request:

```go
func LoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()
            requestID := r.Header.Get("X-Request-ID")
            if requestID == "" {
                requestID = uuid.New().String()
            }

            // Build request-scoped logger with base fields
            reqLogger := logger.With(
                FieldRequestID, requestID,
                FieldMethod,    r.Method,
                FieldPath,      r.URL.Path,  // NOT r.URL.String() — no query params
            )

            // Inject into context
            ctx := withLogger(r.Context(), reqLogger)

            // Inject request ID into response headers for client correlation
            w.Header().Set("X-Request-ID", requestID)

            // Wrap ResponseWriter to capture status code
            rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
            next.ServeHTTP(rw, r.WithContext(ctx))

            // Emit request log after handler completes
            reqLogger.Info("request",
                FieldStatus,     rw.status,
                FieldDurationMS, time.Since(start).Milliseconds(),
            )
        })
    }
}
```

After the session middleware runs, the actor is known — enrich the logger immediately:

```go
// Inside session middleware, after actor is resolved:
reqLogger := LoggerFromContext(r.Context()).With(
    FieldActorID,   actor.ID.String(),
    FieldActorType, actor.Type,
)
ctx = withLogger(r.Context(), reqLogger)
```

From this point on, every log line emitted during the request automatically carries actor context.

---

### Log levels — used correctly

| Level | When |
|---|---|
| `DEBUG` | Detailed flow tracing. Off in production by default. Enabled per-request via a query param or header in staging. |
| `INFO` | Business events worth recording: order placed, payment captured, subscription renewed. Also: request completed. |
| `WARN` | Unexpected but handled: payment retry attempt, deprecated API call, cache miss on critical path. |
| `ERROR` | Failures requiring investigation: unhandled error, database connection failure, River job exhausted retries. |

The most common mistake is treating every non-200 as `ERROR`. A 404 is expected. A 422 is a client error. `ERROR` should mean "a human should look at this."

```go
// Good
logger.Info("order.placed", FieldOrderID, order.ID, FieldAmount, order.Total)
logger.Warn("payment.retry_attempt", "attempt", attempt, FieldOrderID, order.ID)
logger.Error("db.query_failed", "err", err, FieldResourceType, "order")

// Bad — 404 is not an error
logger.Error("order not found", FieldOrderID, id)  // use Info or omit entirely
```

---

### Business event logging

Key business events get explicit log lines at `INFO` with structured fields. These are the lines you'll query in Loki when something goes wrong with an order or a renewal.

```go
// Order placed
log.Info("order.placed",
    FieldOrderID,    order.ID,
    FieldCustomerID, order.CustomerID,
    FieldAmount,     order.Total,
    FieldCurrency,   order.Currency,
)

// Payment captured
log.Info("payment.captured",
    FieldOrderID, order.ID,
    FieldAmount,  payment.Amount,
    "provider",   "stripe",
    "payment_intent_id", payment.ProviderID,
)

// Subscription renewal
log.Info("subscription.renewed",
    "subscription_id", sub.ID,
    FieldCustomerID,   sub.CustomerID,
    FieldAmount,       sub.Amount,
    "river_job_id",    jobID,
)

// Renewal failure
log.Error("subscription.renewal_failed",
    "subscription_id", sub.ID,
    FieldCustomerID,   sub.CustomerID,
    "attempt",         attempt,
    "err",             err,
)
```

---

### Loki query examples

With consistent field names, Loki LogQL queries are straightforward:

```logql
# All errors in the last hour
{service="lean-commerce"} | json | level="ERROR"

# Everything that happened to a specific order
{service="lean-commerce"} | json | order_id="<uuid>"

# All payment failures today
{service="lean-commerce"} | json | event="payment.failed"

# Slow requests (> 500ms)
{service="lean-commerce"} | json | duration_ms > 500

# What did this staff member do?
{service="lean-commerce"} | json | actor_id="<uuid>" | actor_type="staff"
```

These queries work because every field is consistently named and present. Inconsistent field names (mixing `order_id` and `orderId` and `oid`) make Loki close to useless.

---

## Part 2: Metrics

### Library: `prometheus/client_golang`

```go
import "github.com/prometheus/client_golang/prometheus"
import "github.com/prometheus/client_golang/prometheus/promhttp"
```

MIT licensed. The standard for Go + Prometheus. Metrics are registered on a custom registry (not the default global) to make testing easier and avoid conflicts with library metrics.

```go
// internal/metrics/registry.go
type Registry struct {
    reg *prometheus.Registry

    // HTTP
    HTTPRequestsTotal    *prometheus.CounterVec
    HTTPRequestDuration  *prometheus.HistogramVec

    // Business — orders
    OrdersPlacedTotal    *prometheus.CounterVec
    OrdersRevenueTotal   *prometheus.CounterVec

    // Business — payments
    PaymentsCapturedTotal *prometheus.CounterVec
    PaymentsFailedTotal   *prometheus.CounterVec

    // Business — subscriptions
    SubscriptionRenewalsTotal *prometheus.CounterVec
    SubscriptionRenewalFailuresTotal *prometheus.CounterVec
    ActiveSubscriptionsGauge  *prometheus.GaugeVec

    // Background jobs
    JobsProcessedTotal  *prometheus.CounterVec
    JobsFailedTotal     *prometheus.CounterVec
    JobQueueDepth       *prometheus.GaugeVec
}

func NewRegistry() *Registry {
    reg := prometheus.NewRegistry()
    r := &Registry{reg: reg}

    r.HTTPRequestsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "http_requests_total",
            Help: "Total HTTP requests by method, path, and status.",
        },
        []string{"method", "path", "status"},
    )

    r.HTTPRequestDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "http_request_duration_seconds",
            Help:    "HTTP request duration in seconds.",
            Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
        },
        []string{"method", "path", "status"},
    )

    r.OrdersPlacedTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "orders_placed_total",
            Help: "Total orders placed.",
        },
        []string{"currency"},
    )

    r.OrdersRevenueTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "orders_revenue_cents_total",
            Help: "Cumulative order revenue in cents.",
        },
        []string{"currency"},
    )

    r.PaymentsCapturedTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "payments_captured_total",
            Help: "Total payments successfully captured.",
        },
        []string{"provider"},
    )

    r.PaymentsFailedTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "payments_failed_total",
            Help: "Total payment failures.",
        },
        []string{"provider", "reason"},
    )

    r.SubscriptionRenewalsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "subscription_renewals_total",
            Help: "Total subscription renewal attempts.",
        },
        []string{"status"}, // "success" | "failed" | "skipped"
    )

    r.ActiveSubscriptionsGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "active_subscriptions",
            Help: "Current count of active subscriptions.",
        },
        []string{"plan_interval"}, // "monthly" | "weekly" etc.
    )

    r.JobsProcessedTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "jobs_processed_total",
            Help: "Total background jobs processed.",
        },
        []string{"kind", "status"}, // status: "completed" | "failed" | "cancelled"
    )

    r.JobQueueDepth = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "job_queue_depth",
            Help: "Current number of pending jobs by kind.",
        },
        []string{"kind"},
    )

    reg.MustRegister(
        r.HTTPRequestsTotal,
        r.HTTPRequestDuration,
        r.OrdersPlacedTotal,
        r.OrdersRevenueTotal,
        r.PaymentsCapturedTotal,
        r.PaymentsFailedTotal,
        r.SubscriptionRenewalsTotal,
        r.ActiveSubscriptionsGauge,
        r.JobsProcessedTotal,
        r.JobQueueDepth,
        // Standard Go runtime metrics
        collectors.NewGoCollector(),
        collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
    )

    return r
}

// Handler exposes the /metrics endpoint for Prometheus scraping.
func (r *Registry) Handler() http.Handler {
    return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{})
}
```

---

### HTTP metrics middleware

Increment counters and observe histograms after every request. This wraps the entire handler chain so it catches all requests including 404s and 500s.

```go
func MetricsMiddleware(reg *metrics.Registry) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()
            rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

            next.ServeHTTP(rw, r)

            duration := time.Since(start).Seconds()
            status := strconv.Itoa(rw.status)

            // Normalize path to avoid high-cardinality label explosion.
            // /orders/abc-123 and /orders/def-456 should be the same label.
            path := normalizePath(r.URL.Path) // e.g. /orders/{id}

            reg.HTTPRequestsTotal.WithLabelValues(r.Method, path, status).Inc()
            reg.HTTPRequestDuration.WithLabelValues(r.Method, path, status).Observe(duration)
        })
    }
}
```

**Path normalization is critical.** Without it, every unique order UUID becomes a separate label value — Prometheus cardinality explodes and scraping becomes unusable. `/orders/{id}` is the correct label; `/orders/abc-123-def-456` is not. Use your router's pattern (chi, stdlib mux) to extract the route template rather than the matched path.

---

### Business metric instrumentation points

Business metrics are incremented at the service layer, not the handler layer — the service is where the business event actually occurs, and it has access to the relevant values (amount, currency, plan).

```go
// OrderService.Create
func (s *OrderService) Create(ctx context.Context, tx pgx.Tx, ...) (*Order, error) {
    order, err := s.repo.Create(ctx, tx, ...)
    if err != nil { return nil, err }

    s.metrics.OrdersPlacedTotal.WithLabelValues(order.Currency).Inc()
    s.metrics.OrdersRevenueTotal.WithLabelValues(order.Currency).Add(float64(order.Total))

    return order, nil
}

// PaymentService.Capture
func (s *PaymentService) Capture(ctx context.Context, ...) error {
    err := s.stripe.Capture(...)
    if err != nil {
        reason := classifyPaymentError(err) // "card_declined" | "insufficient_funds" | etc.
        s.metrics.PaymentsFailedTotal.WithLabelValues("stripe", reason).Inc()
        return err
    }
    s.metrics.PaymentsCapturedTotal.WithLabelValues("stripe").Inc()
    return nil
}
```

---

### Prometheus scrape config

```yaml
# prometheus.yml
scrape_configs:
  - job_name: lean-commerce
    static_configs:
      - targets: ['localhost:8080']
    metrics_path: /metrics
    scrape_interval: 15s
```

The `/metrics` endpoint should not be exposed on the public-facing port in production. Route it on an internal port (e.g. `8081`) or protect it with IP allowlisting. Prometheus metrics expose internal application state — not something that should be publicly queryable.

---

## Part 3: Grafana Dashboards

Two dashboards cover the two questions this system needs to answer.

### Dashboard 1: System Health

Panels:

**Request rate** (requests/sec, by status class 2xx/4xx/5xx)
```promql
sum by (status_class) (
  rate(http_requests_total[5m])
)
```

**Error rate** (% of requests that are 5xx)
```promql
sum(rate(http_requests_total{status=~"5.."}[5m]))
/
sum(rate(http_requests_total[5m]))
```

**p50 / p95 / p99 latency**
```promql
histogram_quantile(0.95,
  sum by (le) (rate(http_request_duration_seconds_bucket[5m]))
)
```

**Payment failure rate**
```promql
rate(payments_failed_total[5m])
```

**Job queue depth** (are background jobs keeping up?)
```promql
job_queue_depth
```

### Dashboard 2: Business Activity

Panels:

**Orders per hour**
```promql
sum(increase(orders_placed_total[1h]))
```

**Revenue per hour (in dollars)**
```promql
sum(increase(orders_revenue_cents_total[1h])) / 100
```

**Subscription renewal success rate**
```promql
rate(subscription_renewals_total{status="success"}[1h])
/
rate(subscription_renewals_total[1h])
```

**Active subscriptions by interval**
```promql
active_subscriptions
```

---

## Part 4: Alerting

Minimum viable alert set. These are the alerts that wake someone up:

```yaml
# alerts.yml
groups:
  - name: lean-commerce
    rules:
      - alert: HighErrorRate
        expr: |
          sum(rate(http_requests_total{status=~"5.."}[5m]))
          / sum(rate(http_requests_total[5m])) > 0.05
        for: 2m
        annotations:
          summary: "Error rate above 5% for 2 minutes"

      - alert: HighLatency
        expr: |
          histogram_quantile(0.95,
            sum by (le) (rate(http_request_duration_seconds_bucket[5m]))
          ) > 2
        for: 5m
        annotations:
          summary: "p95 latency above 2s for 5 minutes"

      - alert: JobQueueBacklog
        expr: job_queue_depth > 500
        for: 10m
        annotations:
          summary: "Job queue depth above 500 for 10 minutes"

      - alert: SubscriptionRenewalFailureSpike
        expr: |
          rate(subscription_renewals_total{status="failed"}[15m]) > 0.1
        for: 5m
        annotations:
          summary: "Subscription renewal failure rate elevated"
```

---

## How metrics and logs complement each other

Metrics and logs answer different questions and should not be used interchangeably:

| Question | Tool |
|---|---|
| Is error rate elevated right now? | Prometheus metric + Grafana alert |
| Which specific requests are erroring? | Loki — filter by `level=ERROR` |
| How many orders in the last hour? | Prometheus counter |
| What happened to order X specifically? | Loki — filter by `order_id` |
| Is the job queue backing up? | Prometheus gauge |
| Why did job Y fail? | Loki — filter by `river_job_id` |
| What's p95 latency trending over a week? | Prometheus histogram + Grafana |
| What did this staff member do today? | Loki — filter by `actor_id` |

The `request_id` and `river_job_id` fields are the bridge between the two systems. A Grafana alert fires on elevated error rate → you open Loki and filter by `level=ERROR` in the same time window → you find the `request_id` → you see the full request context. The observability stack is only useful if these correlation IDs are present and consistent.

---

## Promtail configuration (log shipping)

```yaml
# promtail-config.yml
clients:
  - url: http://loki:3100/loki/api/v1/push

scrape_configs:
  - job_name: lean-commerce
    static_configs:
      - targets: [localhost]
        labels:
          service: lean-commerce
          env: production
          __path__: /var/log/lean-commerce/*.log

    pipeline_stages:
      - json:
          expressions:
            level: level
            request_id: request_id
            actor_id: actor_id
      - labels:
          level:
          request_id:
          actor_id:
```

This promotes `level`, `request_id`, and `actor_id` to Loki labels — making them indexed and fast to filter on. Don't promote high-cardinality fields (like `order_id`) to labels; keep those as log line content and use `| json | order_id="..."` in LogQL queries.
