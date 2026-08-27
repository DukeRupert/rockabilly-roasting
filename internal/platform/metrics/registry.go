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
	RiverJobsCancelled *prometheus.CounterVec
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

	// Equipment service module. The two gauges are set by the daily stale sweep
	// and the counter by whoever opens a ticket, so all three are only
	// meaningful on an instance with the module switched on — everywhere else
	// the sweep returns early, nobody opens a ticket, and they stay at zero.
	ServiceTicketsOpen   *prometheus.GaugeVec
	ServiceTicketsStale  prometheus.Gauge
	ServiceTicketsOpened *prometheus.CounterVec

	// Stripe webhooks
	StripeWebhooksReceived  *prometheus.CounterVec
	StripeWebhooksProcessed *prometheus.CounterVec

	// Rate limiting
	RateLimitHitsTotal *prometheus.CounterVec

	// Email
	EmailsSent *prometheus.CounterVec

	// Delivery routes
	RoutesPlanned      *prometheus.CounterVec
	RouteStops         prometheus.Histogram
	RouteDuration      prometheus.Histogram
	RouteStopsResolved *prometheus.CounterVec
	GeocodeLookups     *prometheus.CounterVec
}

// routeStopBuckets span a delivery day: a handful of stops on a quiet Monday,
// up to the ~99 the router will take. Fine-grained at the low end because the
// difference between 5 and 15 stops is a different kind of day; coarse past 40,
// where the only question is "is this run unusually big?".
var routeStopBuckets = []float64{1, 3, 5, 8, 12, 20, 30, 40, 60, 100}

// routeDurationBuckets are drive-time seconds. The Tri-Cities baseline run is
// ~50 minutes, so the interesting range is 15 minutes to about three hours.
var routeDurationBuckets = []float64{900, 1800, 2700, 3600, 5400, 7200, 10800}

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

		// A third outcome, because some jobs end without either succeeding or
		// failing: a renewal whose card declined did its work correctly and
		// must not be retried, and counting it as a failure put it on the same
		// graph line as a worker that is actually broken.
		RiverJobsCancelled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "river_jobs_cancelled_total",
			Help: "Total River jobs cancelled deliberately: terminal, but working as intended.",
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

		// --- Equipment service module ---

		// Labelled by status so the breakdown shows where work is piling up —
		// a queue of thirty "waiting_parts" is a supplier problem, thirty "new"
		// is a staffing one.
		ServiceTicketsOpen: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "service_tickets_open_total",
			Help: "Current count of service tickets that are not resolved or cancelled, by status.",
		}, []string{"status"}),

		// The one worth alerting on: an open ticket nobody has spoken to the
		// customer about inside the contact window is how an account is lost
		// quietly.
		ServiceTicketsStale: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "service_tickets_stale_total",
			Help: "Current count of open service tickets past the customer-contact window.",
		}),

		// Labelled by who raised it, which only became a question worth asking
		// once wholesale customers could open tickets themselves: the ratio of
		// customer-reported to staff-reported work is how a merchant finds out
		// whether the portal is being used at all.
		ServiceTicketsOpened: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "service_tickets_opened_total",
			Help: "Total service tickets opened, by who raised them and how bad it was.",
		}, []string{"source", "severity"}),

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

		// --- Delivery routes ---

		RoutesPlanned: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "delivery_routes_planned_total",
			Help: "Delivery routes planned, by outcome.",
		}, []string{"outcome"}),

		RouteStops: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "delivery_route_stops",
			Help:    "Stops per planned delivery route.",
			Buckets: routeStopBuckets,
		}),

		RouteDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "delivery_route_duration_seconds",
			Help:    "Estimated drive time of a planned delivery route.",
			Buckets: routeDurationBuckets,
		}),

		RouteStopsResolved: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "delivery_route_stops_resolved_total",
			Help: "Route stops closed out by the driver, by outcome (delivered/skipped).",
		}, []string{"outcome"}),

		// The ratio that matters: hits should dominate once the cache is warm,
		// because every miss is a billed Google call. A sustained rise in
		// misses means addresses are churning or the cache was cleared.
		GeocodeLookups: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "geocode_lookups_total",
			Help: "Address geocode resolutions, by source (cache/provider) and outcome.",
		}, []string{"source", "outcome"}),
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
		m.RiverJobsCancelled,
		m.RiverJobsPending,
		m.RiverJobDuration,
		// Checkout
		m.CheckoutStarted,
		m.CheckoutCompleted,
		m.CheckoutFailed,
		m.CheckoutDuration,
		m.CouponApplied,
		m.CouponRejected,
		// Equipment service
		m.ServiceTicketsOpen,
		m.ServiceTicketsStale,
		m.ServiceTicketsOpened,
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
		// Delivery routes
		m.RoutesPlanned,
		m.RouteStops,
		m.RouteDuration,
		m.RouteStopsResolved,
		m.GeocodeLookups,
	)

	return m
}
