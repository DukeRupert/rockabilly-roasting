package audit

// Audit action constants — namespaced as "resource.verb".
const (
	// Order actions
	AuditOrderCreated                = "order.created"
	AuditOrderStatusChanged          = "order.status_changed"
	AuditOrderRefunded               = "order.refunded"
	AuditOrderCancelled              = "order.cancelled"
	AuditOrderFulfilled              = "order.fulfilled"
	AuditOrderShipped                = "order.shipped"
	AuditOrderDelivered              = "order.delivered"
	AuditOrderFulfillmentReverted    = "order.fulfillment_reverted"
	AuditOrderShipmentReverted       = "order.shipment_reverted"
	AuditOrderReadyForPickup         = "order.ready_for_pickup"
	AuditOrderPickedUp               = "order.picked_up"
	AuditOrderOutForDelivery         = "order.out_for_delivery"
	AuditOrderLineItemVariantChanged = "order.line_item_variant_changed"
	AuditOrderShippingMethodChanged  = "order.shipping_method_changed"

	// Product/pricing actions
	AuditProductCreated      = "product.created"
	AuditProductCloned       = "product.cloned"
	AuditProductUpdated      = "product.updated"
	AuditProductArchived     = "product.archived"
	AuditProductDeleted      = "product.deleted"
	AuditVariantCreated      = "variant.created"
	AuditVariantUpdated      = "variant.updated"
	AuditVariantArchived     = "variant.archived"
	AuditVariantUnarchived   = "variant.unarchived"
	AuditVariantDeleted      = "variant.deleted"
	AuditVariantPriceUpdated = "variant.price_updated"

	// Subscription actions
	AuditSubscriptionCreated               = "subscription.created"
	AuditSubscriptionPaused                = "subscription.paused"
	AuditSubscriptionCancelled             = "subscription.cancelled"
	AuditSubscriptionRenewed               = "subscription.renewed"
	AuditSubscriptionResumed               = "subscription.resumed"
	AuditSubscriptionFailed                = "subscription.renewal_failed"
	AuditSubscriptionExpired               = "subscription.expired"
	AuditSubscriptionShippingGrandfathered = "subscription.shipping_grandfathered_changed"
	AuditSubscriptionVariantChanged        = "subscription.variant_changed"
	AuditSubscriptionPlanChanged           = "subscription.plan_changed"
	AuditSubscriptionDunningAck            = "subscription.dunning_acknowledged"
	AuditPlanCreated                       = "subscription_plan.created"
	AuditPlanDeactivated                   = "subscription_plan.deactivated"
	AuditPlanActivated                     = "subscription_plan.activated"
	AuditPlanUpdated                       = "subscription_plan.updated"

	// Customer actions (staff-initiated)
	AuditCustomerGroupChanged            = "customer.group_changed"
	AuditCustomerTaxExemptionGranted     = "customer.tax_exemption_granted"
	AuditCustomerTaxExemptionRevoked     = "customer.tax_exemption_revoked"
	AuditCustomerDeactivated             = "customer.deactivated"
	AuditCustomerAddressAdded            = "customer.address_added"
	AuditCustomerAddressUpdated          = "customer.address_updated"
	AuditCustomerAddressDeleted          = "customer.address_deleted"
	AuditCustomerPaymentTermsUpdated     = "customer.payment_terms_updated"
	AuditCustomerBillingMethodUpdated    = "customer.billing_method_updated"
	AuditCustomerLocalFulfillmentUpdated = "customer.local_fulfillment_updated"
	AuditCustomerStripeIDLinked          = "customer.stripe_customer_id_linked"
	AuditCustomerPriceListUpdated        = "customer.price_list_updated"

	// Customer group actions
	AuditCustomerGroupCreated       = "customer_group.created"
	AuditCustomerGroupDeleted       = "customer_group.deleted"
	AuditCustomerGroupMemberAdded   = "customer_group.member_added"
	AuditCustomerGroupMemberRemoved = "customer_group.member_removed"

	// Price list actions
	AuditPriceListCreated                 = "price_list.created"
	AuditPriceListUpdated                 = "price_list.updated"
	AuditPriceListDeleted                 = "price_list.deleted"
	AuditDefaultWholesalePriceListUpdated = "price_list.default_wholesale_updated"

	// Customer auth actions
	AuditCustomerLogin           = "customer.login"
	AuditCustomerLogout          = "customer.logout"
	AuditCustomerPasswordSet     = "customer.password_set"
	AuditCustomerPasswordChanged = "customer.password_changed"
	AuditCustomerEmailVerified   = "customer.email_verified"
	AuditCustomerPhoneUpdated    = "customer.phone_updated"

	// Staff actions
	AuditStaffCreated     = "staff.created"
	AuditStaffRoleChanged = "staff.role_changed"
	AuditStaffDeactivated = "staff.deactivated"
	AuditStaffLogin       = "staff.login"
	AuditStaffLogout      = "staff.logout"

	// Shipping actions
	AuditShipmentLabelCreated  = "shipment.label_created"
	AuditShipmentStatusUpdated = "shipment.status_updated"
	AuditShipmentImported      = "shipment.imported"
	AuditShippingConfigUpdated = "shipping_config.updated"
	AuditBoxPresetCreated      = "box_preset.created"
	AuditBoxPresetUpdated      = "box_preset.updated"
	AuditBoxPresetDeleted      = "box_preset.deleted"

	// Email actions for shipping
	AuditEmailOrderShipped        = "email.order_shipped"
	AuditEmailOrderReadyForPickup = "email.order_ready_for_pickup"
	AuditEmailOrderOutForDelivery = "email.order_out_for_delivery"

	// Discount actions
	AuditDiscountCreated     = "discount.created"
	AuditDiscountUpdated     = "discount.updated"
	AuditDiscountDeactivated = "discount.deactivated"

	// Wholesale customer actions
	AuditWholesaleApplicationApproved = "wholesale.application_approved"
	AuditWholesaleApplicationDeclined = "wholesale.application_declined"
	AuditWholesaleAccountSuspended    = "wholesale.account_suspended"
	AuditWholesaleAccountReactivated  = "wholesale.account_reactivated"

	// White-label onboarding actions
	AuditWhiteLabelInviteSent = "white_label.invite_sent"
	AuditWhiteLabelSubmitted  = "white_label.submitted"

	// Product visibility actions
	AuditProductVisibilityUpdated     = "product.visibility_updated"
	AuditProductGroupAccessUpdated    = "product.group_access_updated"
	AuditProductCustomerAccessUpdated = "product.customer_access_updated"

	// Product media actions
	AuditProductMediaAdded   = "product_media.added"
	AuditProductMediaDeleted = "product_media.deleted"

	// Invoice actions
	AuditInvoiceCreated         = "invoice.created"
	AuditInvoiceSent            = "invoice.sent"
	AuditInvoiceVoided          = "invoice.voided"
	AuditInvoicePaymentRecorded = "invoice.payment_recorded"

	// QuickBooks integration actions
	AuditQBCustomerCreated         = "qb.customer_created"
	AuditQBCustomerLinked          = "qb.customer_linked"
	AuditQBCustomerSynced          = "qb.customer_synced"
	AuditQBInvoiceCreated          = "qb.invoice_created"
	AuditQBPaymentSynced           = "qb.payment_synced"
	AuditQBConnected               = "qb.connected"
	AuditQBDisconnected            = "qb.disconnected"
	AuditQBInvoiceVoided           = "qb.invoice_voided"
	AuditOrderPaymentCaptured      = "order.payment_captured"
	AuditOrderPaymentInvoiced      = "order.payment_invoiced"
	AuditOrderPaymentPartiallyPaid = "order.payment_partially_paid"
	AuditOrderPaymentOverdue       = "order.payment_overdue"
	// AuditOrderMarkedPaid is a staff manual override capturing payment outside
	// the automated QB reconcile path (AuditOrderPaymentCaptured); kept distinct
	// so manual money-state changes are greppable in the audit log.
	AuditOrderMarkedPaid = "order.marked_paid"

	// Email actions (audited on successful send)
	AuditEmailMagicLinkSent                = "email.magic_link_sent"
	AuditEmailPasswordSetupSent            = "email.password_setup_sent"
	AuditEmailVerificationSent             = "email.verification_sent"
	AuditEmailOrderConfirmed               = "email.order_confirmed"
	AuditEmailSubscriptionConfirmed        = "email.subscription_confirmed"
	AuditEmailSubscriptionRenewed          = "email.subscription_renewed"
	AuditEmailSubscriptionPastDue          = "email.subscription_past_due"
	AuditEmailSubscriptionCancelled        = "email.subscription_cancelled"
	AuditEmailSubscriptionEnded            = "email.subscription_ended"
	AuditEmailRefundIssued                 = "email.refund_issued"
	AuditEmailInvoiceSent                  = "email.invoice_sent"
	AuditEmailInvoicePaid                  = "email.invoice_paid"
	AuditEmailInvoicePastDue               = "email.invoice_past_due"
	AuditEmailWholesaleApplicationReceived = "email.wholesale_application_received"
	AuditEmailWholesaleApproved            = "email.wholesale_approved"
	AuditEmailWholesaleMigrated            = "email.wholesale_migrated"
	AuditEmailWholesaleSuspended           = "email.wholesale_suspended"
	AuditEmailWhiteLabelInvite             = "email.white_label_invite"
	AuditEmailWhiteLabelSubmitted          = "email.white_label_submitted"

	// Attribute actions
	AuditAttributeSetCreated      = "attribute_set.created"
	AuditAttributeSetUpdated      = "attribute_set.updated"
	AuditAttributeSetDeleted      = "attribute_set.deleted"
	AuditAttributeKeyCreated      = "attribute_key.created"
	AuditAttributeKeyUpdated      = "attribute_key.updated"
	AuditAttributeKeyDeleted      = "attribute_key.deleted"
	AuditProductAttributesUpdated = "product.attributes_updated"
)
