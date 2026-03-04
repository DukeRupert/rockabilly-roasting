package store

// WebhookStore provides database access for webhook event tracking.
type WebhookStore struct{}

// NewWebhookStore creates a new WebhookStore.
func NewWebhookStore() *WebhookStore {
	return &WebhookStore{}
}
