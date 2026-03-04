package app

import (
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// SubscriptionService contains business logic for subscriptions.
type SubscriptionService struct {
	subscriptions *store.SubscriptionStore
	orders        *store.OrderStore
	audit         *audit.AuditWriter
	metrics       *metrics.Registry
}

// NewSubscriptionService creates a new SubscriptionService.
func NewSubscriptionService(
	subscriptions *store.SubscriptionStore,
	orders *store.OrderStore,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *SubscriptionService {
	return &SubscriptionService{
		subscriptions: subscriptions,
		orders:        orders,
		audit:         audit,
		metrics:       metrics,
	}
}
