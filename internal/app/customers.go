package app

import (
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// CustomerService contains business logic for customer management.
type CustomerService struct {
	customers *store.CustomerStore
	audit     *audit.AuditWriter
	metrics   *metrics.Registry
}

// NewCustomerService creates a new CustomerService.
func NewCustomerService(customers *store.CustomerStore, audit *audit.AuditWriter, metrics *metrics.Registry) *CustomerService {
	return &CustomerService{
		customers: customers,
		audit:     audit,
		metrics:   metrics,
	}
}
