package metrics

import "github.com/prometheus/client_golang/prometheus"

// Commerce-tuned histogram buckets — most handlers should be under 100ms.
var httpBuckets = []float64{0.005, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

// Registry holds all application Prometheus metrics.
// Initialized once at startup, injected into middleware and services.
type Registry struct {
	Reg *prometheus.Registry

	// HTTP
	HTTPRequestsTotal    *prometheus.CounterVec
	HTTPRequestDuration  *prometheus.HistogramVec
	HTTPRequestsInFlight prometheus.Gauge

	// Database
	DBQueryDuration *prometheus.HistogramVec
	DBPoolOpen      prometheus.Gauge
	DBPoolIdle      prometheus.Gauge
	DBPoolWaitCount prometheus.Counter

	// River jobs
	RiverJobsEnqueued  *prometheus.CounterVec
	RiverJobsCompleted *prometheus.CounterVec
	RiverJobsFailed    *prometheus.CounterVec
	RiverJobsPending   *prometheus.GaugeVec
	RiverJobDuration   *prometheus.HistogramVec

	// Checkout funnel
	CheckoutStarted   *prometheus.CounterVec
	CheckoutCompleted *prometheus.CounterVec
	CheckoutFailed    *prometheus.CounterVec
	CheckoutDuration  *prometheus.HistogramVec
	CouponApplied     prometheus.Counter
	CouponRejected    *prometheus.CounterVec

	// Subscription health
	SubscriptionsActive    prometheus.Gauge
	SubscriptionsPaused    prometheus.Gauge
	SubscriptionsCancelled prometheus.Gauge
	SubscriptionRenewals   *prometheus.CounterVec

	// Stripe webhooks
	StripeWebhooksReceived  *prometheus.CounterVec
	StripeWebhooksProcessed *prometheus.CounterVec

	// Rate limiting
	RateLimitHitsTotal *prometheus.CounterVec

	// Email
	EmailsSent *prometheus.CounterVec
}

// NewRegistry creates and registers all application metrics.
func NewRegistry() *Registry {
	reg := prometheus.NewRegistry()

	// Include default Go runtime + process collectors.
	reg.MustRegister(prometheus.NewGoCollector())
	reg.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))

	m := &Registry{
		Reg: reg,

		// --- HTTP ---

		HTTPRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests.",
		}, []string{"method", "path_pattern", "status_code"}),

		HTTPRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: httpBuckets,
		}, []string{"method", "path_pattern", "status_code"}),

		HTTPRequestsInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "Number of HTTP requests currently being served.",
		}),

		// --- Database ---

		DBQueryDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "db_query_duration_seconds",
			Help:    "Database query duration in seconds.",
			Buckets: httpBuckets,
		}, []string{"query_name", "success"}),

		DBPoolOpen: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "db_pool_open_connections",
			Help: "Number of open database connections.",
		}),

		DBPoolIdle: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "db_pool_idle_connections",
			Help: "Number of idle database connections.",
		}),

		DBPoolWaitCount: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "db_pool_wait_count_total",
			Help: "Total times a query waited for a database connection.",
		}),

		// --- River jobs ---

		RiverJobsEnqueued: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "river_jobs_enqueued_total",
			Help: "Total River jobs enqueued.",
		}, []string{"job_kind"}),

		RiverJobsCompleted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "river_jobs_completed_total",
			Help: "Total River jobs completed successfully.",
		}, []string{"job_kind"}),

		RiverJobsFailed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "river_jobs_failed_total",
			Help: "Total River jobs that failed.",
		}, []string{"job_kind"}),

		RiverJobsPending: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "river_jobs_pending",
			Help: "Current River job queue depth per kind.",
		}, []string{"job_kind", "state"}),

		RiverJobDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "river_job_duration_seconds",
			Help:    "River job execution duration in seconds.",
			Buckets: httpBuckets,
		}, []string{"job_kind", "success"}),

		// --- Checkout funnel ---

		CheckoutStarted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "checkout_started_total",
			Help: "Total checkouts started.",
		}, []string{"customer_type"}),

		CheckoutCompleted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "checkout_completed_total",
			Help: "Total checkouts completed successfully.",
		}, []string{"customer_type"}),

		CheckoutFailed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "checkout_failed_total",
			Help: "Total checkouts that failed.",
		}, []string{"customer_type", "failure_reason"}),

		CheckoutDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "checkout_duration_seconds",
			Help:    "Checkout flow duration in seconds.",
			Buckets: httpBuckets,
		}, []string{"customer_type"}),

		CouponApplied: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "coupon_applied_total",
			Help: "Total coupons successfully applied.",
		}),

		CouponRejected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "coupon_rejected_total",
			Help: "Total coupons rejected.",
		}, []string{"rejection_reason"}),

		// --- Subscription health ---

		SubscriptionsActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "subscriptions_active_total",
			Help: "Current count of active subscriptions.",
		}),

		SubscriptionsPaused: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "subscriptions_paused_total",
			Help: "Current count of paused subscriptions.",
		}),

		SubscriptionsCancelled: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "subscriptions_cancelled_total",
			Help: "Current count of cancelled subscriptions.",
		}),

		SubscriptionRenewals: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "subscription_renewals_total",
			Help: "Total subscription renewal attempts.",
		}, []string{"result"}),

		// --- Stripe webhooks ---

		StripeWebhooksReceived: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "stripe_webhooks_received_total",
			Help: "Total Stripe webhook events received.",
		}, []string{"event_type"}),

		StripeWebhooksProcessed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "stripe_webhooks_processed_total",
			Help: "Total Stripe webhook events processed.",
		}, []string{"event_type", "result"}),

		// --- Rate limiting ---

		RateLimitHitsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "rate_limit_hits_total",
			Help: "Total rate limit hits.",
		}, []string{"config", "key_type"}),

		// --- Email ---

		EmailsSent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "emails_sent_total",
			Help: "Total transactional emails sent.",
		}, []string{"kind", "status"}),
	}

	reg.MustRegister(
		// HTTP
		m.HTTPRequestsTotal,
		m.HTTPRequestDuration,
		m.HTTPRequestsInFlight,
		// DB
		m.DBQueryDuration,
		m.DBPoolOpen,
		m.DBPoolIdle,
		m.DBPoolWaitCount,
		// River
		m.RiverJobsEnqueued,
		m.RiverJobsCompleted,
		m.RiverJobsFailed,
		m.RiverJobsPending,
		m.RiverJobDuration,
		// Checkout
		m.CheckoutStarted,
		m.CheckoutCompleted,
		m.CheckoutFailed,
		m.CheckoutDuration,
		m.CouponApplied,
		m.CouponRejected,
		// Subscriptions
		m.SubscriptionsActive,
		m.SubscriptionsPaused,
		m.SubscriptionsCancelled,
		m.SubscriptionRenewals,
		// Stripe webhooks
		m.StripeWebhooksReceived,
		m.StripeWebhooksProcessed,
		// Rate limiting
		m.RateLimitHitsTotal,
		// Email
		m.EmailsSent,
	)

	return m
}
