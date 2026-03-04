package store

// SubscriptionStore provides database access for subscription plans and subscriptions.
type SubscriptionStore struct{}

// NewSubscriptionStore creates a new SubscriptionStore.
func NewSubscriptionStore() *SubscriptionStore {
	return &SubscriptionStore{}
}
