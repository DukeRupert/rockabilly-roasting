package app

import (
	"errors"

	"github.com/dukerupert/hiri/internal/domain"
)

// Sentinel errors for the application layer.
var (
	// Order errors
	ErrOrderNotFound                 = errors.New("order not found")
	ErrOrderNotRefundable            = errors.New("order is not refundable")
	ErrOrderNotPayable               = errors.New("order cannot be manually marked paid")
	ErrOrderNotCancellable           = errors.New("order is not cancellable")
	ErrOrderAlreadyFulfilled         = errors.New("order is already fulfilled")
	ErrOrderFulfillmentNotRevertible = errors.New("order fulfillment cannot be reverted")
	ErrOrderShipmentNotRevertible    = errors.New("order shipment cannot be reverted")
	ErrOrderNotEditable              = errors.New("order cannot be edited in its current state")
	ErrLineItemNotFound              = errors.New("line item not found")
	ErrLineItemNotInOrder            = errors.New("line item does not belong to this order")
	ErrVariantNotOnSameProduct       = errors.New("target variant is not on the same product")
	ErrVariantPriceMismatch          = errors.New("target variant has a different price; use cancel and recreate instead")

	// Cart errors
	ErrCartNotFound = errors.New("cart not found")
	ErrCartExpired  = errors.New("cart has expired")
	ErrCartEmpty    = errors.New("cart is empty")

	// Product access errors
	ErrProductNotAccessible = errors.New("product is not available to this customer")

	// Customer errors
	ErrCustomerNotFound   = errors.New("customer not found")
	ErrEmailAlreadyExists = errors.New("email already exists")
	ErrEmailNotVerified   = errors.New("email not verified")
	ErrInvalidCredentials = errors.New("invalid credentials")

	// Product errors
	ErrProductNotFound         = errors.New("product not found")
	ErrVariantNotFound         = errors.New("variant not found")
	ErrSKUAlreadyExists        = errors.New("sku already exists")
	ErrDuplicateVariantOptions = errors.New("a variant with these options already exists")
	ErrVariantInUse            = errors.New("variant is referenced by existing orders, carts, or subscriptions — archive it instead")
	ErrVariantArchived         = errors.New("variant is archived and cannot be purchased")
	ErrVariantNotInChannel     = errors.New("variant is not available on this sales channel")

	// Inventory errors
	ErrInsufficientStock = errors.New("insufficient stock")

	// Subscription errors
	ErrSubscriptionNotFound       = errors.New("subscription not found")
	ErrSubscriptionNotActive      = errors.New("subscription is not active")
	ErrSubscriptionNotPausable    = errors.New("subscription cannot be paused")
	ErrSubscriptionNotResumable   = errors.New("subscription cannot be resumed")
	ErrSubscriptionNotCancellable = errors.New("subscription cannot be cancelled")
	ErrSubscriptionNotEditable    = errors.New("subscription cannot be edited in its current state")
	ErrSubscriptionNotSkippable   = errors.New("subscription cannot be skipped")
	ErrInvalidSkipRequest         = errors.New("choose a number of shipments to skip or a restart date, not both")
	ErrSkipIntervalsOutOfRange    = errors.New("skip count is out of range")
	ErrPostponeNotDeliveryDay     = errors.New("that day has no delivery run to move")
	ErrPostponeNotForward         = errors.New("a run can only be moved to a later day")
	ErrPostponeTooFar             = errors.New("a run can only be moved up to two weeks")
	ErrPostponeAlreadyRun         = errors.New("that run has already gone out")
	ErrRestoreRunPassed           = errors.New("that run's scheduled day has already passed")
	ErrPostponeIntoPast           = errors.New("a run cannot be moved onto a day that has passed")
	ErrPostponeTargetRunMoved     = errors.New("the run on that day has itself been moved")
	ErrPostponeStrandsMovedRun    = errors.New("another run has already been moved onto that day")
	ErrRunRouteActive             = errors.New("that run's route is already out with the driver")
	ErrPostponeNoSchedule         = errors.New("no delivery schedule is configured")
	ErrSkipDateOutOfRange         = errors.New("pick a restart date within the next 60 days")
	ErrSkipDateBeforeNextOrder    = errors.New("pick a restart date after the shipment already scheduled")
	ErrNoSkipToUndo               = errors.New("no skip to undo")
	ErrSkipUndoTooLate            = errors.New("the skipped shipment date has already passed")
	// ErrRenewalPaymentDeclined signals that a renewal charge was declined and
	// the dunning state has already been advanced (past_due retry scheduled, or
	// expired at the cap). The job worker treats it as terminal — the renewal
	// scheduler owns the next attempt, so River must not retry the job.
	ErrRenewalPaymentDeclined   = errors.New("renewal payment declined")
	ErrSubscriptionPlanNotFound = errors.New("subscription plan not found")
	ErrSubscriptionPlanInactive = errors.New("subscription plan is not active")

	// Fulfillment errors
	ErrFulfillmentNotFound = errors.New("fulfillment not found")

	// Discount errors
	ErrDiscountNotFound      = errors.New("discount not found")
	ErrDiscountExpired       = errors.New("discount has expired")
	ErrDiscountNotActive     = errors.New("discount is not active")
	ErrCouponAlreadyUsed     = errors.New("coupon code already used")
	ErrCouponAlreadyRedeemed = errors.New("coupon code was just redeemed by someone else")
	ErrCouponNotFound        = errors.New("coupon code not found")
	ErrCouponCodeExists      = errors.New("coupon code already exists")
	ErrDiscountInvalid       = errors.New("discount fields are invalid")
	ErrMinimumOrderNotMet    = errors.New("minimum order amount not met")

	// Auth errors
	ErrSessionExpired   = errors.New("session expired")
	ErrTokenExpired     = errors.New("token expired")
	ErrTokenAlreadyUsed = errors.New("token already used")
	ErrStaffNotFound    = errors.New("staff not found")
	ErrStaffInactive    = errors.New("staff account is inactive")
	// Staff management errors
	ErrStaffEmailExists   = errors.New("a staff member with this email already exists")
	ErrStaffNameRequired  = errors.New("staff name is required")
	ErrStaffEmailRequired = errors.New("staff email is required")
	ErrInvalidStaffRole   = errors.New("invalid staff role")
	ErrStaffInviteInvalid = errors.New("staff invite link is invalid, expired, or already used")
	ErrCannotModifySelf   = errors.New("you cannot change your own role or account status")
	ErrLastActiveAdmin    = errors.New("cannot remove the last active admin")
	ErrPermissionDenied   = errors.New("permission denied")
	ErrMagicLinkExpired   = errors.New("magic link expired or already used")
	ErrSetupTokenExpired  = errors.New("setup link expired or already used")
	ErrPasswordTooShort   = errors.New("password must be at least 10 characters")

	// Customer user (additional wholesale logins) errors
	ErrCustomerUserNotFound      = errors.New("team member not found")
	ErrCustomerUserInviteInvalid = errors.New("invite link is invalid, expired, or already used")
	ErrCustomerUserEmailRequired = errors.New("email address is required")
	// ErrCustomerUserEmailTaken covers a collision with either an existing
	// customers row or another customer_users row. Both matter: customers.email
	// is checked first at login, so an invite that shadows one would send the
	// invitee to the wrong account.
	ErrCustomerUserEmailTaken = errors.New("that email address is already in use")
	ErrNotWholesaleAccount    = errors.New("team members are only available on wholesale accounts")

	// White-label onboarding errors
	ErrWhiteLabelInviteInvalid = errors.New("white-label invite link is invalid, expired, or already used")
	ErrWhiteLabelBaseInvalid   = errors.New("selected base coffee is not available for white-label")
	ErrWhiteLabelNameRequired  = errors.New("white-label product name is required")
	ErrWhiteLabelLabelRequired = errors.New("white-label label image is required")
	// ErrProductHasWhiteLabelChildren blocks archiving a coffee that white-label
	// products are still based on. Errors wrapping it are *WhiteLabelChildrenError,
	// whose message names the children staff need to reassign.
	ErrProductHasWhiteLabelChildren = errors.New("product is the base coffee for white-label products")
	// ErrNotWhiteLabelProduct guards the reassignment path — only a product created
	// through white-label onboarding has a base coffee to repoint.
	ErrNotWhiteLabelProduct = errors.New("product is not a white-label submission")

	// Payment errors
	ErrPaymentFailed         = errors.New("payment failed")
	ErrPaymentAmountMismatch = errors.New("payment amount does not match order total")

	// Tax errors
	ErrTaxCalculationFailed = errors.New("tax calculation failed")
	ErrInvalidTaxConfig     = errors.New("invalid tax configuration")
	// ErrInvalidQBBillingMode guards the one setting that decides whether real
	// customers get billed; an unrecognised value must be refused rather than
	// coerced.
	ErrInvalidQBBillingMode = errors.New("invalid quickbooks billing mode")
	// ErrQBBillingNotLive refuses to bill while the shop is in test mode. Test
	// mode's only promise is that nothing bills; an exception would void it.
	ErrQBBillingNotLive = errors.New("quickbooks billing is in test mode")
	// ErrQBOrderAlreadyInvoiced stops an order being billed twice.
	ErrQBOrderAlreadyInvoiced = errors.New("order already has a quickbooks invoice")
	// ErrQBOrderNotBillable covers an order with no customer or one that is
	// not a wholesale order.
	ErrQBOrderNotBillable = errors.New("order cannot be invoiced through quickbooks")
	// ErrQBSalesItemRequired guards the one item every invoice line needs;
	// QBO rejects a line without an ItemRef.
	ErrQBSalesItemRequired = errors.New("a quickbooks item is required for invoice lines")
	// ErrQBItemNotFound means the chosen item does not exist in the connected
	// company — caught at save time, because the alternative is every invoice
	// failing later.
	ErrQBItemNotFound = errors.New("that item does not exist in the connected quickbooks company")
	// ErrQBNoActiveItems means the company is connected but has nothing to
	// bill against — a fact about their books, not about the connection.
	ErrQBNoActiveItems = errors.New("the connected quickbooks company has no active items")

	// Shipping errors
	ErrShipmentNotFound      = errors.New("shipment not found")
	ErrShipmentWeightUnknown = errors.New("shipment weight cannot be calculated: variant has no weight set")
	// ErrShipmentNotRefundable signals a label cannot be refunded: it carries no
	// provider transaction ID (imported/legacy), a refund is already in flight or
	// completed, or the shipment is delivered (an obviously-used label).
	ErrShipmentNotRefundable = errors.New("shipment label cannot be refunded")
	// ErrOrderHasActiveLabel guards against buying a second label: an order that
	// already has a live (non-refunded) label must have it refunded first.
	ErrOrderHasActiveLabel = errors.New("order already has an active shipping label")

	// Taxon errors
	ErrTaxonNotFound = errors.New("taxon not found")

	// Address errors
	ErrAddressNotFound = errors.New("address not found")
	ErrLastAddress     = errors.New("cannot delete the only address")
	// ErrAddressIncomplete signals a checkout address is missing one of the
	// fields required to price + ship an order (street, city, state, ZIP).
	// Surfaced to the buyer with a fix-it message rather than an opaque 500.
	ErrAddressIncomplete = errors.New("address is missing required fields")

	// ErrFulfillmentUnavailable signals the buyer picked a fulfillment method
	// (e.g. local delivery) that isn't valid for their ship-to ZIP. Surfaced as
	// a fix-it message so they can switch to pickup or shipping.
	ErrFulfillmentUnavailable = errors.New("fulfillment method not available for address")

	// ErrPickupUnavailable signals the merchant does not currently offer pickup,
	// so a customer following a "switch to pickup" link has nowhere to switch
	// to. Distinct from ErrOrderNotSwitchable: nothing about the order is wrong,
	// the shop just has pickup turned off.
	ErrPickupUnavailable = errors.New("pickup is not currently offered")

	// ErrOrderNotSwitchable signals an order can no longer be moved to pickup —
	// it was never on local delivery, or it has already been packed, dispatched,
	// or cancelled. Surfaced to the customer as "too late, call the shop"
	// rather than as a failure.
	ErrOrderNotSwitchable = errors.New("order can no longer be switched to pickup")

	// Status errors
	ErrInvalidOrderStatus = errors.New("invalid order status transition")

	// Price errors
	ErrPriceNotFound = errors.New("price not found")
	ErrInvalidPrice  = errors.New("price must not be negative")
	// ErrInvalidTierQuantity is returned for a volume break below 2. Quantity 1
	// is the list price itself, which is set through SetPriceListPrice.
	ErrInvalidTierQuantity = errors.New("volume break must start at 2 or more")
	// ErrInvalidWholesaleMOQ covers the three ways a variant's wholesale
	// minimum/multiple can be self-contradictory. See
	// CatalogService.UpdateVariantWholesaleMOQ for why a minimum that is not a
	// multiple of the multiple is rejected rather than silently rounded up.
	ErrInvalidWholesaleMOQ = errors.New("invalid wholesale order quantity rule")
	ErrPriceListNotFound   = errors.New("price list not found")

	// Cart errors (item-level)
	ErrInvalidQuantity = errors.New("quantity must be greater than zero")

	// Slug errors
	ErrSlugAlreadyExists = errors.New("slug already exists")

	// Wholesale errors
	ErrWholesaleNotApproved = errors.New("wholesale account is not approved")
	ErrWholesaleNotPending  = errors.New("wholesale application is not pending")
	ErrMOQViolation         = errors.New("minimum order quantity not met")
	ErrWholesalePricesStale = errors.New("wholesale cart prices are stale")

	// Attribute errors
	ErrAttributeSetNotFound           = errors.New("attribute set not found")
	ErrAttributeKeyNotFound           = errors.New("attribute key not found")
	ErrAttributeValueNotAllowed       = errors.New("attribute value is not in the allowed list")
	ErrAttributeAllowedValuesRequired = errors.New("allowed values are required for enum types")

	// QuickBooks errors
	ErrQBNotConnected = errors.New("quickbooks is not connected")

	// Invoice errors
	ErrInvoiceNotFound     = errors.New("invoice not found")
	ErrOrderNotInvoiceable = errors.New("order cannot be invoiced")
	ErrInvoiceNotPayable   = errors.New("invoice cannot accept payments")
	ErrInvoiceNotSendable  = errors.New("invoice is not in draft status")
	ErrInvoiceNotVoidable  = errors.New("invoice cannot be voided")
	// ErrOrderQBManaged guards the manual invoice path: an order whose invoice
	// lives in QuickBooks (qb_invoice_id set) is reconciled solely by the QB
	// path, so the manual InvoiceService must not write its payment status.
	ErrOrderQBManaged = errors.New("order is managed by QuickBooks; use the QuickBooks invoice flow")

	// Box preset errors
	ErrBoxPresetNameRequired      = errors.New("box preset name is required")
	ErrBoxPresetDimensionsInvalid = errors.New("box preset dimensions must be positive")
	ErrBoxPresetMaxWeightInvalid  = errors.New("box preset max weight must be positive")

	// Label / shipment errors
	ErrShipmentNoPhysicalItems = errors.New("order has no physical items to ship")
	ErrNoBoxPreset             = errors.New("no box presets configured")

	// Geocoding errors. The split matters to the caller: an address the
	// provider cannot match needs a human to correct it, while an unavailable
	// provider means try the same address again later.
	ErrAddressNotGeocodable  = errors.New("address could not be geocoded")
	ErrGeocoderUnavailable   = errors.New("geocoding provider unavailable")
	ErrGeocoderNotConfigured = errors.New("geocoding provider is not configured")

	// Route planning errors.
	ErrNoDeliveryStops = errors.New("no local delivery orders to route")
	// ErrOriginNotConfigured means shipping_config has no usable origin
	// address. The roastery address is the ship-from EasyPost already uses, so
	// this is a settings gap rather than a missing route feature.
	ErrOriginNotConfigured = errors.New("roastery origin address is not configured")
	// ErrOriginNotGeocodable means the configured origin exists but cannot be
	// placed on the map — every route starts there, so planning cannot proceed.
	ErrOriginNotGeocodable = errors.New("roastery origin address could not be geocoded")
	// ErrEndNotGeocodable means staff asked the run to finish at an address
	// that could not be placed on the map. Planning stops rather than falling
	// back to the roastery: silently ignoring the requested ending would give
	// the driver a route optimized to end at the wrong side of town.
	ErrEndNotGeocodable = errors.New("the end-of-run address could not be geocoded")
	ErrRouteNotFound    = errors.New("delivery route not found")
	// ErrRouteAlreadyActive guards a driver mid-run: re-planning a date whose
	// route is already out with a driver would swap the stop list under them.
	ErrRouteAlreadyActive = errors.New("a route for this date is already active; complete it before re-planning")
	// ErrRouteNotActivatable means the route is gone or already active.
	// Re-activating would mint a second token and break the link the driver
	// already has open.
	ErrRouteNotActivatable = errors.New("only a draft route can be activated")
	ErrRouteEmpty          = errors.New("a route needs at least one stop before it can be activated")
	// ErrRouteStopNotFound covers both "no such stop" and "that stop is on a
	// different route". The driver page must not distinguish them: its share
	// token grants one route, and a stop id from another route should look
	// exactly like one that does not exist.
	ErrRouteStopNotFound = errors.New("route stop not found")
	// ErrStopAlreadyDelivered guards the skip path — undoing a delivery would
	// mean un-completing the order behind it, which is a staff action in admin,
	// not something to do from a phone in a van.
	ErrStopAlreadyDelivered = errors.New("this stop is already marked delivered")

	// --- Equipment service ---

	ErrEquipmentNotFound = errors.New("equipment not found")
	// ErrEquipmentMakeRequired guards the nameless machine. A register row with
	// no make is unsearchable and unrecognisable — it is the one field that has
	// to be there, since model and serial are genuinely often unknown.
	ErrEquipmentMakeRequired     = errors.New("what make is the machine?")
	ErrInvalidEquipmentCategory  = errors.New("pick a valid equipment category")
	ErrInvalidEquipmentOwnership = errors.New("pick a valid ownership")
	ErrInvalidEquipmentStatus    = errors.New("pick a valid equipment status")
	// ErrEquipmentSiteNotOnAccount rejects a site that belongs to a different
	// customer. address_id is a plain foreign key with nothing tying it to the
	// machine's owner, and it arrives from an editable form field — the scoped
	// picker is a convenience, this is the control.
	ErrEquipmentSiteNotOnAccount = errors.New("that address is not on this customer's account")
	// ErrEquipmentSiteIncomplete guards a half-typed new site. A street with no
	// town is not somewhere a tech can be sent.
	ErrEquipmentSiteIncomplete = errors.New("a new site needs a street, city, state and ZIP")
	// ErrEquipmentRetired rejects scheduling work on a machine that is out of
	// service for good. Maintenance nobody will ever do is exactly the noise a
	// due list has to stay free of to be worth opening.
	ErrEquipmentRetired = errors.New("that machine is retired")

	// --- Service tickets ---

	ErrServiceTicketNotFound = errors.New("service ticket not found")
	// ErrServiceTicketTitleRequired guards the untitled ticket. The list view is
	// a column of titles; a blank one is a row nobody can triage without
	// opening it.
	ErrServiceTicketTitleRequired = errors.New("what is wrong? give the ticket a one-line title")
	ErrInvalidServiceSeverity     = errors.New("pick a valid severity")
	ErrInvalidServiceTicketStatus = errors.New("pick a valid ticket status")
	ErrInvalidServiceNoteKind     = errors.New("pick a valid note type")
	// ErrEmptyServiceNote rejects the blank timeline entry. An empty note that
	// counts as contact would reset the staleness clock while saying nothing —
	// the precise failure the flag exists to catch.
	ErrEmptyServiceNote = errors.New("write something before saving the note")
	// ErrTicketEquipmentMismatch stops a ticket being filed against a machine
	// belonging to somebody else, which would put one cafe's repair history on
	// another's page.
	ErrTicketEquipmentMismatch = errors.New("that machine belongs to a different customer")

	// --- Service parts and time ---

	ErrServicePartNotFound = errors.New("that part is not on this ticket")
	// ErrPartNameRequired guards the nameless line. "1 × £4.25" on a repair
	// record six months later is worse than no line at all.
	ErrPartNameRequired    = errors.New("what part is it?")
	ErrInvalidPartQuantity = errors.New("quantity has to be at least one")
	ErrInvalidPartCost     = errors.New("a part cannot cost less than nothing")
	ErrInvalidPartStatus   = errors.New("pick a valid part status")

	ErrServiceTimeEntryNotFound = errors.New("that time entry is not on this ticket")
	// ErrInvalidTimeMinutes rejects zero and negative stints. Zero minutes is
	// someone tabbing past the field, and it would quietly dilute every
	// hours-per-account number computed later.
	ErrInvalidTimeMinutes     = errors.New("how long did it take? minutes, at least one")
	ErrInvalidServiceTimeKind = errors.New("pick labour or travel")

	// --- Optional feature modules ---

	// ErrUnknownModule is returned when a toggle names a key this binary does
	// not have in its registry. Not a module to create on the fly — either the
	// request was hand-made, or the caller is a deploy behind.
	ErrUnknownModule = errors.New("unknown module")

	// --- Announcements ---

	ErrAnnouncementNotFound = errors.New("announcement not found")
	// ErrEmptyAnnouncement guards the blank blast: a notice with no subject or
	// no body reaching every customer is never what anyone intended.
	ErrEmptyAnnouncement = errors.New("announcement subject and message are both required")
	ErrInvalidAudience   = errors.New("choose a valid audience")
	// ErrAnnouncementNotCancellable is returned once dispatch has started —
	// mail is already leaving and there is nothing honest left to undo.
	ErrAnnouncementNotCancellable = errors.New("this announcement has already started sending")
	// ErrScheduleInPast rejects a send time that has already gone by. River
	// would fire it instantly, which is a surprising way to learn you typed the
	// wrong date into a mailing that reaches every customer.
	ErrScheduleInPast = errors.New("pick a send time in the future")

	// Background job errors
	//
	// ErrJobNotDead guards the retry action: only a discarded job is eligible.
	// A job that is queued or running is already on its way, and shoving it
	// back in line helps nobody.
	ErrJobNotDead = errors.New("this job is not waiting to be retried")
	// ErrJobRetryUnavailable means no River client is wired in this process,
	// so there is nothing to hand the job back to.
	ErrJobRetryUnavailable = errors.New("background job retry is unavailable")

	// --- Preventive maintenance ---

	ErrPlanNotFound = errors.New("maintenance plan not found")
	// ErrPlanNameRequired guards the unnamed plan. The plan name is what staff
	// pick from when assigning one to a machine; a blank one is unpickable.
	ErrPlanNameRequired = errors.New("give the plan a name")
	// ErrPlanNameTaken catches the near-duplicate. Two plans called "Linea PB
	// warranty" is a data-entry accident nobody notices until the second one
	// has machines on it.
	ErrPlanNameTaken = errors.New("a plan with that name already exists")
	// ErrPlanInUse blocks deleting a plan machines have been on. The
	// maintenance history of every one of them hangs off it; deactivate the
	// plan instead, which takes it out of the picker and leaves the record.
	ErrPlanInUse = errors.New("machines have been on this plan — deactivate it instead of deleting it")
	// ErrPlanInactive stops a retired plan being assigned to something new.
	ErrPlanInactive = errors.New("that plan is no longer active")
	// ErrPlanHasNoTasks rejects assigning an empty plan. It would generate no
	// maintenance at all while looking on the machine's page exactly like one
	// that does — the worst kind of wrong.
	ErrPlanHasNoTasks       = errors.New("add at least one task to the plan before assigning it")
	ErrPlanTaskNotFound     = errors.New("that task is not on this plan")
	ErrPlanTaskNameRequired = errors.New("what gets done? name the task")
	// ErrPlanIntervalInvalid guards the interval. Zero would come due forever;
	// ten years is past the point where anything would ever surface it again,
	// and is far likelier to be a typo than a schedule.
	ErrPlanIntervalInvalid = errors.New("how often? give an interval between 1 and 3650 days")
	// ErrPlanLeadInvalid rejects a warning window as long as the interval
	// itself, which would leave the task permanently reading as due soon and
	// the due list saying nothing at all.
	ErrPlanLeadInvalid = errors.New("the notice period has to be shorter than the interval")
	// ErrPlanStartRequired guards the anchor date. Every due date on the
	// machine counts forward from it, so there is no sensible default.
	ErrPlanStartRequired           = errors.New("when does the schedule start?")
	ErrPlanContractEndsBeforeStart = errors.New("the contract cannot end before the schedule starts")
	// ErrPlanAlreadyAssigned stops the same plan going on a machine twice,
	// which would double every due item on it.
	ErrPlanAlreadyAssigned    = errors.New("that machine is already on this plan")
	ErrPlanAssignmentNotFound = errors.New("that plan is not on this machine")
	ErrPlanAssignmentEnded    = errors.New("that plan has already been taken off this machine")

	ErrMaintenanceNotFound = errors.New("scheduled maintenance not found")
	// ErrMaintenanceAlreadyClosed makes the double submit safe: the second
	// click finds the item already done and says so, rather than writing a
	// second follow-on occurrence.
	ErrMaintenanceAlreadyClosed = errors.New("that maintenance has already been closed out")
	// ErrMaintenanceDateRequired guards the date the next occurrence is
	// measured from. Defaulting it to today would silently re-anchor a
	// fortnight-old visit to the day it was typed up.
	ErrMaintenanceDateRequired = errors.New("when was it done?")
	// ErrMaintenanceDateOutOfRange catches the slipped year. The day a
	// completion is logged on anchors the next occurrence, so a date a decade
	// out takes the machine off the due list for a decade, silently.
	ErrMaintenanceDateOutOfRange = errors.New("that date is more than ten years away — check the year")
	// ErrMaintenanceDateInFuture guards the completion date specifically. "Done
	// on" records something that happened; a date ahead of today would anchor
	// the next occurrence from a day nobody worked.
	ErrMaintenanceDateInFuture = errors.New("that is in the future — when was the work actually done?")

	// ErrLaborRateInvalid guards the hourly rate. The cap exists because
	// somebody typing cents into a dollars field would put a six-figure number
	// against every account in the cost report.
	ErrLaborRateInvalid = errors.New("give an hourly rate no higher than $10,000")
	// ErrLaborRateZero keeps "unset" to one spelling. A saved 0.00 reads as
	// unset to ServiceLaborRates.Set(), so it would stamp every hour uncosted
	// while the settings page showed a figure somebody had typed.
	ErrLaborRateZero = errors.New("leave the labour rate blank rather than zero — zero would silently price nothing")
	// ErrTravelRateWithoutLabor rejects a travel rate with no labour rate
	// behind it. Travel falls back to the labour rate, and with none set there
	// is no money column for it to appear in — the setting would do nothing,
	// silently.
	ErrTravelRateWithoutLabor = errors.New("set the labour rate first — a travel rate on its own has nothing to appear in")
)

// MOQViolationError carries the per-line minimum/multiple violations so
// handlers can tell the buyer which lines are short instead of a bare
// rejection. errors.Is(err, ErrMOQViolation) matches it, so existing checks
// and the HTTP mapping keep working.
type MOQViolationError struct {
	Violations []domain.MOQViolation
}

func (e *MOQViolationError) Error() string        { return ErrMOQViolation.Error() }
func (e *MOQViolationError) Is(target error) bool { return target == ErrMOQViolation }
