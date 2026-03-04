package store

// CustomerStore provides database access for customers and addresses.
type CustomerStore struct{}

// NewCustomerStore creates a new CustomerStore.
func NewCustomerStore() *CustomerStore {
	return &CustomerStore{}
}
