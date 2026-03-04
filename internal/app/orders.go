package app

import (
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// OrderService contains business logic for orders and carts.
type OrderService struct {
	orders  *store.OrderStore
	audit   *audit.AuditWriter
	metrics *metrics.Registry
}

// NewOrderService creates a new OrderService.
func NewOrderService(orders *store.OrderStore, audit *audit.AuditWriter, metrics *metrics.Registry) *OrderService {
	return &OrderService{
		orders:  orders,
		audit:   audit,
		metrics: metrics,
	}
}
