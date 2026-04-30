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

// AbandonedOrderCleanupArgs scans for pre-paid-intent orders (status=pending,
// payment_status=awaiting) that have been sitting unconfirmed for longer
// than the configured threshold and cancels them.
type AbandonedOrderCleanupArgs struct{}

// Kind returns the job kind identifier.
func (AbandonedOrderCleanupArgs) Kind() string { return "abandoned_order_cleanup" }

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

// WholesaleSuspendedArgs sends a suspension notification email to a wholesale customer.
type WholesaleSuspendedArgs struct {
	CustomerID uuid.UUID `json:"customer_id"`
}

// Kind returns the job kind identifier.
func (WholesaleSuspendedArgs) Kind() string { return "wholesale_suspended" }

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
	Next       string    `json:"next,omitempty"`
}

// Kind returns the job kind identifier.
func (MagicLinkSendArgs) Kind() string { return "magic_link_send" }

// EmailVerifySendArgs sends an email-verification message to a customer.
// The underlying token is a magic-link token; redeeming it verifies the
// email and signs the customer in. The email copy is framed around
// verification rather than sign-in.
type EmailVerifySendArgs struct {
	CustomerID uuid.UUID `json:"customer_id"`
	RawToken   string    `json:"raw_token"`
	Next       string    `json:"next,omitempty"`
}

// Kind returns the job kind identifier.
func (EmailVerifySendArgs) Kind() string { return "email_verify_send" }

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

// --- QuickBooks integration jobs ---

// EnsureQBCustomerArgs creates or updates a QB customer, then chains to CreateQBInvoice.
type EnsureQBCustomerArgs struct {
	CustomerID uuid.UUID `json:"customer_id"`
	OrderID    uuid.UUID `json:"order_id"`
}

// Kind returns the job kind identifier.
func (EnsureQBCustomerArgs) Kind() string { return "qb_ensure_customer" }

// CreateQBInvoiceArgs creates a QB invoice for a wholesale order.
type CreateQBInvoiceArgs struct {
	OrderID      uuid.UUID `json:"order_id"`
	QBCustomerID string    `json:"qb_customer_id"`
}

// Kind returns the job kind identifier.
func (CreateQBInvoiceArgs) Kind() string { return "qb_create_invoice" }

// ProcessQBInvoiceUpdateArgs handles a QB webhook notification about an invoice update.
type ProcessQBInvoiceUpdateArgs struct {
	QBInvoiceID string `json:"qb_invoice_id"`
	RealmID     string `json:"realm_id"`
}

// Kind returns the job kind identifier.
func (ProcessQBInvoiceUpdateArgs) Kind() string { return "qb_process_invoice_update" }

// SyncQBCustomerArgs syncs customer details to QB (triggered by profile updates).
type SyncQBCustomerArgs struct {
	CustomerID uuid.UUID `json:"customer_id"`
}

// Kind returns the job kind identifier.
func (SyncQBCustomerArgs) Kind() string { return "qb_sync_customer" }

// SyncQBPaymentArgs records a manual payment in QBO against the linked invoice.
type SyncQBPaymentArgs struct {
	OrderID   uuid.UUID `json:"order_id"`
	InvoiceID uuid.UUID `json:"invoice_id"` // Hiri invoice ID
	Amount    int       `json:"amount"`      // payment amount in cents
	Method    string    `json:"method"`      // check, cash, other
	Reference string    `json:"reference,omitempty"`
}

// Kind returns the job kind identifier.
func (SyncQBPaymentArgs) Kind() string { return "qb_sync_payment" }

// EmailInvoicePaidArgs sends a payment confirmation email for a QB/ACH payment.
type EmailInvoicePaidArgs struct {
	OrderID    uuid.UUID `json:"order_id"`
	CustomerID uuid.UUID `json:"customer_id"`
}

// Kind returns the job kind identifier.
func (EmailInvoicePaidArgs) Kind() string { return "email:invoice_paid" }

// SubscriptionRenewalReceiptArgs sends a renewal receipt for a subscription
// renewal order — fires after the off-session charge succeeds and the order
// is created.
type SubscriptionRenewalReceiptArgs struct {
	OrderID    uuid.UUID `json:"order_id"`
	CustomerID uuid.UUID `json:"customer_id"`
}

// Kind returns the job kind identifier.
func (SubscriptionRenewalReceiptArgs) Kind() string { return "email:subscription_renewal_receipt" }

// SubscriptionPastDueArgs sends a payment-failed / past-due notice to the
// customer asking them to update their card.
type SubscriptionPastDueArgs struct {
	SubscriptionID uuid.UUID `json:"subscription_id"`
	CustomerID     uuid.UUID `json:"customer_id"`
}

// Kind returns the job kind identifier.
func (SubscriptionPastDueArgs) Kind() string { return "email:subscription_past_due" }

// SubscriptionCancelledArgs sends a cancellation confirmation to the customer.
type SubscriptionCancelledArgs struct {
	SubscriptionID uuid.UUID `json:"subscription_id"`
	CustomerID     uuid.UUID `json:"customer_id"`
}

// Kind returns the job kind identifier.
func (SubscriptionCancelledArgs) Kind() string { return "email:subscription_cancelled" }

// OrderShippedEmailArgs sends an "order shipped" notification with tracking.
// Enqueued in the same transaction as the shipment insert during a Pirate
// Ship CSV tracking import.
type OrderShippedEmailArgs struct {
	OrderID    uuid.UUID `json:"order_id"`
	CustomerID uuid.UUID `json:"customer_id"`
	ShipmentID uuid.UUID `json:"shipment_id"`
}

// Kind returns the job kind identifier.
func (OrderShippedEmailArgs) Kind() string { return "email:order_shipped" }

// OrderReadyForPickupEmailArgs sends a "your order is ready" notification for
// pickup orders. Enqueued from MarkReadyForPickup so the email rides on the
// same transaction as the fulfillment status flip.
type OrderReadyForPickupEmailArgs struct {
	OrderID    uuid.UUID `json:"order_id"`
	CustomerID uuid.UUID `json:"customer_id"`
}

// Kind returns the job kind identifier.
func (OrderReadyForPickupEmailArgs) Kind() string { return "email:order_ready_for_pickup" }

// OrderOutForDeliveryEmailArgs sends an "out for local delivery today"
// notification. Enqueued from MarkOutForDelivery the day staff dispatches
// the local-delivery route.
type OrderOutForDeliveryEmailArgs struct {
	OrderID    uuid.UUID `json:"order_id"`
	CustomerID uuid.UUID `json:"customer_id"`
}

// Kind returns the job kind identifier.
func (OrderOutForDeliveryEmailArgs) Kind() string { return "email:order_out_for_delivery" }

// RefundConfirmationArgs sends a refund-issued confirmation. The amount is the
// refunded total in cents (may be partial).
type RefundConfirmationArgs struct {
	OrderID      uuid.UUID `json:"order_id"`
	CustomerID   uuid.UUID `json:"customer_id"`
	RefundAmount int       `json:"refund_amount"`
}

// Kind returns the job kind identifier.
func (RefundConfirmationArgs) Kind() string { return "email:refund_confirmation" }
