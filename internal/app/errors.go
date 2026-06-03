package app

import "errors"

// Sentinel errors for the application layer.
var (
	// Order errors
	ErrOrderNotFound      = errors.New("order not found")
	ErrOrderNotRefundable = errors.New("order is not refundable")
	ErrOrderNotCancellable = errors.New("order is not cancellable")
	ErrOrderAlreadyFulfilled = errors.New("order is already fulfilled")
	ErrOrderFulfillmentNotRevertible = errors.New("order fulfillment cannot be reverted")
	ErrOrderShipmentNotRevertible    = errors.New("order shipment cannot be reverted")
	ErrOrderNotEditable              = errors.New("order cannot be edited in its current state")
	ErrLineItemNotFound              = errors.New("line item not found")
	ErrLineItemNotInOrder            = errors.New("line item does not belong to this order")
	ErrVariantNotOnSameProduct       = errors.New("target variant is not on the same product")
	ErrVariantPriceMismatch          = errors.New("target variant has a different price; use cancel and recreate instead")

	// Cart errors
	ErrCartNotFound  = errors.New("cart not found")
	ErrCartExpired   = errors.New("cart has expired")
	ErrCartEmpty     = errors.New("cart is empty")

	// Product access errors
	ErrProductNotAccessible = errors.New("product is not available to this customer")

	// Customer errors
	ErrCustomerNotFound     = errors.New("customer not found")
	ErrEmailAlreadyExists   = errors.New("email already exists")
	ErrEmailNotVerified     = errors.New("email not verified")
	ErrInvalidCredentials   = errors.New("invalid credentials")

	// Product errors
	ErrProductNotFound  = errors.New("product not found")
	ErrVariantNotFound  = errors.New("variant not found")
	ErrSKUAlreadyExists              = errors.New("sku already exists")
	ErrDuplicateVariantOptions       = errors.New("a variant with these options already exists")
	ErrVariantInUse                  = errors.New("variant is referenced by existing orders, carts, or subscriptions — archive it instead")
	ErrVariantArchived               = errors.New("variant is archived and cannot be purchased")

	// Inventory errors
	ErrInsufficientStock = errors.New("insufficient stock")

	// Subscription errors
	ErrSubscriptionNotFound      = errors.New("subscription not found")
	ErrSubscriptionNotActive     = errors.New("subscription is not active")
	ErrSubscriptionNotPausable   = errors.New("subscription cannot be paused")
	ErrSubscriptionNotResumable  = errors.New("subscription cannot be resumed")
	ErrSubscriptionNotCancellable = errors.New("subscription cannot be cancelled")
	ErrSubscriptionNotEditable    = errors.New("subscription cannot be edited in its current state")
	ErrSubscriptionPlanNotFound  = errors.New("subscription plan not found")
	ErrSubscriptionPlanInactive  = errors.New("subscription plan is not active")

	// Fulfillment errors
	ErrFulfillmentNotFound = errors.New("fulfillment not found")

	// Discount errors
	ErrDiscountNotFound    = errors.New("discount not found")
	ErrDiscountExpired     = errors.New("discount has expired")
	ErrDiscountNotActive   = errors.New("discount is not active")
	ErrCouponAlreadyUsed     = errors.New("coupon code already used")
	ErrCouponAlreadyRedeemed = errors.New("coupon code was just redeemed by someone else")
	ErrCouponNotFound        = errors.New("coupon code not found")
	ErrMinimumOrderNotMet    = errors.New("minimum order amount not met")

	// Auth errors
	ErrSessionExpired    = errors.New("session expired")
	ErrTokenExpired      = errors.New("token expired")
	ErrTokenAlreadyUsed  = errors.New("token already used")
	ErrStaffNotFound     = errors.New("staff not found")
	ErrStaffInactive     = errors.New("staff account is inactive")
	ErrPermissionDenied  = errors.New("permission denied")
	ErrMagicLinkExpired  = errors.New("magic link expired or already used")
	ErrSetupTokenExpired = errors.New("setup link expired or already used")
	ErrPasswordTooShort  = errors.New("password must be at least 10 characters")

	// Payment errors
	ErrPaymentFailed         = errors.New("payment failed")
	ErrPaymentAmountMismatch = errors.New("payment amount does not match order total")

	// Tax errors
	ErrTaxCalculationFailed = errors.New("tax calculation failed")
	ErrInvalidTaxConfig     = errors.New("invalid tax configuration")

	// Shipping errors
	ErrShipmentNotFound        = errors.New("shipment not found")
	ErrShipmentWeightUnknown   = errors.New("shipment weight cannot be calculated: variant has no weight set")

	// Taxon errors
	ErrTaxonNotFound = errors.New("taxon not found")

	// Address errors
	ErrAddressNotFound = errors.New("address not found")
	ErrLastAddress     = errors.New("cannot delete the only address")

	// Status errors
	ErrInvalidOrderStatus = errors.New("invalid order status transition")

	// Price errors
	ErrPriceNotFound     = errors.New("price not found")
	ErrInvalidPrice      = errors.New("price must not be negative")
	ErrPriceListNotFound = errors.New("price list not found")

	// Cart errors (item-level)
	ErrInvalidQuantity = errors.New("quantity must be greater than zero")

	// Slug errors
	ErrSlugAlreadyExists = errors.New("slug already exists")

	// Wholesale errors
	ErrWholesaleNotApproved   = errors.New("wholesale account is not approved")
	ErrWholesaleNotPending    = errors.New("wholesale application is not pending")
	ErrMOQViolation           = errors.New("minimum order quantity not met")
	ErrWholesalePricesStale   = errors.New("wholesale cart prices are stale")

	// Attribute errors
	ErrAttributeSetNotFound          = errors.New("attribute set not found")
	ErrAttributeKeyNotFound          = errors.New("attribute key not found")
	ErrAttributeValueNotAllowed      = errors.New("attribute value is not in the allowed list")
	ErrAttributeAllowedValuesRequired = errors.New("allowed values are required for enum types")

	// QuickBooks errors
	ErrQBNotConnected = errors.New("quickbooks is not connected")

	// Invoice errors
	ErrInvoiceNotFound    = errors.New("invoice not found")
	ErrOrderNotInvoiceable = errors.New("order cannot be invoiced")
	ErrInvoiceNotPayable  = errors.New("invoice cannot accept payments")
	ErrInvoiceNotSendable = errors.New("invoice is not in draft status")
	ErrInvoiceNotVoidable = errors.New("invoice cannot be voided")
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
)
