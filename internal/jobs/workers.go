package jobs

import (
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

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

// RenewalInsertOpts is the only sanctioned way to enqueue a
// SubscriptionRenewalArgs. Every insert site must use it — the scheduler's
// dead-card rung, the staff Retry button, and the customer's own retry.
//
// This is a function rather than a var because River's InsertOpts is a mutable
// struct; handing out a shared pointer would let one caller's edit reach the
// others.
//
// # Why every site has to agree
//
// River derives a job's unique_key by hashing a string assembled from the
// unique options that were *set* (see riverqueue/river internal/dbunique). The
// "&period=" segment is appended only when ByPeriod is non-zero, so opts that
// differ produce different keys and can never collide — two inserts with
// different UniqueOpts do not deduplicate against each other at all, however
// identical their args.
//
// That bit us: the scheduler used ByArgs+ByPeriod while both manual-retry
// buttons used ByArgs alone, so a staff Retry and the scheduler's rung for the
// same subscription each got their own key and both ran. RenewSubscription has
// no period guard, so two jobs mean two PaymentIntents and two renewal orders
// for one billing period — a genuine double charge.
//
// # Why these particular options
//
// ByArgs keys on the subscription ID, which is the whole of the args, so the
// unit of deduplication is "a charge attempt for this subscription".
//
// ByPeriod bounds it to a day. Without it, River's default unique states
// include Completed, so a finished job anywhere inside the retention window
// would swallow a legitimate later attempt — a customer who fixed their card on
// Tuesday could never get a charge because Monday's attempt already existed. A
// day is the right bound because a charge attempt already made today is one
// this subscription has had: riding on it is correct rather than a loss.
//
// The bucket is truncation-based, not rolling, so two attempts either side of
// UTC midnight land in different buckets and both run. That window is about as
// wide as the scheduler's one-minute cadence and needs a human clicking Retry
// inside it; the residual risk is noted in RenewSubscription.
func RenewalInsertOpts() *river.InsertOpts {
	return &river.InsertOpts{UniqueOpts: river.UniqueOpts{
		ByArgs:   true,
		ByPeriod: 24 * time.Hour,
	}}
}

// BatchRenewalArgs triggers a batched renewal for multiple subscriptions
// belonging to the same customer and shipping address.
//
// CustomerID and ShippingAddressID are not read by the worker — RenewBatch
// derives both from the subscriptions themselves and rejects a batch whose
// members disagree. They are here to be the deduplication key, and carrying
// them is what lets that key be the *group* rather than the exact membership
// list. See BatchRenewalInsertOpts.
type BatchRenewalArgs struct {
	SubscriptionIDs   []uuid.UUID `json:"subscription_ids"`
	CustomerID        uuid.UUID   `json:"customer_id"        river:"unique"`
	ShippingAddressID uuid.UUID   `json:"shipping_address_id" river:"unique"`
}

// Kind returns the job kind identifier.
func (BatchRenewalArgs) Kind() string { return "batch_renewal" }

// BatchRenewalInsertOpts is the only sanctioned way to enqueue a
// BatchRenewalArgs, for the same reason RenewalInsertOpts is for the solo path:
// options that differ between insert sites hash to different unique keys and so
// deduplicate against nothing.
//
// # Why this needed uniqueness at all
//
// The batch insert used to pass nil — no uniqueness whatsoever. The scheduler
// runs every minute and ListSubscriptionsDueForRenewal returns anything with
// next_order_at <= now(), and next_order_at only moves once the batch job has
// actually run. So every tick between enqueueing a batch and that batch
// finishing enqueued another one for the same group. Seconds of overlap on a
// good day; on a bad one, a job sitting in retryable with backoff while a fresh
// duplicate is minted every sixty seconds. Each duplicate that runs charges the
// customer and places an order.
//
// # Why the key is the group, not the members
//
// ByArgs would otherwise hash SubscriptionIDs, and that is the wrong key twice
// over. Slice order is not guaranteed stable across runs — the source query
// orders by next_order_at with no tiebreak — and group membership genuinely
// changes between ticks as more subscriptions come due, so [A,B] and [A,B,C]
// would be different keys and both would run, charging A and B twice.
//
// The `river:"unique"` tags on CustomerID and ShippingAddressID make River build
// the key from those two fields alone (see rivershared/structtag), so the unit
// of deduplication is "a batch renewal for this customer at this address" —
// which is exactly what batchKey groups on.
//
// ByPeriod bounds it to a day, matching RenewalInsertOpts. Renewal times are
// anchored to a single hour (anchorRenewalTime), so a group has at most one
// legitimate renewal instant per day and a day-long bucket cannot orphan a
// second batch that should have run. Discarded jobs are outside River's default
// unique states, so a batch that exhausts its retries does not wedge the group
// until midnight.
//
// # The trade-off, so nobody "fixes" it blindly
//
// Completed is one of River's default unique states, so a finished batch keeps
// blocking its group for the rest of the day. If two subscriptions at the same
// customer and address somehow come due *after* today's batch already ran, they
// are skipped and renew tomorrow instead — a day late.
//
// That is the deliberate side of the trade. Dropping Completed from the states
// would close it, at the price of re-enqueueing every sixty seconds after a
// batch is discarded, hammering a renewal that is already failing. Between a
// bounded one-day delay and an unbounded retry storm against Stripe, the delay
// is the one to take — and it needs an unanchored next_order_at plus two
// subscriptions sharing an address to happen at all, since a group of one goes
// down the solo path instead.
func BatchRenewalInsertOpts() *river.InsertOpts {
	return &river.InsertOpts{UniqueOpts: river.UniqueOpts{
		ByArgs:   true,
		ByPeriod: 24 * time.Hour,
	}}
}

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

// ServiceTicketOpenedArgs notifies staff that a wholesale customer reported a
// broken machine from the portal.
//
// Only the ticket id travels: severity is on the ticket, and the worker reads
// the row anyway. The enqueue side takes severity as an argument because it
// decides whether the job may wait for the morning, which is a scheduling
// question rather than a payload one — see Enqueuer.EnqueueServiceTicketOpened.
type ServiceTicketOpenedArgs struct {
	TicketID uuid.UUID `json:"ticket_id"`
}

// Kind returns the job kind identifier.
func (ServiceTicketOpenedArgs) Kind() string { return "service_ticket_opened" }

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

// WhiteLabelInviteArgs emails a white-label onboarding invite to an approved
// wholesale customer.
type WhiteLabelInviteArgs struct {
	CustomerID uuid.UUID `json:"customer_id"`
}

// Kind returns the job kind identifier.
func (WhiteLabelInviteArgs) Kind() string { return "white_label_invite" }

// StaffInviteArgs emails a staff member their invite / password-setup link.
// Enqueued when an admin adds a new team member or resends the invite.
type StaffInviteArgs struct {
	StaffID uuid.UUID `json:"staff_id"`
}

// Kind returns the job kind identifier.
func (StaffInviteArgs) Kind() string { return "staff_invite" }

// WhiteLabelSubmittedArgs notifies staff that a client submitted a white-label
// product for review.
type WhiteLabelSubmittedArgs struct {
	ProductID uuid.UUID `json:"product_id"`
}

// Kind returns the job kind identifier.
func (WhiteLabelSubmittedArgs) Kind() string { return "white_label_submitted" }

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

// PasswordResetSendArgs sends a self-service password reset email to a customer.
// The token is a setup token (magic_link_tokens, 72h) minted in the request tx;
// the email links to /account/password-setup.
type PasswordResetSendArgs struct {
	CustomerID uuid.UUID `json:"customer_id"`
	RawToken   string    `json:"raw_token"`
}

// Kind returns the job kind identifier.
func (PasswordResetSendArgs) Kind() string { return "password_reset_send" }

// CustomerUserInviteSendArgs emails an invite / password-setup link to an
// additional login on a wholesale account. The token is minted in the request
// transaction; the email links to /wholesale/invite.
type CustomerUserInviteSendArgs struct {
	CustomerUserID uuid.UUID `json:"customer_user_id"`
	RawToken       string    `json:"raw_token"`
}

// Kind returns the job kind identifier.
func (CustomerUserInviteSendArgs) Kind() string { return "customer_user_invite_send" }

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

// BuyLabelArgs purchases a shipping label for an order via the configured
// LabelProvider and persists the shipment. Idempotency note: each successful
// run creates a new shipment row; UIs that enqueue this job must guard
// against double-clicks (the order list multi-select uses a confirm dialog;
// the order detail button disables itself when a label already exists).
type BuyLabelArgs struct {
	OrderID     uuid.UUID `json:"order_id"`
	ServiceCode string    `json:"service_code,omitempty"` // "" → provider default (Shippo: usps_ground_advantage)

	// Actor fields are copied into the job args so the worker can record
	// audit entries with the original staff identity even when run later.
	ActorType string     `json:"actor_type"`
	ActorID   *uuid.UUID `json:"actor_id,omitempty"`
	ActorName string     `json:"actor_name"`
}

// Kind returns the job kind identifier.
func (BuyLabelArgs) Kind() string { return "buy_label" }

// PollLabelRefundArgs polls the carrier for the resolution of a label refund
// that was requested via the admin refund flow. Refunds settle asynchronously
// (Shippo takes up to 14 days), so this job snoozes and re-polls until the
// refund reaches a terminal state or the request ages out.
type PollLabelRefundArgs struct {
	ShipmentID uuid.UUID `json:"shipment_id"`
	RefundID   string    `json:"refund_id"`
}

// Kind returns the job kind identifier.
func (PollLabelRefundArgs) Kind() string { return "poll_label_refund" }

// --- QuickBooks integration jobs ---

// EnsureQBCustomerArgs creates or updates a QB customer, then chains to CreateQBInvoice.
type EnsureQBCustomerArgs struct {
	CustomerID uuid.UUID `json:"customer_id"`
	OrderID    uuid.UUID `json:"order_id"`
	// StaffRequested marks a chain a person deliberately started from the
	// admin. It overrides the manual-billing gate — the whole point of Bill
	// now is to invoice an account that nothing invoices on its own — and it
	// overrides nothing else.
	StaffRequested bool `json:"staff_requested,omitempty"`
}

// Kind returns the job kind identifier.
func (EnsureQBCustomerArgs) Kind() string { return "qb_ensure_customer" }

// CreateQBInvoiceArgs creates a QB invoice for a wholesale order.
type CreateQBInvoiceArgs struct {
	OrderID      uuid.UUID `json:"order_id"`
	QBCustomerID string    `json:"qb_customer_id"`
	// CustomerLookupError carries a shadow-mode QBO customer lookup that
	// failed, so the preview can say so. Shadow must still produce a row: an
	// order absent from the review list reads as nothing to bill, which is the
	// one conclusion a proof period must never invite. Empty in live mode,
	// where a failed lookup fails the job instead.
	CustomerLookupError string `json:"customer_lookup_error,omitempty"`
	// StaffRequested carries the Bill now override down the chain: it is what
	// lets a person invoice an account that nothing invoices on its own.
	StaffRequested bool `json:"staff_requested,omitempty"`
}

// Kind returns the job kind identifier.
func (CreateQBInvoiceArgs) Kind() string { return "qb_create_invoice" }

// SendQBInvoiceArgs has QBO email an invoice to the customer. Chained by
// CreateQBInvoice in the transaction that persists the QB invoice ID, so a
// send failure retries independently of (and never re-creates) the invoice.
type SendQBInvoiceArgs struct {
	OrderID     uuid.UUID `json:"order_id"`
	QBInvoiceID string    `json:"qb_invoice_id"`
}

// Kind returns the job kind identifier.
func (SendQBInvoiceArgs) Kind() string { return "qb_send_invoice" }

// EmailQBInvoiceAlertArgs notifies staff that a QB invoicing job failed
// permanently — the order will not be billed until someone intervenes.
type EmailQBInvoiceAlertArgs struct {
	OrderID    uuid.UUID `json:"order_id"`
	FailedKind string    `json:"failed_kind"` // job kind that failed
	Cause      string    `json:"cause"`       // error message for the email body
}

// Kind returns the job kind identifier.
func (EmailQBInvoiceAlertArgs) Kind() string { return "email:qb_invoice_alert" }

// CheckQBTokenArgs runs the daily QuickBooks refresh-token expiry check —
// warns staff before the connection lapses and stalls all invoicing.
type CheckQBTokenArgs struct{}

// Kind returns the job kind identifier.
func (CheckQBTokenArgs) Kind() string { return "qb_token_check" }

// ProcessQBInvoiceUpdateArgs handles a QB webhook notification about an invoice update.
type ProcessQBInvoiceUpdateArgs struct {
	QBInvoiceID string `json:"qb_invoice_id"`
	RealmID     string `json:"realm_id"`
}

// Kind returns the job kind identifier.
func (ProcessQBInvoiceUpdateArgs) Kind() string { return "qb_process_invoice_update" }

// ReconcileQBInvoicesArgs triggers a sweep of open wholesale QB invoices: the
// safety net for missed webhooks plus the overdue detector. Enqueued by a daily
// periodic job; carries no payload.
type ReconcileQBInvoicesArgs struct{}

// Kind returns the job kind identifier.
func (ReconcileQBInvoicesArgs) Kind() string { return "qb_reconcile_invoices" }

// QBShadowDigestArgs asks for the weekly summary of what QuickBooks billing
// would have done while the shop is in shadow mode. Periodic job; carries no
// payload.
type QBShadowDigestArgs struct{}

// Kind returns the job kind identifier.
func (QBShadowDigestArgs) Kind() string { return "qb_shadow_digest" }

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
	Amount    int       `json:"amount"`     // payment amount in cents
	Method    string    `json:"method"`     // check, cash, other
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

// EmailInvoicePastDueArgs sends a past-due reminder for an overdue wholesale
// invoice. Stage is which reminder this is (1 on going overdue, then weekly);
// it is part of the args so the reconcile's ByArgs uniqueness guarantees one
// email per stage. DueDate is QB's authoritative due date, carried through so
// the email displays the date the invoice was actually issued under.
type EmailInvoicePastDueArgs struct {
	OrderID    uuid.UUID `json:"order_id"`
	CustomerID uuid.UUID `json:"customer_id"`
	Stage      int       `json:"stage"`
	DueDate    time.Time `json:"due_date"`
}

// Kind returns the job kind identifier.
func (EmailInvoicePastDueArgs) Kind() string { return "email:invoice_past_due" }

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
//
// One job kind covers every rung of the dunning ladder; Stage picks the
// template. Following the same shape as EmailInvoicePastDueArgs keeps three
// near-identical workers from existing, and means a change to how past-due mail
// is rendered lands in one place.
type SubscriptionPastDueArgs struct {
	SubscriptionID uuid.UUID `json:"subscription_id"`
	CustomerID     uuid.UUID `json:"customer_id"`
	// Stage is which notice this is — see the SubscriptionPastDueStage
	// constants. A zero value (a job enqueued before the ladder existed) renders
	// as the first notice.
	Stage int `json:"stage"`
}

// Stage values for SubscriptionPastDueArgs. These are persisted in job args and
// mirrored by the app layer's ladder, so append — never renumber.
const (
	// SubscriptionPastDueStageFirst is the notice sent on the first decline.
	// The payment-failed webhook uses it directly; it is always a customer's
	// first word that a charge failed.
	SubscriptionPastDueStageFirst = 1
	// SubscriptionPastDueStageReminder is the mid-window "shipment on hold".
	SubscriptionPastDueStageReminder = 2
	// SubscriptionPastDueStageFinal is the last warning before closeout.
	SubscriptionPastDueStageFinal = 3
)

// Kind returns the job kind identifier.
func (SubscriptionPastDueArgs) Kind() string { return "email:subscription_past_due" }

// SubscriptionCancelledArgs sends a cancellation confirmation to the customer.
type SubscriptionCancelledArgs struct {
	SubscriptionID uuid.UUID `json:"subscription_id"`
	CustomerID     uuid.UUID `json:"customer_id"`
}

// Kind returns the job kind identifier.
func (SubscriptionCancelledArgs) Kind() string { return "email:subscription_cancelled" }

// SubscriptionSkippedArgs tells the customer their next shipment was skipped,
// when the following one bills, and how to undo it. SkippedCount is the number
// of shipments skipped, or 0 when the customer named a restart date instead.
type SubscriptionSkippedArgs struct {
	SubscriptionID uuid.UUID `json:"subscription_id"`
	CustomerID     uuid.UUID `json:"customer_id"`
	SkippedCount   int       `json:"skipped_count"`
}

// Kind returns the job kind identifier.
func (SubscriptionSkippedArgs) Kind() string { return "email:subscription_skipped" }

// SubscriptionSkipUndoneArgs tells the customer a staff member reversed a skip,
// so their next shipment bills sooner than the last message told them.
// SkippedTo is carried in the args because undoing clears the record of it.
type SubscriptionSkipUndoneArgs struct {
	SubscriptionID uuid.UUID `json:"subscription_id"`
	CustomerID     uuid.UUID `json:"customer_id"`
	SkippedTo      time.Time `json:"skipped_to"`
}

// Kind returns the job kind identifier.
func (SubscriptionSkipUndoneArgs) Kind() string { return "email:subscription_skip_undone" }

// SubscriptionDunningEndedArgs sends the "subscription ended" notice after
// dunning retries are exhausted and the subscription is expired.
type SubscriptionDunningEndedArgs struct {
	SubscriptionID uuid.UUID `json:"subscription_id"`
	CustomerID     uuid.UUID `json:"customer_id"`
}

// Kind returns the job kind identifier.
func (SubscriptionDunningEndedArgs) Kind() string { return "email:subscription_ended" }

// OrderShippedEmailArgs sends an "order shipped" notification with tracking.
// Enqueued in the same transaction as the shipment insert that triggered it.
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
