package audit

// Audit action constants — namespaced as "resource.verb".
const (
	// Order actions
	// Background jobs — an operator handing a discarded job back to River.
	AuditJobRetried = "job.retried"

	// Equipment service — the machines a shop maintains for its customers.
	// Retiring and returning to service get their own actions rather than one
	// status_changed: the timeline's label and marker are chosen from the
	// action string alone, so a generic verb could not colour a retirement
	// differently from a machine coming back.
	AuditEquipmentCreated           = "equipment.created"
	AuditEquipmentUpdated           = "equipment.updated"
	AuditEquipmentSentToShop        = "equipment.sent_to_shop"
	AuditEquipmentReturnedToService = "equipment.returned_to_service"
	AuditEquipmentRetired           = "equipment.retired"

	// Optional feature modules — turning a whole section of the app on or off.
	// Worth an audit entry because enabling one can start sending customer mail
	// nobody was expecting, and "who did this" is the first question asked.
	AuditModuleEnabled  = "module.enabled"
	AuditModuleDisabled = "module.disabled"

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
	AuditOrderSwitchedToPickup       = "order.switched_to_pickup"
	AuditOrderInternalNoteUpdated    = "order.internal_note_updated"

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
	AuditSubscriptionSkipped               = "subscription.skipped"
	AuditSubscriptionSkipUndone            = "subscription.skip_undone"
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
	AuditCustomerEmailUpdated            = "customer.email_updated"
	AuditCustomerOrderRemindersEnabled   = "customer.order_reminders_enabled"
	AuditCustomerOrderRemindersDisabled  = "customer.order_reminders_disabled"
	AuditCustomerAnnouncementsEnabled    = "customer.announcements_enabled"
	AuditCustomerAnnouncementsDisabled   = "customer.announcements_disabled"

	// Customer user actions (additional logins on a wholesale account).
	// These record against the ACCOUNT (resource_type "customer",
	// resource_id = customers.id) with the affected login in
	// metadata.customer_user_id, so an account's audit trail stays in one place.
	AuditCustomerUserInvited               = "customer_user.invited"
	AuditCustomerUserInviteResent          = "customer_user.invite_resent"
	AuditCustomerUserRevoked               = "customer_user.revoked"
	AuditCustomerUserPasswordSet           = "customer_user.password_set"
	AuditCustomerUserPasswordChanged       = "customer_user.password_changed"
	AuditCustomerUserNotificationsEnabled  = "customer_user.notifications_enabled"
	AuditCustomerUserNotificationsDisabled = "customer_user.notifications_disabled"

	// Customer group actions. Customer groups have been retired — nothing writes
	// these any more, but historical audit rows carry them, so the constants stay
	// for the reader that renders those rows.
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
	AuditStaffActivated   = "staff.activated"
	AuditStaffPasswordSet = "staff.password_set"
	AuditStaffLogin       = "staff.login"
	AuditStaffLogout      = "staff.logout"

	// Shipping actions
	AuditShipmentLabelCreated         = "shipment.label_created"
	AuditShipmentLabelRefundRequested = "shipment.label_refund_requested"
	AuditShipmentLabelRefunded        = "shipment.label_refunded"
	AuditShipmentLabelRefundFailed    = "shipment.label_refund_failed"
	AuditShipmentStatusUpdated        = "shipment.status_updated"
	AuditShipmentImported             = "shipment.imported"
	AuditShippingConfigUpdated        = "shipping_config.updated"
	AuditDeliveryRunPostponed         = "delivery_run.postponed"
	AuditDeliveryRunRestored          = "delivery_run.restored"
	AuditBoxPresetCreated             = "box_preset.created"
	AuditBoxPresetUpdated             = "box_preset.updated"
	AuditBoxPresetDeleted             = "box_preset.deleted"

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
	AuditWhiteLabelReassigned = "white_label.base_reassigned"

	// Product visibility actions
	AuditProductVisibilityUpdated = "product.visibility_updated"
	// Retired with customer groups; kept for historical rows.
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
	AuditQBInvoiceEmailed          = "qb.invoice_emailed"
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
	AuditEmailSubscriptionSkipped          = "email.subscription_skipped"
	AuditEmailSubscriptionSkipUndone       = "email.subscription_skip_undone"
	AuditEmailSubscriptionEnded            = "email.subscription_ended"
	AuditEmailRefundIssued                 = "email.refund_issued"
	AuditEmailInvoiceSent                  = "email.invoice_sent"
	AuditEmailInvoicePaid                  = "email.invoice_paid"
	AuditEmailInvoicePastDue               = "email.invoice_past_due"
	AuditEmailQBInvoiceAlert               = "email.qb_invoice_alert"
	AuditEmailQBTokenAlert                 = "email.qb_token_alert"
	AuditEmailWholesaleApplicationReceived = "email.wholesale_application_received"
	AuditEmailWholesaleApproved            = "email.wholesale_approved"
	AuditEmailWholesaleMigrated            = "email.wholesale_migrated"
	AuditEmailWholesaleSuspended           = "email.wholesale_suspended"
	AuditEmailWhiteLabelInvite             = "email.white_label_invite"
	AuditEmailWhiteLabelSubmitted          = "email.white_label_submitted"
	AuditEmailStaffInvite                  = "email.staff_invite"
	AuditEmailOrderReminderSent            = "email.order_reminder_sent"
	AuditEmailWholesaleNoticeSent          = "email.wholesale_notice_sent"
	AuditEmailAnnouncementSent             = "email.announcement_sent"
	AuditEmailAnnouncementTestSent         = "email.announcement_test_sent"

	// Announcement actions — the notice itself, recorded against the
	// announcement row. The per-recipient sends are the email.* actions above,
	// recorded against each customer, so "what did we send this account" and
	// "who did this notice reach" are both answerable.
	AuditAnnouncementScheduled  = "announcement.scheduled"
	AuditAnnouncementCancelled  = "announcement.cancelled"
	AuditAnnouncementDispatched = "announcement.dispatched"

	// Attribute actions
	AuditAttributeSetCreated      = "attribute_set.created"
	AuditAttributeSetUpdated      = "attribute_set.updated"
	AuditAttributeSetDeleted      = "attribute_set.deleted"
	AuditAttributeKeyCreated      = "attribute_key.created"
	AuditAttributeKeyUpdated      = "attribute_key.updated"
	AuditAttributeKeyDeleted      = "attribute_key.deleted"
	AuditProductAttributesUpdated = "product.attributes_updated"

	// Delivery route actions
	AuditRoutePlanned     = "delivery_route.planned"
	AuditRouteActivated   = "delivery_route.activated"
	AuditRouteCompleted   = "delivery_route.completed"
	AuditRouteStopRemoved = "delivery_route.stop_removed"
	AuditRouteStopSkipped = "delivery_route.stop_skipped"
)
