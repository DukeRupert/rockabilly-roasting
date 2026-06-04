package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	sentryhttp "github.com/getsentry/sentry-go/http"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/platform/help"
	"github.com/dukerupert/hiri/internal/platform/media"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/platform/quickbooks"
	"github.com/dukerupert/hiri/internal/platform/ratelimit"
	"github.com/dukerupert/hiri/internal/platform/sessions"
	"github.com/dukerupert/hiri/internal/platform/turnstile"
	"github.com/riverqueue/river"
)

// Deps holds all dependencies needed by HTTP handlers.
//
// Handlers route through app services (or platform/* for infrastructure concerns).
// Direct *store.X references are not permitted here — if a handler needs access
// to data that isn't covered by an existing service, extend the service rather
// than reaching into the store.
type Deps struct {
	Pool                   *pgxpool.Pool
	Logger                 *slog.Logger
	Metrics                *metrics.Registry
	Sessions               *sessions.Manager
	OrderService           *app.OrderService
	CustomerService        *app.CustomerService
	CatalogService         *app.CatalogService
	CheckoutService        *app.CheckoutService
	FulfillmentService     *app.FulfillmentService
	SubscriptionService    *app.SubscriptionService
	DiscountService        *app.DiscountService
	AuthService            *app.AuthService
	PricingService         *app.PricingService
	CartService            *app.CartService
	WholesaleService       *app.WholesaleService
	AttributeService       *app.AttributeService
	InvoiceService         *app.InvoiceService
	CustomerGroupService   *app.CustomerGroupService
	PriceListService       *app.PriceListService
	AuditQueryService      *app.AuditQueryService
	WebhookService         *app.WebhookService
	AuditWriter            *audit.AuditWriter // for cross-boundary audit events (OAuth connect/disconnect); prefer recording through a service
	PaymentProvider        payments.Provider
	RiverClient            *river.Client[pgx.Tx]
	R2Client               *media.R2Client
	MediaConfig            *media.Config
	QBClient               quickbooks.Client
	QBOAuthManager         *quickbooks.OAuthManager // nil when QuickBooks is not configured
	QBWebhookVerifierToken string
	QBHTTPClient           *http.Client
	HelpRegistry           *help.Registry
	RateLimiter            *ratelimit.Limiter
	TurnstileVerifier      *turnstile.Verifier // verifies Cloudflare Turnstile tokens; no-op when no secret configured
	TurnstileSiteKey       string              // public site key embedded in widget; empty disables widget
	SecureCookies          bool
	BaseURL                string // public site URL, e.g. "https://rockabillyroasting.com"
	Mailer                 email.Sender
	EmailFrom              string         // sender address for transactional emails
	StaffEmail             string         // staff notification recipient
	MerchantTZ             *time.Location // local timezone for day-bounded queries (e.g. "today's revenue")
}

// MetricsMux returns a handler for the internal metrics listener.
func MetricsMux(reg *metrics.Registry) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg.Reg, promhttp.HandlerOpts{}))
	return mux
}

