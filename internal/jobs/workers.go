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
	WholesaleService    *app.WholesaleService
	InvoiceService      *app.InvoiceService
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

// OrderConfirmEmailArgs sends an order confirmation email.
type OrderConfirmEmailArgs struct {
	OrderID    uuid.UUID `json:"order_id"`
	CustomerID uuid.UUID `json:"customer_id"`
}

// Kind returns the job kind identifier.
func (OrderConfirmEmailArgs) Kind() string { return "email:order_confirm" }

// SubscriptionConfirmEmailArgs sends a subscription confirmation email.
type SubscriptionConfirmEmailArgs struct {
	SubscriptionID uuid.UUID `json:"subscription_id"`
	CustomerID     uuid.UUID `json:"customer_id"`
}

// Kind returns the job kind identifier.
func (SubscriptionConfirmEmailArgs) Kind() string { return "email:subscription_confirm" }

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

// WholesaleApplicationNotifyArgs notifies staff of a new wholesale application.
type WholesaleApplicationNotifyArgs struct {
	CustomerID uuid.UUID `json:"customer_id"`
}

// Kind returns the job kind identifier.
func (WholesaleApplicationNotifyArgs) Kind() string { return "wholesale_application_notify" }

// WholesaleApprovedArgs sends a welcome email to an approved wholesale customer.
type WholesaleApprovedArgs struct {
	CustomerID uuid.UUID `json:"customer_id"`
}

// Kind returns the job kind identifier.
func (WholesaleApprovedArgs) Kind() string { return "wholesale_approved" }

// InvoiceSendArgs sends an invoice to the customer via email.
type InvoiceSendArgs struct {
	InvoiceID uuid.UUID `json:"invoice_id"`
}

// Kind returns the job kind identifier.
func (InvoiceSendArgs) Kind() string { return "invoice_send" }

// MagicLinkSendArgs sends a magic link email to a customer.
type MagicLinkSendArgs struct {
	CustomerID uuid.UUID `json:"customer_id"`
	RawToken   string    `json:"raw_token"`
}

// Kind returns the job kind identifier.
func (MagicLinkSendArgs) Kind() string { return "magic_link_send" }

// R2ImageDeleteArgs deletes an image from Cloudflare R2.
// Enqueued when a product media record is removed from the DB.
type R2ImageDeleteArgs struct {
	R2Key string `json:"r2_key"`
}

// Kind returns the job kind identifier.
func (R2ImageDeleteArgs) Kind() string { return "r2_image_delete" }

// StoreLabelToR2Args fetches a shipping label from the provider URL
// and uploads it to R2 for permanent storage.
type StoreLabelToR2Args struct {
	ShipmentID uuid.UUID `json:"shipment_id"`
	LabelURL   string    `json:"label_url"`
}

// Kind returns the job kind identifier.
func (StoreLabelToR2Args) Kind() string { return "store_label_to_r2" }
