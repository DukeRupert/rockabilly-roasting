package store

// FulfillmentStore provides database access for fulfillments, inventory, and stock levels.
type FulfillmentStore struct{}

// NewFulfillmentStore creates a new FulfillmentStore.
func NewFulfillmentStore() *FulfillmentStore {
	return &FulfillmentStore{}
}
