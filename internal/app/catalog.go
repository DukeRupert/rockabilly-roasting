package app

import (
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// CatalogService contains business logic for products, variants, and taxonomy.
type CatalogService struct {
	catalog *store.CatalogStore
	audit   *audit.AuditWriter
	metrics *metrics.Registry
}

// NewCatalogService creates a new CatalogService.
func NewCatalogService(catalog *store.CatalogStore, audit *audit.AuditWriter, metrics *metrics.Registry) *CatalogService {
	return &CatalogService{
		catalog: catalog,
		audit:   audit,
		metrics: metrics,
	}
}
