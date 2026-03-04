package metrics

import "github.com/prometheus/client_golang/prometheus"

// Registry holds all application Prometheus metrics.
type Registry struct {
	Reg *prometheus.Registry

	// HTTP
	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec

	// Business — orders
	OrdersPlacedTotal  *prometheus.CounterVec
	OrdersRevenueTotal *prometheus.CounterVec

	// Business — payments
	PaymentsCapturedTotal *prometheus.CounterVec
	PaymentsFailedTotal   *prometheus.CounterVec

	// Business — subscriptions
	SubscriptionRenewalsTotal        *prometheus.CounterVec
	SubscriptionRenewalFailuresTotal *prometheus.CounterVec
	ActiveSubscriptionsGauge         *prometheus.GaugeVec

	// Background jobs
	JobsProcessedTotal *prometheus.CounterVec
	JobQueueDepth      *prometheus.GaugeVec

	// Rate limiting
	RateLimitHitsTotal *prometheus.CounterVec
}

// NewRegistry creates and registers all application metrics.
func NewRegistry() *Registry {
	reg := prometheus.NewRegistry()

	m := &Registry{
		Reg: reg,

		HTTPRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests.",
		}, []string{"method", "path", "status"}),

		HTTPRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "path", "status"}),

		OrdersPlacedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "orders_placed_total",
			Help: "Total number of orders placed.",
		}, []string{"currency"}),

		OrdersRevenueTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "orders_revenue_total",
			Help: "Total revenue in cents.",
		}, []string{"currency"}),

		PaymentsCapturedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "payments_captured_total",
			Help: "Total payments captured.",
		}, []string{"provider"}),

		PaymentsFailedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "payments_failed_total",
			Help: "Total payments failed.",
		}, []string{"provider", "reason"}),

		SubscriptionRenewalsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "subscription_renewals_total",
			Help: "Total subscription renewals.",
		}, []string{"status"}),

		SubscriptionRenewalFailuresTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "subscription_renewal_failures_total",
			Help: "Total subscription renewal failures.",
		}, []string{"reason"}),

		ActiveSubscriptionsGauge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "active_subscriptions",
			Help: "Number of active subscriptions.",
		}, []string{"plan_interval"}),

		JobsProcessedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "jobs_processed_total",
			Help: "Total background jobs processed.",
		}, []string{"kind", "status"}),

		JobQueueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "job_queue_depth",
			Help: "Current depth of the job queue.",
		}, []string{"kind"}),

		RateLimitHitsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "rate_limit_hits_total",
			Help: "Total rate limit hits.",
		}, []string{"config", "key_type"}),
	}

	reg.MustRegister(
		m.HTTPRequestsTotal,
		m.HTTPRequestDuration,
		m.OrdersPlacedTotal,
		m.OrdersRevenueTotal,
		m.PaymentsCapturedTotal,
		m.PaymentsFailedTotal,
		m.SubscriptionRenewalsTotal,
		m.SubscriptionRenewalFailuresTotal,
		m.ActiveSubscriptionsGauge,
		m.JobsProcessedTotal,
		m.JobQueueDepth,
		m.RateLimitHitsTotal,
	)

	return m
}
