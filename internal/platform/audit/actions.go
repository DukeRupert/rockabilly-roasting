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

	// Customer actions (staff-initiated)
	AuditCustomerGroupChanged        = "customer.group_changed"
	AuditCustomerTaxExemptionGranted = "customer.tax_exemption_granted"
	AuditCustomerTaxExemptionRevoked = "customer.tax_exemption_revoked"
	AuditCustomerDeactivated         = "customer.deactivated"

	// Staff actions
	AuditStaffCreated     = "staff.created"
	AuditStaffRoleChanged = "staff.role_changed"
	AuditStaffDeactivated = "staff.deactivated"

	// Shipping actions
	AuditShipmentLabelCreated  = "shipment.label_created"
	AuditShippingConfigUpdated = "shipping_config.updated"

	// Discount actions
	AuditDiscountCreated     = "discount.created"
	AuditDiscountUpdated     = "discount.updated"
	AuditDiscountDeactivated = "discount.deactivated"
)