// NewRouter creates a new HTTP router with all routes and middleware registered.
func NewRouter(deps *Deps) http.Handler {
	mux := http.NewServeMux()

	// Health check — pings the database to verify connectivity.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := deps.Pool.Ping(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("db unhealthy")) //nolint:errcheck
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	})

	// Prometheus metrics served on a separate internal listener (see MetricsMux).

	// Static assets
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler("internal/ui/assets")))

	// SEO files
	mux.HandleFunc("GET /robots.txt", deps.handleRobotsTxt)
	mux.HandleFunc("GET /sitemap.xml", deps.handleSitemapXML)

	// Legacy WooCommerce URL redirects (pre-cutover paths). Both trailing-
	// slash and bare forms are registered because WC always emitted a slash.
	mux.HandleFunc("GET /product/{slug}", deps.handleLegacyProductRedirect)
	mux.HandleFunc("GET /product/{slug}/{$}", deps.handleLegacyProductRedirect)
	mux.HandleFunc("GET /product-category/{slug}", deps.handleLegacyCatalogRedirect)
	mux.HandleFunc("GET /product-category/{slug}/{$}", deps.handleLegacyCatalogRedirect)
	mux.HandleFunc("GET /shop-merchandise", deps.handleLegacyCatalogRedirect)
	mux.HandleFunc("GET /shop-merchandise/{$}", deps.handleLegacyCatalogRedirect)
	mux.HandleFunc("GET /rhythm-and-brews", deps.handleLegacyCatalogRedirect)
	mux.HandleFunc("GET /rhythm-and-brews/{$}", deps.handleLegacyCatalogRedirect)

	// Storefront routes
	mux.HandleFunc("GET /{$}", deps.handleStorefrontHome)
	mux.HandleFunc("GET /catalog", deps.handleStorefrontCatalog)
	mux.HandleFunc("GET /catalog/{slug}", deps.handleStorefrontProduct)
	mux.HandleFunc("GET /about", deps.handleAboutPage)
	contactIPLimit := ratelimit.EndpointLimit(deps.RateLimiter, ratelimit.ContactIPLimit, ratelimit.ContactWindow, func(r *http.Request) string {
		return ratelimit.ContactIPKey(ratelimit.ClientIP(r))
	})
	mux.Handle("POST /about/contact", contactIPLimit(http.HandlerFunc(deps.handleContactSubmit)))
	mux.HandleFunc("GET /wholesale", deps.handleWholesaleLandingPage)
	mux.HandleFunc("GET /privacy", deps.handlePrivacyPage)
	mux.HandleFunc("GET /terms", deps.handleTermsPage)
	mux.HandleFunc("GET /shipping", deps.handleShippingPage)
	mux.HandleFunc("GET /newsletter/thanks", deps.handleNewsletterThanksPage)
	mux.HandleFunc("GET /help", deps.handleHelpIndex)
	mux.HandleFunc("GET /help/{slug}", deps.handleHelpArticle)

	// Cart routes
	mux.HandleFunc("POST /cart/add", deps.handleCartAdd)
	mux.HandleFunc("GET /cart", deps.handleCartView)
	mux.HandleFunc("POST /cart/update", deps.handleCartUpdateQuantity)
	mux.HandleFunc("POST /cart/remove", deps.handleCartRemoveItem)

	// Subscriptions landing page
	mux.HandleFunc("GET /subscriptions", deps.handleSubscriptionsPage)

	// Subscribe routes
	mux.HandleFunc("GET /subscribe", deps.handleSubscribePage)
	mux.HandleFunc("POST /api/subscribe/payment-intent", deps.handleSubscribePaymentIntent)
	mux.HandleFunc("POST /api/subscribe/confirm", deps.handleSubscribeConfirm)

	// Checkout routes
	mux.HandleFunc("GET /order/confirmed", deps.handleOrderConfirmed)
	mux.HandleFunc("GET /checkout", deps.handleCheckoutPage)
	mux.HandleFunc("GET /api/checkout/cart", deps.handleCheckoutCart)
	mux.HandleFunc("POST /api/checkout/address", deps.handleCheckoutAddress)
	couponIPLimit := ratelimit.EndpointLimit(deps.RateLimiter, ratelimit.CouponIPLimit, ratelimit.CouponWindow, func(r *http.Request) string {
		return ratelimit.CouponIPKey(ratelimit.ClientIP(r))
	})
	mux.Handle("POST /api/checkout/coupon", couponIPLimit(http.HandlerFunc(deps.handleCheckoutApplyCoupon)))
	mux.HandleFunc("DELETE /api/checkout/coupon", deps.handleCheckoutRemoveCoupon)
	mux.HandleFunc("POST /api/checkout/payment-intent", deps.handleCheckoutPaymentIntent)
	mux.HandleFunc("POST /api/checkout/confirm", deps.handleCheckoutConfirm)

	// Wholesale application and password setup (public)
	mux.HandleFunc("GET /wholesale/apply", deps.handleWholesaleApplyPage)
	wholesaleApplyIPLimit := ratelimit.EndpointLimit(deps.RateLimiter, ratelimit.WholesaleApplyIPLimit, ratelimit.WholesaleApplyWindow, func(r *http.Request) string {
		return ratelimit.WholesaleApplyIPKey(ratelimit.ClientIP(r))
	})
	mux.Handle("POST /wholesale/apply", wholesaleApplyIPLimit(http.HandlerFunc(deps.handleWholesaleApply)))
	mux.HandleFunc("GET /wholesale/setup", deps.handleWholesaleSetupPage)
	mux.HandleFunc("POST /wholesale/setup", deps.handleWholesaleSetup)

	// Generic password setup/reset (admin-triggered, retail or wholesale customer)
	mux.HandleFunc("GET /account/password-setup", deps.handleAccountPasswordSetupPage)
	mux.HandleFunc("POST /account/password-setup", deps.handleAccountPasswordSetup)

	// Retail account auth routes (magic link, no session required)
	magicLinkLimit := ratelimit.AuthLimit(deps.RateLimiter, ratelimit.MagicLinkIPLimit, ratelimit.MagicLinkIPLimit, ratelimit.MagicLinkWindow, func(r *http.Request) string {
		return r.FormValue("email")
	})
	mux.HandleFunc("GET /account/login", deps.handleAccountLoginPage)
	mux.Handle("POST /account/login", magicLinkLimit(http.HandlerFunc(deps.handleAccountLoginRequest)))
	mux.HandleFunc("GET /account/magic", deps.handleAccountMagicRedeem)
	mux.Handle("POST /account/logout", deps.requireCustomerSession(http.HandlerFunc(deps.handleAccountLogout)))

	// Security POST routes: rate-limited + authenticated. Registered at the outer
	// mux so the rate-limit middleware wraps the auth check (same pattern as wholesale
	// auth routes). The limiter uses the same IP-based limits as magic-link since the
	// threat model is identical: someone with a stolen session brute-forcing passwords.
	securityLimit := ratelimit.AuthLimit(deps.RateLimiter, ratelimit.MagicLinkIPLimit, ratelimit.MagicLinkIPLimit, ratelimit.MagicLinkWindow, func(r *http.Request) string {
		return ratelimit.ClientIP(r)
	})
	mux.Handle("POST /account/security/set", securityLimit(deps.requireRetailCustomer(http.HandlerFunc(deps.handleAccountPasswordSet))))
	mux.Handle("POST /account/security/change", securityLimit(deps.requireRetailCustomer(http.HandlerFunc(deps.handleAccountPasswordChange))))
	mux.Handle("POST /account/verify-email/send", magicLinkLimit(deps.requireRetailCustomer(http.HandlerFunc(deps.handleAccountVerifyEmailSend))))

	// Retail customer account routes — requires authenticated retail customer
	accountMux := http.NewServeMux()
	accountMux.HandleFunc("GET /account/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/account/settings", http.StatusSeeOther)
	})
	accountMux.HandleFunc("GET /account/settings", deps.handleAccountSettings)
	accountMux.HandleFunc("POST /account/settings", deps.handleAccountSettingsUpdate)
	accountMux.HandleFunc("GET /account/orders", deps.handleAccountOrders)
	accountMux.HandleFunc("GET /account/orders/{id}", deps.handleAccountOrderShow)
	accountMux.HandleFunc("GET /account/subscriptions", deps.handleAccountSubscriptions)
	accountMux.HandleFunc("POST /account/subscriptions/{id}/pause", deps.handleAccountSubscriptionPause)
	accountMux.HandleFunc("POST /account/subscriptions/{id}/resume", deps.handleAccountSubscriptionResume)
	accountMux.HandleFunc("POST /account/subscriptions/{id}/cancel", deps.handleAccountSubscriptionCancel)
	accountMux.HandleFunc("POST /account/billing-portal", deps.handleAccountBillingPortal)
	accountMux.HandleFunc("GET /account/addresses", deps.handleAccountAddresses)
	accountMux.HandleFunc("POST /account/addresses", deps.handleAccountAddressCreate)
	accountMux.HandleFunc("POST /account/addresses/{id}", deps.handleAccountAddressUpdate)
	accountMux.HandleFunc("POST /account/addresses/{id}/delete", deps.handleAccountAddressDelete)
	accountMux.HandleFunc("POST /account/addresses/{id}/default", deps.handleAccountAddressSetDefault)
	accountMux.HandleFunc("GET /account/security", deps.handleAccountSecurity)
	mux.Handle("GET /account/{path...}", deps.requireRetailCustomer(accountMux))
	mux.Handle("POST /account/{path...}", deps.requireRetailCustomer(accountMux))

	// Wholesale auth routes (password, no session required)
	wholesaleAuthLimit := ratelimit.AuthLimit(deps.RateLimiter, ratelimit.AuthIPLimit, ratelimit.AuthIdentifierLimit, ratelimit.AuthWindow, func(r *http.Request) string {
		return r.FormValue("email")
	})
	mux.HandleFunc("GET /wholesale/login", deps.handleWholesaleLoginPage)
	mux.Handle("POST /wholesale/login", wholesaleAuthLimit(http.HandlerFunc(deps.handleWholesaleLogin)))

	// Wholesale logout (requires session)
	mux.Handle("POST /wholesale/logout", deps.requireCustomerSession(http.HandlerFunc(deps.handleWholesaleLogout)))

	// Wholesale portal — requires approved wholesale customer
	wholesaleMux := http.NewServeMux()
	wholesaleMux.HandleFunc("GET /wholesale/portal", deps.handleWholesaleQuickOrder)
	wholesaleMux.HandleFunc("POST /wholesale/portal/bulk-add", deps.handleWholesaleBulkAdd)
	wholesaleMux.HandleFunc("GET /wholesale/checkout", deps.handleWholesaleCheckoutPage)
	wholesaleMux.HandleFunc("GET /wholesale/order-confirmed", deps.handleWholesaleOrderConfirmed)
	wholesaleMux.HandleFunc("POST /wholesale/checkout/confirm", deps.handleWholesaleCheckoutConfirm)
	wholesaleMux.HandleFunc("POST /wholesale/cart/update", deps.handleWholesaleCartUpdate)
	wholesaleMux.HandleFunc("POST /wholesale/cart/remove", deps.handleWholesaleCartRemove)
	wholesaleMux.HandleFunc("GET /wholesale/help", deps.handleWholesaleHelpIndex)
	wholesaleMux.HandleFunc("GET /wholesale/help/{slug}", deps.handleWholesaleHelpArticle)
	// Wholesale account area (history, settings, addresses, security)
	wholesaleMux.HandleFunc("GET /wholesale/account", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/wholesale/account/orders", http.StatusSeeOther)
	})
	wholesaleMux.HandleFunc("GET /wholesale/account/orders", deps.handleWholesaleAccountOrders)
	wholesaleMux.HandleFunc("GET /wholesale/account/orders/{id}", deps.handleWholesaleAccountOrderShow)
	wholesaleMux.HandleFunc("GET /wholesale/account/settings", deps.handleWholesaleAccountSettings)
	wholesaleMux.HandleFunc("POST /wholesale/account/settings", deps.handleWholesaleAccountSettingsUpdate)
	wholesaleMux.HandleFunc("GET /wholesale/account/addresses", deps.handleWholesaleAccountAddresses)
	wholesaleMux.HandleFunc("POST /wholesale/account/addresses", deps.handleWholesaleAccountAddressCreate)
	wholesaleMux.HandleFunc("POST /wholesale/account/addresses/{id}", deps.handleWholesaleAccountAddressUpdate)
	wholesaleMux.HandleFunc("POST /wholesale/account/addresses/{id}/delete", deps.handleWholesaleAccountAddressDelete)
	wholesaleMux.HandleFunc("POST /wholesale/account/addresses/{id}/default", deps.handleWholesaleAccountAddressSetDefault)
	wholesaleMux.HandleFunc("GET /wholesale/account/security", deps.handleWholesaleAccountSecurity)
	wholesaleMux.HandleFunc("POST /wholesale/account/security/set", deps.handleWholesaleAccountPasswordSet)
	wholesaleMux.HandleFunc("POST /wholesale/account/security/change", deps.handleWholesaleAccountPasswordChange)
	mux.Handle("GET /wholesale/portal", deps.requireApprovedWholesale(wholesaleMux))
	mux.Handle("GET /wholesale/portal/", deps.requireApprovedWholesale(wholesaleMux))
	mux.Handle("POST /wholesale/portal/{path...}", deps.requireApprovedWholesale(wholesaleMux))
	mux.Handle("GET /wholesale/checkout", deps.requireApprovedWholesale(wholesaleMux))
	mux.Handle("GET /wholesale/checkout/", deps.requireApprovedWholesale(wholesaleMux))
	mux.Handle("GET /wholesale/order-confirmed", deps.requireApprovedWholesale(wholesaleMux))
	mux.Handle("POST /wholesale/checkout/{path...}", deps.requireApprovedWholesale(wholesaleMux))
	mux.Handle("POST /wholesale/cart/{path...}", deps.requireApprovedWholesale(wholesaleMux))
	mux.Handle("GET /wholesale/help", deps.requireApprovedWholesale(wholesaleMux))
	mux.Handle("GET /wholesale/help/{slug}", deps.requireApprovedWholesale(wholesaleMux))
	mux.Handle("GET /wholesale/account", deps.requireApprovedWholesale(wholesaleMux))
	mux.Handle("GET /wholesale/account/{path...}", deps.requireApprovedWholesale(wholesaleMux))
	mux.Handle("POST /wholesale/account/{path...}", deps.requireApprovedWholesale(wholesaleMux))

	// Staff auth routes (no session required)
	staffAuthLimit := ratelimit.AuthLimit(deps.RateLimiter, ratelimit.StaffIPLimit, ratelimit.StaffIdentifierLimit, ratelimit.StaffWindow, func(r *http.Request) string {
		return r.FormValue("email")
	})
	mux.HandleFunc("GET /auth/staff/login", deps.handleStaffLoginPage)
	mux.Handle("POST /auth/staff/login", staffAuthLimit(http.HandlerFunc(deps.handleStaffLogin)))

	// Admin routes — all require staff session
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("GET /admin/", deps.handleAdminDashboard)
	adminMux.HandleFunc("GET /admin/dashboard/top-sellers", deps.handleAdminTopSellers)
	adminMux.HandleFunc("GET /admin/dashboard/revenue", deps.handleAdminRevenue)

	// Admin catalog — categories
	adminMux.HandleFunc("GET /admin/categories", deps.handleAdminCategoryList)
	adminMux.HandleFunc("POST /admin/categories", deps.handleAdminCategoryCreate)
	adminMux.HandleFunc("POST /admin/categories/{id}", deps.handleAdminCategoryUpdate)
	adminMux.HandleFunc("POST /admin/categories/{id}/delete", deps.handleAdminCategoryDelete)

	// Admin catalog — products
	adminMux.HandleFunc("GET /admin/catalog", deps.handleAdminProductList)
	adminMux.HandleFunc("GET /admin/catalog/new", deps.handleAdminProductNew)
	adminMux.HandleFunc("POST /admin/catalog", deps.handleAdminProductCreate)
	adminMux.HandleFunc("GET /admin/catalog/{id}", deps.handleAdminProductEdit)
	adminMux.HandleFunc("POST /admin/catalog/{id}", deps.handleAdminProductUpdate)
	adminMux.HandleFunc("POST /admin/catalog/{id}/status", deps.handleAdminProductStatusUpdate)
	adminMux.HandleFunc("POST /admin/catalog/{id}/subscribable", deps.handleAdminProductSubscribableUpdate)
	adminMux.HandleFunc("POST /admin/catalog/{id}/featured", deps.handleAdminProductFeaturedUpdate)
	adminMux.HandleFunc("POST /admin/catalog/{id}/visibility", deps.handleAdminProductVisibilityUpdate)
	adminMux.HandleFunc("POST /admin/catalog/{id}/clone", deps.handleAdminProductClone)
	adminMux.HandleFunc("POST /admin/catalog/{id}/delete", deps.handleAdminProductDelete)
	adminMux.HandleFunc("POST /admin/catalog/{id}/variants", deps.handleAdminVariantCreate)
	adminMux.HandleFunc("POST /admin/catalog/{id}/variants/{variantID}", deps.handleAdminVariantUpdate)
	adminMux.HandleFunc("POST /admin/catalog/{id}/variants/{variantID}/archive", deps.handleAdminVariantArchive)
	adminMux.HandleFunc("POST /admin/catalog/{id}/variants/{variantID}/unarchive", deps.handleAdminVariantUnarchive)
	adminMux.HandleFunc("POST /admin/catalog/{id}/variants/{variantID}/delete", deps.handleAdminVariantDelete)
	adminMux.HandleFunc("POST /admin/catalog/{id}/variants/{variantID}/price", deps.handleAdminVariantPriceUpdate)
	adminMux.HandleFunc("POST /admin/catalog/{id}/options", deps.handleAdminOptionCreate)
	adminMux.HandleFunc("POST /admin/catalog/{id}/options/{optionID}/delete", deps.handleAdminOptionDelete)
	adminMux.HandleFunc("POST /admin/catalog/{id}/options/{optionID}/values", deps.handleAdminOptionValueCreate)
	adminMux.HandleFunc("POST /admin/catalog/{id}/options/{optionID}/values/{valueID}/delete", deps.handleAdminOptionValueDelete)

	// Admin catalog — media
	adminMux.HandleFunc("POST /admin/images/upload-url", deps.handleAdminImageUploadURL)
	adminMux.HandleFunc("POST /admin/catalog/{id}/images", deps.handleAdminProductImageCreate)
	adminMux.HandleFunc("POST /admin/catalog/{id}/images/{imageID}/delete", deps.handleAdminProductImageDelete)
	adminMux.HandleFunc("POST /admin/catalog/{id}/images/{imageID}/primary", deps.handleAdminProductImageSetPrimary)
	adminMux.HandleFunc("POST /admin/catalog/{id}/images/reorder", deps.handleAdminProductImageReorder)

	// Admin orders
	adminMux.HandleFunc("GET /admin/orders", deps.handleAdminOrderList)
	adminMux.HandleFunc("GET /admin/orders/wholesale", deps.handleAdminWholesaleOrderList)
	adminMux.HandleFunc("GET /admin/orders/new", deps.handleAdminOrderNew)
	adminMux.HandleFunc("POST /admin/orders/new", deps.handleAdminOrderCreate)
	adminMux.HandleFunc("GET /admin/orders/batch/invoices", deps.handleAdminOrderInvoiceBatch)
	adminMux.HandleFunc("GET /admin/orders/batch/packing-slips", deps.handleAdminOrderPackingSlipBatch)
	adminMux.HandleFunc("POST /admin/orders/batch/ready-for-pickup", deps.handleAdminOrderReadyForPickupBatch)
	adminMux.HandleFunc("POST /admin/orders/batch/picked-up", deps.handleAdminOrderPickedUpBatch)
	adminMux.HandleFunc("POST /admin/orders/batch/out-for-delivery", deps.handleAdminOrderOutForDeliveryBatch)
	adminMux.HandleFunc("GET /admin/orders/{id}", deps.handleAdminOrderShow)
	adminMux.HandleFunc("POST /admin/orders/{id}/cancel", deps.handleAdminOrderCancel)
	adminMux.HandleFunc("POST /admin/orders/{id}/mark-paid", deps.handleAdminOrderMarkPaid)
	adminMux.HandleFunc("POST /admin/orders/{id}/refund", deps.handleAdminOrderRefund)
	adminMux.HandleFunc("POST /admin/orders/{id}/fulfill", deps.handleAdminOrderFulfill)
	adminMux.HandleFunc("POST /admin/orders/{id}/ship", deps.handleAdminOrderShip)
	adminMux.HandleFunc("POST /admin/orders/{id}/revert-fulfillment", deps.handleAdminOrderRevertFulfillment)
	adminMux.HandleFunc("POST /admin/orders/{id}/revert-shipment", deps.handleAdminOrderRevertShipment)
	adminMux.HandleFunc("POST /admin/orders/{id}/ready-for-pickup", deps.handleAdminOrderReadyForPickup)
	adminMux.HandleFunc("POST /admin/orders/{id}/picked-up", deps.handleAdminOrderPickedUp)
	adminMux.HandleFunc("POST /admin/orders/{id}/out-for-delivery", deps.handleAdminOrderOutForDelivery)
	adminMux.HandleFunc("POST /admin/orders/{id}/shipping-method", deps.handleAdminOrderShippingMethod)
	adminMux.HandleFunc("GET /admin/orders/{id}/packing-slip", deps.handleAdminOrderPackingSlip)
	adminMux.HandleFunc("GET /admin/orders/{id}/invoice", deps.handleAdminOrderInvoice)
	adminMux.HandleFunc("POST /admin/orders/{id}/line-items/{lineItemID}/variant", deps.handleAdminOrderLineItemVariantUpdate)

	// Admin customers
	adminMux.HandleFunc("GET /admin/customers", deps.handleAdminCustomerList)
	adminMux.HandleFunc("GET /admin/customers/{id}", deps.handleAdminCustomerShow)
	adminMux.HandleFunc("POST /admin/customers/{id}/groups/add", deps.handleAdminCustomerGroupAdd)
	adminMux.HandleFunc("POST /admin/customers/{id}/groups/{groupID}/remove", deps.handleAdminCustomerGroupRemove)
	adminMux.HandleFunc("POST /admin/customers/{id}/payment-terms", deps.handleAdminCustomerPaymentTerms)
	adminMux.HandleFunc("POST /admin/customers/{id}/price-list", deps.handleAdminCustomerPriceList)
	adminMux.HandleFunc("POST /admin/customers/{id}/billing-method", deps.handleAdminCustomerBillingMethod)
	adminMux.HandleFunc("POST /admin/customers/{id}/local-fulfillment", deps.handleAdminCustomerLocalFulfillment)
	adminMux.HandleFunc("POST /admin/customers/{id}/send-password-setup", deps.handleAdminCustomerSendPasswordSetup)

	// Admin customer groups (access control for restricted products; not pricing)
	adminMux.HandleFunc("GET /admin/groups", deps.handleAdminGroupList)
	adminMux.HandleFunc("POST /admin/groups", deps.handleAdminGroupCreate)
	adminMux.HandleFunc("POST /admin/groups/{id}/delete", deps.handleAdminGroupDelete)

	// Admin price lists
	adminMux.HandleFunc("GET /admin/price-lists", deps.handleAdminPriceListList)
	adminMux.HandleFunc("POST /admin/price-lists", deps.handleAdminPriceListCreate)
	adminMux.HandleFunc("POST /admin/price-lists/{id}", deps.handleAdminPriceListUpdate)
	adminMux.HandleFunc("POST /admin/price-lists/{id}/delete", deps.handleAdminPriceListDelete)
	adminMux.HandleFunc("GET /admin/price-lists/prices", deps.handleAdminPriceListPrices)
	adminMux.HandleFunc("POST /admin/price-lists/prices/bulk", deps.handleAdminPriceListPriceBulkUpdate)

	// Admin subscription plans
	adminMux.HandleFunc("GET /admin/plans", deps.handleAdminPlanList)
	adminMux.HandleFunc("POST /admin/plans", deps.handleAdminPlanCreate)
	adminMux.HandleFunc("POST /admin/plans/{id}/deactivate", deps.handleAdminPlanDeactivate)
	adminMux.HandleFunc("POST /admin/plans/{id}/activate", deps.handleAdminPlanActivate)
	adminMux.HandleFunc("POST /admin/plans/{id}/discount", deps.handleAdminPlanUpdateDiscount)

	// Admin subscriptions
	adminMux.HandleFunc("GET /admin/subscriptions", deps.handleAdminSubscriptionList)
	adminMux.HandleFunc("GET /admin/subscriptions/{id}", deps.handleAdminSubscriptionShow)
	adminMux.HandleFunc("POST /admin/subscriptions/{id}/pause", deps.handleAdminSubscriptionPause)
	adminMux.HandleFunc("POST /admin/subscriptions/{id}/resume", deps.handleAdminSubscriptionResume)
	adminMux.HandleFunc("POST /admin/subscriptions/{id}/cancel", deps.handleAdminSubscriptionCancel)
	adminMux.HandleFunc("POST /admin/subscriptions/{id}/variant", deps.handleAdminSubscriptionVariantUpdate)
	adminMux.HandleFunc("POST /admin/subscriptions/{id}/plan", deps.handleAdminSubscriptionPlanUpdate)

	// Admin fulfillment & shipping
	adminMux.HandleFunc("GET /admin/fulfillment", deps.handleAdminFulfillmentList)
	adminMux.HandleFunc("GET /admin/wholesale/fulfillment", deps.handleAdminWholesaleFulfillmentList)
	adminMux.HandleFunc("POST /admin/orders/{id}/label", deps.handleAdminShipmentLabelCreate)
	adminMux.HandleFunc("POST /admin/orders/labels", deps.handleAdminShipmentBulkLabelCreate)
	adminMux.HandleFunc("GET /admin/shipments/{id}/label", deps.handleAdminShipmentLabelDownload)

	// Admin discounts
	adminMux.HandleFunc("GET /admin/discounts", deps.handleAdminDiscountList)

	// Admin wholesale
	adminMux.HandleFunc("GET /admin/wholesale", deps.handleAdminWholesaleList)
	adminMux.HandleFunc("POST /admin/wholesale/{id}/price-list", deps.handleAdminWholesalePriceList)
	adminMux.HandleFunc("POST /admin/wholesale/{id}/approve", deps.handleAdminWholesaleApprove)
	adminMux.HandleFunc("POST /admin/wholesale/{id}/decline", deps.handleAdminWholesaleDecline)
	adminMux.HandleFunc("POST /admin/wholesale/{id}/suspend", deps.handleAdminWholesaleSuspend)
	adminMux.HandleFunc("POST /admin/wholesale/{id}/reactivate", deps.handleAdminWholesaleReactivate)

	// Admin invoices
	adminMux.HandleFunc("GET /admin/invoices/{id}", deps.handleAdminInvoiceShow)
	adminMux.HandleFunc("POST /admin/invoices", deps.handleAdminInvoiceCreate)
	adminMux.HandleFunc("POST /admin/invoices/{id}/send", deps.handleAdminInvoiceSend)
	adminMux.HandleFunc("POST /admin/invoices/{id}/payment", deps.handleAdminInvoiceRecordPayment)
	adminMux.HandleFunc("POST /admin/invoices/{id}/void", deps.handleAdminInvoiceVoid)

	// Admin attributes
	adminMux.HandleFunc("GET /admin/attributes", deps.handleAdminAttributeSetList)
	adminMux.HandleFunc("POST /admin/attributes", deps.handleAdminAttributeSetCreate)
	adminMux.HandleFunc("GET /admin/attributes/{id}", deps.handleAdminAttributeSetEdit)
	adminMux.HandleFunc("POST /admin/attributes/{id}", deps.handleAdminAttributeSetUpdate)
	adminMux.HandleFunc("POST /admin/attributes/{id}/delete", deps.handleAdminAttributeSetDelete)
	adminMux.HandleFunc("POST /admin/attributes/{id}/keys", deps.handleAdminAttributeKeyCreate)
	adminMux.HandleFunc("GET /admin/attributes/{id}/keys/{keyID}", deps.handleAdminAttributeKeyEdit)
	adminMux.HandleFunc("POST /admin/attributes/{id}/keys/{keyID}", deps.handleAdminAttributeKeyUpdate)
	adminMux.HandleFunc("POST /admin/attributes/{id}/keys/{keyID}/delete", deps.handleAdminAttributeKeyDelete)

	// Product attributes (on product edit page)
	adminMux.HandleFunc("POST /admin/catalog/{id}/attributes/assign", deps.handleAdminProductAttributeAssign)
	adminMux.HandleFunc("POST /admin/catalog/{id}/attributes/remove", deps.handleAdminProductAttributeRemove)
	adminMux.HandleFunc("POST /admin/catalog/{id}/attributes/save", deps.handleAdminProductAttributeSave)

	// Admin settings / integrations
	adminMux.HandleFunc("GET /admin/settings", deps.handleAdminSettings)
	adminMux.HandleFunc("POST /admin/settings/shipping", deps.handleAdminShippingSettingsUpdate)
	adminMux.HandleFunc("POST /admin/settings/default-price-list", deps.handleAdminDefaultPriceListUpdate)
	adminMux.HandleFunc("GET /admin/settings/box-presets", deps.handleAdminBoxPresets)
	adminMux.HandleFunc("POST /admin/settings/box-presets", deps.handleAdminBoxPresetCreate)
	adminMux.HandleFunc("POST /admin/settings/box-presets/{id}", deps.handleAdminBoxPresetUpdate)
	adminMux.HandleFunc("POST /admin/settings/box-presets/{id}/delete", deps.handleAdminBoxPresetDelete)
	adminMux.HandleFunc("GET /admin/settings/integrations/quickbooks/connect", deps.handleAdminQBConnect)
	adminMux.HandleFunc("GET /admin/settings/integrations/quickbooks/callback", deps.handleAdminQBCallback)
	adminMux.HandleFunc("POST /admin/settings/integrations/quickbooks/disconnect", deps.handleAdminQBDisconnect)

	// Admin audit log
	adminMux.HandleFunc("GET /admin/audit", deps.handleAdminAuditList)

	// Admin help
	adminMux.HandleFunc("GET /admin/help", deps.handleAdminHelpIndex)
	adminMux.HandleFunc("GET /admin/help/{slug}", deps.handleAdminHelpArticle)

	// Staff logout (requires session)
	adminMux.HandleFunc("POST /auth/staff/logout", deps.handleStaffLogout)

	// Dev/test route
	adminMux.HandleFunc("GET /admin/dev/error", func(w http.ResponseWriter, r *http.Request) {
		Error(w, r, errors.New("simulated server error for testing"))
	})

	// Mount admin mux behind session middleware
	mux.Handle("GET /admin/", deps.requireStaffSession(adminMux))
	mux.Handle("POST /admin/{path...}", deps.requireStaffSession(adminMux))
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
	})
	mux.Handle("POST /auth/staff/logout", deps.requireStaffSession(adminMux))

	// Webhooks
	mux.HandleFunc("POST /webhooks/stripe", deps.handleStripeWebhook)
	mux.HandleFunc("POST /webhooks/quickbooks", deps.handleQBWebhook)

	// Catch-all 404 for unmatched GET routes (branded error page)
	mux.HandleFunc("GET /", deps.handleNotFoundPage)

	// Apply middleware stack (outermost runs first)
	var handler http.Handler = mux
	handler = deps.optionalCustomerSession(handler)
	handler = maxBodySizeMiddleware(handler, 1<<20) // 1 MB limit, excludes /webhooks/
	handler = requestIDMiddleware(handler)
	handler = loggingMiddleware(handler, deps.Logger, deps.Metrics)
	handler = ratelimit.GlobalLimit(deps.RateLimiter, ratelimit.GlobalIPLimit, ratelimit.GlobalWindow)(handler)
	handler = metrics.HTTPMiddleware(deps.Metrics)(handler)
	// Sentry wraps everything so it can recover panics from any middleware.
	// No-op when SENTRY_DSN is unset (the hub is a dummy).
	handler = sentryhttp.New(sentryhttp.Options{
		Repanic:         true,
		WaitForDelivery: false,
	}).Handle(handler)

	return handler
}

// maxBodySizeMiddleware limits request body size for non-webhook routes.
// Webhook endpoints manage their own body limits via io.LimitReader.
func maxBodySizeMiddleware(next http.Handler, maxBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/webhooks/") {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}
