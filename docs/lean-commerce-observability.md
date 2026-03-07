# Lean Commerce — Observability: Metrics & Structured Logging

Stack: **Prometheus** (metrics collection) + **Grafana** (dashboards + alerting) + **Loki** (log aggregation). All self-hosted. The application emits metrics via a `/metrics` endpoint and writes structured logs to stdout — the host's log collector (Promtail or Alloy) ships them to Loki.

Two questions this system must answer:
1. **Is the system healthy right now?** (errors, latency, uptime)
2. **What is the business doing?** (orders, renewals, revenue)

These are different data sources that complement each other. Metrics answer "how many / how fast / how often." Logs answer "what exactly happened and why."

---

## Part 1: Structured Logging

### Foundation: `slog`

Go's standard `log/slog` package (Go 1.21+). JSON handler writing to stdout. No external dependency.

```go
// platform/logging/logging.go
func New(level slog.Level) *slog.Logger {
    return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
        Level: level,
    }))
}
```

Stdout → Promtail/Alloy → Loki. The host process handles log shipping, not the application.

---

### Standard fields

Defined in `platform/logging/logging.go`:

```go
const (
    FieldRequestID   = "request_id"    // unique per HTTP request
    FieldActorID     = "actor_id"      // authenticated user/staff ID
    FieldActorType   = "actor_type"    // "customer" | "staff" | "system"
    FieldMethod      = "method"        // HTTP method
    FieldPath        = "path"          // URL path
    FieldStatus      = "status"        // HTTP response status code
    FieldDurationMS  = "duration_ms"   // request processing time
    FieldService     = "service"       // "lean-commerce"
    FieldEnv         = "env"           // "production" | "staging"
    FieldEvent       = "event"         // "order.placed", "payment.captured"
    FieldResourceType = "resource_type"
    FieldResourceID  = "resource_id"
    FieldOrderID     = "order_id"
    FieldCustomerID  = "customer_id"
    FieldAmount      = "amount"        // always in cents
    FieldCurrency    = "currency"
)
```

---

### Request-scoped logger via context

The logger for a request is built in `requestIDMiddleware`, enriched with a unique request ID, and passed through context. Downstream handlers and services pull it via `logging.FromContext(ctx)`.

```go
// web/middleware.go — requestIDMiddleware
requestID := uuid.New().String()
logger := logging.FromContext(ctx).With(slog.String(logging.FieldRequestID, requestID))
ctx = logging.WithContext(ctx, logger)
w.Header().Set("X-Request-ID", requestID)
```

### 5xx error logging

The `loggingMiddleware` differentiates between normal and error responses. 5xx errors are logged at `Error` level using the context logger (which carries the request_id), providing full diagnostic context:

```go
if rw.statusCode >= 500 {
    ctxLogger.Error("request failed",
        slog.String(logging.FieldMethod, r.Method),
        slog.String(logging.FieldPath, r.URL.Path),
        slog.Int(logging.FieldStatus, rw.statusCode),
        slog.Float64(logging.FieldDurationMS, float64(duration.Milliseconds())),
    )
}
```

### Job failure logging

River job workers log failures with full context for diagnosis via Loki:

```go
slog.ErrorContext(ctx, "job failed",
    "job_kind",        "subscription_renewal",
    "job_id",          job.ID,
    "attempt",         job.Attempt,
    "subscription_id", job.Args.SubscriptionID,
    "error",           err.Error(),
)
```

---

### Log levels

| Level | When |
|---|---|
| `DEBUG` | Detailed flow tracing. Off in production. |
| `INFO` | Business events: order placed, payment captured, subscription renewed. Request completed. |
| `WARN` | Unexpected but handled: payment retry, deprecated API, cache miss. |
| `ERROR` | Failures requiring investigation: 5xx, DB failures, job exhausted retries. |

A 404 is not an error. A 422 is a client error. `ERROR` means "a human should look at this."

---

## Part 2: Metrics

### Registry

All metrics are defined in `platform/metrics/registry.go` and registered on a custom Prometheus registry (not the global default). The registry includes Go runtime + process collectors.

**Metric inventory:**

#### HTTP layer
| Metric | Type | Labels |
|---|---|---|
| `http_requests_total` | counter | `method`, `path_pattern`, `status_code` |
| `http_request_duration_seconds` | histogram | `method`, `path_pattern`, `status_code` |
| `http_requests_in_flight` | gauge | — |

`path_pattern` uses normalized patterns (`/admin/catalog/{id}`) not raw URLs (`/admin/catalog/abc-123`). UUIDs are replaced with `{id}`, static assets collapsed to `/static/{file}`. Commerce-tuned histogram buckets: 5ms, 25ms, 50ms, 100ms, 250ms, 500ms, 1s, 2.5s, 5s.

