package store

// OrderStore provides database access for orders, carts, line items, and adjustments.
type OrderStore struct{}

// NewOrderStore creates a new OrderStore.
func NewOrderStore() *OrderStore {
	return &OrderStore{}
}
