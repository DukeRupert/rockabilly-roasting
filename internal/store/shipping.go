package store

// ShippingStore provides database access for shipping config and shipments.
type ShippingStore struct{}

// NewShippingStore creates a new ShippingStore.
func NewShippingStore() *ShippingStore {
	return &ShippingStore{}
}
