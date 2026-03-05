package payments

// Stripe event types that the application processes.
const (
	EventPaymentIntentSucceeded         = "payment_intent.succeeded"
	EventPaymentIntentPaymentFailed     = "payment_intent.payment_failed"
	EventPaymentIntentCanceled          = "payment_intent.canceled"
	EventPaymentIntentRequiresAction    = "payment_intent.requires_action"
	EventChargeRefunded                 = "charge.refunded"
	EventChargeRefundUpdated            = "charge.refund.updated"
	EventCustomerSubscriptionCreated    = "customer.subscription.created"
	EventCustomerSubscriptionUpdated    = "customer.subscription.updated"
	EventCustomerSubscriptionDeleted    = "customer.subscription.deleted"
	EventInvoicePaid                    = "invoice.paid"
	EventInvoicePaymentFailed           = "invoice.payment_failed"
	EventPaymentMethodAttached          = "payment_method.attached"
	EventPaymentMethodDetached          = "payment_method.detached"
)

// EventCategory groups Stripe events for routing.
type EventCategory string

const (
	EventCategoryPayment      EventCategory = "payment"
	EventCategoryRefund       EventCategory = "refund"
	EventCategorySubscription EventCategory = "subscription"
	EventCategoryInvoice      EventCategory = "invoice"
	EventCategoryUnknown      EventCategory = "unknown"
)

// CategorizeEvent returns the category for a given Stripe event type.
func CategorizeEvent(eventType string) EventCategory {
	switch eventType {
	case EventPaymentIntentSucceeded,
		EventPaymentIntentPaymentFailed,
		EventPaymentIntentCanceled,
		EventPaymentIntentRequiresAction:
		return EventCategoryPayment

	case EventChargeRefunded, EventChargeRefundUpdated:
		return EventCategoryRefund

	case EventCustomerSubscriptionCreated,
		EventCustomerSubscriptionUpdated,
		EventCustomerSubscriptionDeleted:
		return EventCategorySubscription

	case EventInvoicePaid, EventInvoicePaymentFailed:
		return EventCategoryInvoice

	default:
		return EventCategoryUnknown
	}
}
