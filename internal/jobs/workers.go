package jobs

import (
	"github.com/google/uuid"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
)

// Deps holds dependencies shared by all job workers.
type Deps struct {
	OrderService        *app.OrderService
	CustomerService     *app.CustomerService
	SubscriptionService *app.SubscriptionService
	FulfillmentService  *app.FulfillmentService
	CheckoutService     *app.CheckoutService
	AuthService         *app.AuthService
	Audit               *audit.AuditWriter
	Metrics             *metrics.Registry
}

// Job argument types — each implements river.JobArgs with Kind() returning snake_case.

// SubscriptionRenewalArgs triggers a subscription renewal.
type SubscriptionRenewalArgs struct {
	SubscriptionID uuid.UUID `json:"subscription_id"`
}

// Kind returns the job kind identifier.
func (SubscriptionRenewalArgs) Kind() string { return "subscription_renewal" }

// BatchRenewalArgs triggers a batched renewal for multiple subscriptions
// belonging to the same customer and shipping address.
type BatchRenewalArgs struct {
	SubscriptionIDs []uuid.UUID `json:"subscription_ids"`
}

// Kind returns the job kind identifier.
func (BatchRenewalArgs) Kind() string { return "batch_renewal" }

// PaymentRetryArgs retries a failed payment.
type PaymentRetryArgs struct {
	OrderID uuid.UUID `json:"order_id"`
}

// Kind returns the job kind identifier.
func (PaymentRetryArgs) Kind() string { return "payment_retry" }

// OrderConfirmationArgs sends an order confirmation.
type OrderConfirmationArgs struct {
	OrderID uuid.UUID `json:"order_id"`
}

// Kind returns the job kind identifier.
func (OrderConfirmationArgs) Kind() string { return "order_confirmation" }

// CartExpiryArgs expires an abandoned cart.
type CartExpiryArgs struct {
	CartID uuid.UUID `json:"cart_id"`
}

// Kind returns the job kind identifier.
func (CartExpiryArgs) Kind() string { return "cart_expiry" }

// AbandonedCartArgs sends an abandoned cart notification.
type AbandonedCartArgs struct {
	CartID uuid.UUID `json:"cart_id"`
}

// Kind returns the job kind identifier.
func (AbandonedCartArgs) Kind() string { return "abandoned_cart" }

// SessionPruneArgs prunes expired sessions.
type SessionPruneArgs struct{}

// Kind returns the job kind identifier.
func (SessionPruneArgs) Kind() string { return "session_prune" }