#### Database
| Metric | Type | Labels |
|---|---|---|
| `db_query_duration_seconds` | histogram | `query_name`, `success` |
| `db_pool_open_connections` | gauge | — |
| `db_pool_idle_connections` | gauge | — |
| `db_pool_wait_count_total` | counter | — |

Pool metrics scraped from `pgxpool.Stat()` every 15 seconds by a background goroutine. Query timing via `metrics.TrackQuery()` helper at the store layer.

#### River jobs
| Metric | Type | Labels |
|---|---|---|
| `river_jobs_enqueued_total` | counter | `job_kind` |
| `river_jobs_completed_total` | counter | `job_kind` |
| `river_jobs_failed_total` | counter | `job_kind` |
| `river_jobs_pending` | gauge | `job_kind`, `state` |
| `river_job_duration_seconds` | histogram | `job_kind`, `success` |

Queue depth populated by a background goroutine querying `river_job` table every 15 seconds. Per-job counters via `metrics.TrackJob()` helper in worker `Work` methods.

#### Checkout funnel
| Metric | Type | Labels |
|---|---|---|
| `checkout_started_total` | counter | `customer_type` (retail\|wholesale) |
| `checkout_completed_total` | counter | `customer_type` |
| `checkout_failed_total` | counter | `customer_type`, `failure_reason` |
| `checkout_duration_seconds` | histogram | `customer_type` |
| `coupon_applied_total` | counter | — |
| `coupon_rejected_total` | counter | `rejection_reason` |

`failure_reason` labels: `payment_failed`, `coupon_redeemed`, `inventory_unavailable`, `validation_error`, `internal_error`. Conversion rate: `rate(checkout_completed_total[5m]) / rate(checkout_started_total[5m])`.

#### Subscription health
| Metric | Type | Labels |
|---|---|---|
| `subscriptions_active_total` | gauge | — |
| `subscriptions_paused_total` | gauge | — |
| `subscriptions_cancelled_total` | gauge | — |
| `subscription_renewals_total` | counter | `result` (success\|failed) |

Status gauges populated by a background goroutine querying `subscriptions` table every 60 seconds.

#### Stripe webhooks
| Metric | Type | Labels |
|---|---|---|
| `stripe_webhooks_received_total` | counter | `event_type` |
| `stripe_webhooks_processed_total` | counter | `event_type`, `result` (success\|failed\|duplicate) |

#### Rate limiting
| Metric | Type | Labels |
|---|---|---|
| `rate_limit_hits_total` | counter | `config`, `key_type` |

---

### HTTP metrics middleware

Implemented in `platform/metrics/middleware.go`. Wraps the entire handler chain as outermost middleware. Tracks in-flight requests, request count, and duration.

```go
// web/router.go — middleware stack (outermost runs first)
var handler http.Handler = mux
handler = requestIDMiddleware(handler)
handler = loggingMiddleware(handler, deps.Logger, deps.Metrics)
handler = metrics.HTTPMiddleware(deps.Metrics)(handler)
```

Path normalization replaces UUID segments with `{id}` and collapses `/static/` paths to prevent cardinality explosion.

---

### Background metric collectors

Started in `cmd/server/main.go` at startup, stopped via context cancellation at shutdown:

```go
metrics.CollectPoolMetrics(ctx, metricsReg, pool, 15*time.Second)
metrics.CollectRiverMetrics(ctx, metricsReg, pool, 15*time.Second)
metrics.CollectSubscriptionMetrics(ctx, metricsReg, pool, 60*time.Second)
```

---

### DB query instrumentation

Use `metrics.TrackQuery()` in store methods:

```go
func (s *Store) ListOrders(ctx context.Context, tx pgx.Tx) (result []Order, err error) {
    defer metrics.TrackQuery(s.metrics, "orders.list", time.Now(), &err)
    // ... query
}
```

---

### River job instrumentation

Use `metrics.TrackJob()` in worker `Work` methods:

```go
func (w *MyWorker) Work(ctx context.Context, job *river.Job[MyArgs]) error {
    start := time.Now()
    err := w.doWork(ctx, job)
    metrics.TrackJob(w.metrics, "my_job_kind", start, err)
    return err
}
```

Use `metrics.TrackJobEnqueued()` when enqueueing jobs.

---

### Prometheus endpoint

Exposed at `GET /metrics` using the custom registry:

```go
mux.Handle("GET /metrics", promhttp.HandlerFor(deps.Metrics.Reg, promhttp.HandlerOpts{}))
```

