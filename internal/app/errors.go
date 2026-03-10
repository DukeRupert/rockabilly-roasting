package app

import "errors"

// Sentinel errors for the application layer.
var (
	// Order errors
	ErrOrderNotFound      = errors.New("order not found")
	ErrOrderNotRefundable = errors.New("order is not refundable")
	ErrOrderNotCancellable = errors.New("order is not cancellable")
	ErrOrderAlreadyFulfilled = errors.New("order is already fulfilled")

	// Cart errors
	ErrCartNotFound  = errors.New("cart not found")
	ErrCartExpired   = errors.New("cart has expired")
	ErrCartEmpty     = errors.New("cart is empty")

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

	// Inventory errors
	ErrInsufficientStock = errors.New("insufficient stock")

	// Subscription errors
	ErrSubscriptionNotFound      = errors.New("subscription not found")
	ErrSubscriptionNotActive     = errors.New("subscription is not active")
	ErrSubscriptionNotPausable   = errors.New("subscription cannot be paused")
	ErrSubscriptionNotResumable  = errors.New("subscription cannot be resumed")
	ErrSubscriptionNotCancellable = errors.New("subscription cannot be cancelled")
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
	ErrPasswordTooShort  = errors.New("password must be at least 8 characters")

	// Payment errors
	ErrPaymentFailed = errors.New("payment failed")

	// Tax errors
	ErrTaxCalculationFailed = errors.New("tax calculation failed")
	ErrInvalidTaxConfig     = errors.New("invalid tax configuration")

	// Shipping errors
	ErrShipmentNotFound = errors.New("shipment not found")

	// Taxon errors
	ErrTaxonNotFound = errors.New("taxon not found")

	// Address errors
	ErrAddressNotFound = errors.New("address not found")
	ErrLastAddress     = errors.New("cannot delete the only address")

	// Status errors
	ErrInvalidOrderStatus = errors.New("invalid order status transition")

	// Price errors
	ErrPriceNotFound = errors.New("price not found")
	ErrInvalidPrice  = errors.New("price must not be negative")

	// Cart errors (item-level)
	ErrInvalidQuantity = errors.New("quantity must be greater than zero")

	// Slug errors
	ErrSlugAlreadyExists = errors.New("slug already exists")

	// Wholesale errors
	ErrWholesaleNotApproved = errors.New("wholesale account is not approved")
	ErrWholesaleNotPending  = errors.New("wholesale application is not pending")
	ErrMOQViolation         = errors.New("minimum order quantity not met")

	// Attribute errors
	ErrAttributeSetNotFound = errors.New("attribute set not found")
	ErrAttributeKeyNotFound = errors.New("attribute key not found")

	// QuickBooks errors
	ErrQBNotConnected = errors.New("quickbooks is not connected")

	// Invoice errors
	ErrInvoiceNotFound    = errors.New("invoice not found")
	ErrOrderNotInvoiceable = errors.New("order cannot be invoiced")
	ErrInvoiceNotPayable  = errors.New("invoice cannot accept payments")
	ErrInvoiceNotSendable = errors.New("invoice is not in draft status")
	ErrInvoiceNotVoidable = errors.New("invoice cannot be voided")
)
