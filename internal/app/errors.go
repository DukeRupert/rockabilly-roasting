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
	ErrSubscriptionNotPastDue     = errors.New("subscription is not past due")
	ErrSubscriptionNotSkippable   = errors.New("subscription cannot be skipped")
	ErrInvalidSkipRequest         = errors.New("choose a number of shipments to skip or a restart date, not both")
	ErrSkipIntervalsOutOfRange    = errors.New("skip count is out of range")
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

	// Payment errors
	ErrPaymentFailed         = errors.New("payment failed")
	ErrPaymentAmountMismatch = errors.New("payment amount does not match order total")

	// Tax errors
	ErrTaxCalculationFailed = errors.New("tax calculation failed")
	ErrInvalidTaxConfig     = errors.New("invalid tax configuration")

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
