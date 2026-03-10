package audit

// Audit action constants — namespaced as "resource.verb".
const (
	// Order actions
	AuditOrderCreated       = "order.created"
	AuditOrderStatusChanged = "order.status_changed"
	AuditOrderRefunded      = "order.refunded"
	AuditOrderCancelled     = "order.cancelled"
	AuditOrderFulfilled     = "order.fulfilled"
	AuditOrderShipped       = "order.shipped"

	// Product/pricing actions
	AuditProductCreated      = "product.created"
	AuditProductUpdated      = "product.updated"
	AuditProductArchived     = "product.archived"
	AuditProductDeleted      = "product.deleted"
	AuditVariantCreated      = "variant.created"
	AuditVariantUpdated      = "variant.updated"
	AuditVariantDeleted      = "variant.deleted"
	AuditVariantPriceUpdated = "variant.price_updated"

	// Subscription actions
	AuditSubscriptionCreated   = "subscription.created"
	AuditSubscriptionPaused    = "subscription.paused"
	AuditSubscriptionCancelled = "subscription.cancelled"
	AuditSubscriptionRenewed   = "subscription.renewed"
	AuditSubscriptionResumed   = "subscription.resumed"
	AuditSubscriptionFailed    = "subscription.renewal_failed"
	AuditPlanCreated           = "subscription_plan.created"
	AuditPlanDeactivated       = "subscription_plan.deactivated"
	AuditPlanActivated         = "subscription_plan.activated"
	AuditPlanUpdated           = "subscription_plan.updated"

	// Customer actions (staff-initiated)
	AuditCustomerGroupChanged        = "customer.group_changed"
	AuditCustomerTaxExemptionGranted = "customer.tax_exemption_granted"
	AuditCustomerTaxExemptionRevoked = "customer.tax_exemption_revoked"
	AuditCustomerDeactivated         = "customer.deactivated"
	AuditCustomerAddressAdded        = "customer.address_added"
	AuditCustomerAddressUpdated      = "customer.address_updated"
	AuditCustomerAddressDeleted      = "customer.address_deleted"

	// Customer auth actions
	AuditCustomerLogin  = "customer.login"
	AuditCustomerLogout = "customer.logout"

	// Staff actions
	AuditStaffCreated     = "staff.created"
	AuditStaffRoleChanged = "staff.role_changed"
	AuditStaffDeactivated = "staff.deactivated"
	AuditStaffLogin       = "staff.login"
	AuditStaffLogout      = "staff.logout"

	// Shipping actions
	AuditShipmentLabelCreated  = "shipment.label_created"
	AuditShippingConfigUpdated = "shipping_config.updated"

	// Discount actions
	AuditDiscountCreated     = "discount.created"
	AuditDiscountUpdated     = "discount.updated"
	AuditDiscountDeactivated = "discount.deactivated"

	// Wholesale customer actions
	AuditWholesaleApplicationApproved = "wholesale.application_approved"
	AuditWholesaleApplicationDeclined = "wholesale.application_declined"
	AuditWholesaleAccountSuspended    = "wholesale.account_suspended"
	AuditWholesaleAccountReactivated  = "wholesale.account_reactivated"

	// Product visibility actions
	AuditProductVisibilityUpdated = "product.visibility_updated"

	// Product media actions
	AuditProductMediaAdded   = "product_media.added"
	AuditProductMediaDeleted = "product_media.deleted"

	// Invoice actions
	AuditInvoiceCreated         = "invoice.created"
	AuditInvoiceSent            = "invoice.sent"
	AuditInvoiceVoided          = "invoice.voided"
	AuditInvoicePaymentRecorded = "invoice.payment_recorded"

	// QuickBooks integration actions
	AuditQBCustomerCreated    = "qb.customer_created"
	AuditQBCustomerSynced     = "qb.customer_synced"
	AuditQBInvoiceCreated     = "qb.invoice_created"
	AuditOrderPaymentCaptured = "order.payment_captured"

	// Attribute actions
	AuditAttributeSetCreated      = "attribute_set.created"
	AuditAttributeSetUpdated      = "attribute_set.updated"
	AuditAttributeSetDeleted      = "attribute_set.deleted"
	AuditProductAttributesUpdated = "product.attributes_updated"
)
