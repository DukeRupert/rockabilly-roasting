package app

import (
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/shipping"
	"github.com/dukerupert/hiri/internal/store"
)

// FulfillmentService contains business logic for fulfillment and shipping.
type FulfillmentService struct {
	fulfillment   *store.FulfillmentStore
	shipments     *store.ShippingStore
	orders        *store.OrderStore
	labelProvider shipping.LabelProvider
	audit         *audit.AuditWriter
	metrics       *metrics.Registry
}

// NewFulfillmentService creates a new FulfillmentService.
func NewFulfillmentService(
	fulfillment *store.FulfillmentStore,
	shipments *store.ShippingStore,
	orders *store.OrderStore,
	labelProvider shipping.LabelProvider,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *FulfillmentService {
	return &FulfillmentService{
		fulfillment:   fulfillment,
		shipments:     shipments,
		orders:        orders,
		labelProvider: labelProvider,
		audit:         audit,
		metrics:       metrics,
	}
}
