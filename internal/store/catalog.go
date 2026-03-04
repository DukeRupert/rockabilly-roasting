package store

// CatalogStore provides database access for products, variants, options, taxons, and media.
type CatalogStore struct{}

// NewCatalogStore creates a new CatalogStore.
func NewCatalogStore() *CatalogStore {
	return &CatalogStore{}
}
