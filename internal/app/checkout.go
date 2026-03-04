package app

import (
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// CheckoutService orchestrates the checkout flow: cart validation, payment, and order creation.
type CheckoutService struct {
	orders    *store.OrderStore
	customers *store.CustomerStore
	discounts *store.DiscountStore
	audit     *audit.AuditWriter
	metrics   *metrics.Registry
}

// NewCheckoutService creates a new CheckoutService.
func NewCheckoutService(
	orders *store.OrderStore,
	customers *store.CustomerStore,
	discounts *store.DiscountStore,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *CheckoutService {
	return &CheckoutService{
		orders:    orders,
		customers: customers,
		discounts: discounts,
		audit:     audit,
		metrics:   metrics,
	}
}