In production, this should be protected via IP allowlisting or moved to an internal port. Metrics expose internal application state.

### Scrape config

```yaml
# prometheus.yml
scrape_configs:
  - job_name: lean-commerce
    static_configs:
      - targets: ['localhost:8080']
    metrics_path: /metrics
    scrape_interval: 15s
```

---

## Part 3: Grafana Dashboards

Four dashboards:

### 1. System Health — "Is the app up and healthy?"

**Request rate** (by status class):
```promql
sum by (status_code) (rate(http_requests_total[5m]))
```

**Error rate** (% 5xx):
```promql
sum(rate(http_requests_total{status_code=~"5.."}[5m]))
/ sum(rate(http_requests_total[5m]))
```

**p50 / p95 / p99 latency**:
```promql
histogram_quantile(0.95,
  sum by (le) (rate(http_request_duration_seconds_bucket[5m]))
)
```

**DB pool utilization**:
```promql
db_pool_open_connections
db_pool_idle_connections
```

**Slow query heatmap** — which `query_name` values are in the top percentiles:
```promql
histogram_quantile(0.95,
  sum by (query_name, le) (rate(db_query_duration_seconds_bucket[5m]))
)
```

### 2. Checkout Health — "Is the store making money?"

**Checkout conversion rate**:
```promql
rate(checkout_completed_total[5m]) / rate(checkout_started_total[5m])
```

**Checkout failure breakdown**:
```promql
sum by (failure_reason) (rate(checkout_failed_total[5m]))
```

**Coupon usage**:
```promql
rate(coupon_applied_total[5m])
sum by (rejection_reason) (rate(coupon_rejected_total[5m]))
```

### 3. Subscription Health — "Is recurring revenue healthy?"

**Active / paused / cancelled counts**:
```promql
subscriptions_active_total
subscriptions_paused_total
subscriptions_cancelled_total
```

**Renewal success rate**:
```promql
rate(subscription_renewals_total{result="success"}[1h])
/ rate(subscription_renewals_total[1h])
```

### 4. Background Jobs — "Is async work keeping up?"

**Job completion/failure rate by kind**:
```promql
sum by (job_kind) (rate(river_jobs_completed_total[5m]))
sum by (job_kind) (rate(river_jobs_failed_total[5m]))
```

**Queue depth trends**:
```promql
river_jobs_pending
```

---

## Part 4: Alerting

```yaml
# alerts.yml
groups:
  - name: lean-commerce
    rules:
      - alert: HighErrorRate
        expr: |
          sum(rate(http_requests_total{status_code=~"5.."}[5m]))
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

      - alert: CheckoutConversionDrop
        expr: |
          rate(checkout_completed_total[10m])
          / rate(checkout_started_total[10m]) < 0.7
        for: 10m
        annotations:
          summary: "Checkout conversion rate below 70% for 10 minutes"

      - alert: RiverJobFailureSpike
        expr: |
          sum by (job_kind) (rate(river_jobs_failed_total[5m])) > 0
        for: 5m
        annotations:
          summary: "River job failures detected for {{ $labels.job_kind }}"

      - alert: RiverQueueBacklog
        expr: |
          sum by (job_kind) (river_jobs_pending) > 500
        for: 10m
        annotations:
          summary: "Job queue depth above 500 for {{ $labels.job_kind }}"

      - alert: SubscriptionRenewalFailureRate
        expr: |
          rate(subscription_renewals_total{result="failed"}[1h])
          / rate(subscription_renewals_total[1h]) > 0.05
        for: 15m
        annotations:
          summary: "Subscription renewal failure rate above 5%"
```

---

## How metrics and logs complement each other

| Question | Tool |
|---|---|
| Is error rate elevated right now? | Prometheus metric + Grafana alert |
| Which specific requests are erroring? | Loki — filter by `level=ERROR` |
| Is checkout converting? | Prometheus checkout funnel counters |
| Why did checkout X fail? | Loki — filter by `request_id` from 5xx log |
| Is the job queue backing up? | Prometheus `river_jobs_pending` gauge |
| Why did job Y fail? | Loki — filter by `job_kind` + `job_id` |
| What's p95 latency trending over a week? | Prometheus histogram + Grafana |
| What did this staff member do today? | Loki — filter by `actor_id` |

The `request_id` and `job_id` fields bridge the two systems. A Grafana alert fires on elevated error rate → open Loki, filter by `level=ERROR` in the same window → find the `request_id` → see full context.

---

## Promtail configuration

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

Promote `level`, `request_id`, and `actor_id` to Loki labels for indexed, fast filtering. Don't promote high-cardinality fields like `order_id` — use `| json | order_id="..."` in LogQL queries.
