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

	// Service tickets. Status moves are one action carrying from/to in
	// metadata, unlike equipment: a ticket has seven states and naming each
	// transition would be twenty-odd constants for a timeline that reads the
	// same either way. Resolved is split out because it is the one the marker
	// colours differently.
	AuditServiceTicketOpened    = "service_ticket.opened"
	AuditServiceTicketAssigned  = "service_ticket.assigned"
	AuditServiceTicketStatus    = "service_ticket.status_changed"
	AuditServiceTicketResolved  = "service_ticket.resolved"
	AuditServiceTicketReopened  = "service_ticket.reopened"
	AuditServiceTicketCancelled = "service_ticket.cancelled"
	AuditServiceTicketNoteAdded = "service_ticket.note_added"
	// Parts and hours. Removals are audited as loudly as additions: a part
	// line or a stint deleted after the fact changes what a repair appears to
	// have cost, and that is exactly the kind of edit somebody will later want
	// to attribute.
	AuditServicePartAdded   = "service_ticket.part_added"
	AuditServicePartStatus  = "service_ticket.part_status_changed"
	AuditServicePartRemoved = "service_ticket.part_removed"
	AuditServiceTimeLogged  = "service_ticket.time_logged"
	AuditServiceTimeRemoved = "service_ticket.time_removed"
	// AuditServiceTimeRepriced records one hour being priced by hand. Rates are
	// snapshotted onto the entry, so this is the only way a past hour's cost
	// changes — and the record of who decided it.
	AuditServiceTimeRepriced = "service_ticket.time_repriced"
	// AuditServiceStaleSwept records that the daily sweep sent a digest. Not a
	// per-ticket event — it names how many were quiet, so an unanswered ticket
	// can later be shown to have been reported and ignored rather than missed.
	AuditServiceStaleSwept = "service_ticket.stale_swept"
	// AuditServiceTicketNotified records that the crew were mailed about a
	// customer's report. It sits on the ticket's own timeline so "we were never
	// told" can be answered with a time, which is the whole argument a shop has
	// when a cafe is angry about a machine that stayed broken.
	AuditServiceTicketNotified = "service_ticket.staff_notified"

	// Preventive maintenance. Plans are shop-wide templates, so plan and task
	// edits are recorded against the plan; everything that happens to a
	// particular machine is recorded against the equipment, which is where
	// somebody looking into "why was this serviced late" will actually be.
	AuditServicePlanCreated     = "service_plan.created"
	AuditServicePlanUpdated     = "service_plan.updated"
	AuditServicePlanDeleted     = "service_plan.deleted"
	AuditServicePlanTaskAdded   = "service_plan.task_added"
	AuditServicePlanTaskUpdated = "service_plan.task_updated"
	AuditServicePlanTaskRemoved = "service_plan.task_removed"
	// AuditServicePlanTaskRetired is the other way a task leaves a plan: it had
	// history, so deleting it would have taken completed visits with it. The
	// task stops generating work and everything it already produced stays.
	AuditServicePlanTaskRetired = "service_plan.task_retired"

	AuditServicePlanAssigned          = "equipment.plan_assigned"
	AuditServicePlanAssignmentUpdated = "equipment.plan_assignment_updated"
	AuditServicePlanUnassigned        = "equipment.plan_unassigned"
	AuditMaintenanceCompleted         = "equipment.maintenance_completed"
	AuditMaintenanceSkipped           = "equipment.maintenance_skipped"
	// AuditMaintenanceBooked records the sweep opening a routine ticket for a
	// contract customer's due maintenance. Recorded because a ticket appearing
	// with no human behind it needs something, somewhere, saying why — and it
	// goes on the machine, with the rest of that machine's story, rather than
	// on the ticket it created.
	AuditMaintenanceBooked = "equipment.maintenance_booked"
	// AuditMaintenanceSwept records that the daily sweep ran and what it did.
	// Not a per-machine event: one row a day, so a quiet due list can be told
	// apart from a job that stopped running.
	AuditMaintenanceSwept = "equipment.maintenance_swept"

	// AuditServiceLaborRatesUpdated records a change to what an hour of the
	// crew's time costs.
	//
	// Since migration 083 the rate is snapshotted onto each time entry, so this
	// no longer explains a moved cost figure — nothing moves. It explains the
	// opposite: why two hours logged a week apart cost different amounts, and
	// who decided the new number.
	AuditServiceLaborRatesUpdated = "service.labor_rates_updated"

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
	AuditQBCustomerCreated = "qb.customer_created"
	AuditQBCustomerLinked  = "qb.customer_linked"
	AuditQBCustomerSynced  = "qb.customer_synced"
	AuditQBInvoiceCreated  = "qb.invoice_created"
	// AuditQBInvoicePreviewed records an invoice that was costed but not
	// created — in shadow mode, and on a live run for an account on manual
	// billing. Kept in the audit log as well as qb_invoice_previews because
	// the previews table holds one row per order, refreshed in place, while
	// the question after a proof period is often "what did it decide, and
	// when".
	AuditQBInvoicePreviewed = "qb.invoice_previewed"
	// The two directions of the billing switch are separate actions rather
	// than one with a payload: "when did we start billing customers" is a
	// question worth being able to ask of the action column alone.
	AuditQBBillingModeLive     = "qb.billing_mode_live"
	AuditQBBillingModeShadowed = "qb.billing_mode_shadowed"
	// AuditQBOrderBilledManually records a staffer starting the invoice chain
	// for an order checkout did not bill — in practice, one placed while the
	// shop was in test mode.
	AuditQBOrderBilledManually = "qb.order_billed_manually"
	// AuditQBItemsUpdated records a change to which QuickBooks items invoices
	// bill against — in effect, which income account the shop's wholesale
	// revenue lands in.
	AuditQBItemsUpdated            = "qb.items_updated"
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
	AuditEmailSubscriptionResumed          = "email.subscription_resumed"
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
