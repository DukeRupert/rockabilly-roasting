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
	ErrSKUAlreadyExists = errors.New("sku already exists")

	// Inventory errors
	ErrInsufficientStock = errors.New("insufficient stock")

	// Subscription errors
	ErrSubscriptionNotFound    = errors.New("subscription not found")
	ErrSubscriptionNotActive   = errors.New("subscription is not active")
	ErrSubscriptionNotPausable = errors.New("subscription cannot be paused")

	// Fulfillment errors
	ErrFulfillmentNotFound = errors.New("fulfillment not found")

	// Discount errors
	ErrDiscountNotFound    = errors.New("discount not found")
	ErrDiscountExpired     = errors.New("discount has expired")
	ErrDiscountNotActive   = errors.New("discount is not active")
	ErrCouponAlreadyUsed   = errors.New("coupon code already used")
	ErrCouponNotFound      = errors.New("coupon code not found")
	ErrMinimumOrderNotMet  = errors.New("minimum order amount not met")

	// Auth errors
	ErrSessionExpired    = errors.New("session expired")
	ErrTokenExpired      = errors.New("token expired")
	ErrTokenAlreadyUsed  = errors.New("token already used")
	ErrStaffNotFound     = errors.New("staff not found")
	ErrStaffInactive     = errors.New("staff account is inactive")
	ErrPermissionDenied  = errors.New("permission denied")

	// Payment errors
	ErrPaymentFailed = errors.New("payment failed")

	// Shipping errors
	ErrShipmentNotFound = errors.New("shipment not found")

	// Taxon errors
	ErrTaxonNotFound = errors.New("taxon not found")

	// Address errors
	ErrAddressNotFound = errors.New("address not found")

	// Status errors
	ErrInvalidOrderStatus = errors.New("invalid order status transition")

	// Price errors
	ErrPriceNotFound = errors.New("price not found")

	// Slug errors
	ErrSlugAlreadyExists = errors.New("slug already exists")
)
