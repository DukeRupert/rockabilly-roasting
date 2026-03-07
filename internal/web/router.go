package web

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/platform/sessions"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/riverqueue/river"
)

// Deps holds all dependencies needed by HTTP handlers.
type Deps struct {
	Pool                *pgxpool.Pool
	Logger              *slog.Logger
	Metrics             *metrics.Registry
	Sessions            *sessions.Manager
	OrderService        *app.OrderService
	CustomerService     *app.CustomerService
	CatalogService      *app.CatalogService
	CheckoutService     *app.CheckoutService
	FulfillmentService  *app.FulfillmentService
	SubscriptionService *app.SubscriptionService
	DiscountService     *app.DiscountService
	AuthService         *app.AuthService
	PricingService      *app.PricingService
	CartService         *app.CartService
	WholesaleService    *app.WholesaleService
	InvoiceService      *app.InvoiceService
	PaymentProvider     payments.Provider
	WebhookStore        *store.WebhookStore
	CustomerStore       *store.CustomerStore
	MagicLinkStore      *store.MagicLinkStore
	RiverClient         *river.Client[pgx.Tx]
}

// NewRouter creates a new HTTP router with all routes and middleware registered.
func NewRouter(deps *Deps) http.Handler {
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	})

	// Static assets
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler("internal/ui/assets")))

	// Storefront routes
	mux.HandleFunc("GET /{$}", deps.handleStorefrontHome)
	mux.HandleFunc("GET /catalog", deps.handleStorefrontCatalog)
	mux.HandleFunc("GET /catalog/{slug}", deps.handleStorefrontProduct)

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
	mux.HandleFunc("GET /checkout", deps.handleCheckoutPage)
	mux.HandleFunc("GET /api/checkout/cart", deps.handleCheckoutCart)
	mux.HandleFunc("POST /api/checkout/address", deps.handleCheckoutAddress)
	mux.HandleFunc("POST /api/checkout/payment-intent", deps.handleCheckoutPaymentIntent)
	mux.HandleFunc("POST /api/checkout/confirm", deps.handleCheckoutConfirm)

	// Wholesale application (public)
	mux.HandleFunc("GET /wholesale/apply", deps.handleWholesaleApplyPage)
	mux.HandleFunc("POST /wholesale/apply", deps.handleWholesaleApply)

	// Retail account auth routes (magic link, no session required)
	mux.HandleFunc("GET /account/login", deps.handleAccountLoginPage)
	mux.HandleFunc("POST /account/login", deps.handleAccountLoginRequest)
	mux.HandleFunc("GET /account/magic", deps.handleAccountMagicRedeem)
	mux.Handle("POST /account/logout", deps.requireCustomerSession(http.HandlerFunc(deps.handleAccountLogout)))

	// Wholesale auth routes (password, no session required)
	mux.HandleFunc("GET /wholesale/login", deps.handleWholesaleLoginPage)
	mux.HandleFunc("POST /wholesale/login", deps.handleWholesaleLogin)

	// Wholesale logout (requires session)
	mux.Handle("POST /wholesale/logout", deps.requireCustomerSession(http.HandlerFunc(deps.handleWholesaleLogout)))

	// Wholesale portal — requires approved wholesale customer
	wholesaleMux := http.NewServeMux()
	wholesaleMux.HandleFunc("GET /wholesale/portal", deps.handleWholesaleQuickOrder)
	wholesaleMux.HandleFunc("POST /wholesale/portal/bulk-add", deps.handleWholesaleBulkAdd)
	wholesaleMux.HandleFunc("GET /wholesale/checkout", deps.handleWholesaleCheckoutPage)
	wholesaleMux.HandleFunc("POST /wholesale/checkout/confirm", deps.handleWholesaleCheckoutConfirm)
	mux.Handle("/wholesale/portal", deps.requireApprovedWholesale(wholesaleMux))
	mux.Handle("/wholesale/portal/", deps.requireApprovedWholesale(wholesaleMux))
	mux.Handle("/wholesale/checkout", deps.requireApprovedWholesale(wholesaleMux))
	mux.Handle("/wholesale/checkout/", deps.requireApprovedWholesale(wholesaleMux))

	// Staff auth routes (no session required)
	mux.HandleFunc("GET /auth/staff/login", deps.handleStaffLoginPage)
	mux.HandleFunc("POST /auth/staff/login", deps.handleStaffLogin)

	// Admin routes — all require staff session
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("GET /admin/", deps.handleAdminDashboard)

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
	adminMux.HandleFunc("POST /admin/catalog/{id}/delete", deps.handleAdminProductDelete)
	adminMux.HandleFunc("POST /admin/catalog/{id}/variants", deps.handleAdminVariantCreate)
	adminMux.HandleFunc("POST /admin/catalog/{id}/variants/{variantID}", deps.handleAdminVariantUpdate)
	adminMux.HandleFunc("POST /admin/catalog/{id}/variants/{variantID}/delete", deps.handleAdminVariantDelete)
	adminMux.HandleFunc("POST /admin/catalog/{id}/variants/{variantID}/price", deps.handleAdminVariantPriceUpdate)
	adminMux.HandleFunc("POST /admin/catalog/{id}/options", deps.handleAdminOptionCreate)
	adminMux.HandleFunc("POST /admin/catalog/{id}/options/{optionID}/delete", deps.handleAdminOptionDelete)
	adminMux.HandleFunc("POST /admin/catalog/{id}/options/{optionID}/values", deps.handleAdminOptionValueCreate)
	adminMux.HandleFunc("POST /admin/catalog/{id}/options/{optionID}/values/{valueID}/delete", deps.handleAdminOptionValueDelete)

	// Admin orders
	adminMux.HandleFunc("GET /admin/orders", deps.handleAdminOrderList)
	adminMux.HandleFunc("GET /admin/orders/{id}", deps.handleAdminOrderShow)
	adminMux.HandleFunc("POST /admin/orders/{id}/cancel", deps.handleAdminOrderCancel)
	adminMux.HandleFunc("POST /admin/orders/{id}/refund", deps.handleAdminOrderRefund)
	adminMux.HandleFunc("POST /admin/orders/{id}/fulfill", deps.handleAdminOrderFulfill)
	adminMux.HandleFunc("POST /admin/orders/{id}/ship", deps.handleAdminOrderShip)
	adminMux.HandleFunc("GET /admin/orders/{id}/packing-slip", deps.handleAdminOrderPackingSlip)

	// Admin customers
	adminMux.HandleFunc("GET /admin/customers", deps.handleAdminCustomerList)
	adminMux.HandleFunc("GET /admin/customers/{id}", deps.handleAdminCustomerShow)

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

	// Admin fulfillment
	adminMux.HandleFunc("GET /admin/fulfillment", deps.handleAdminFulfillmentList)

	// Admin discounts
	adminMux.HandleFunc("GET /admin/discounts", deps.handleAdminDiscountList)

	// Admin wholesale
	adminMux.HandleFunc("GET /admin/wholesale", deps.handleAdminWholesaleList)
	adminMux.HandleFunc("POST /admin/wholesale/{id}/approve", deps.handleAdminWholesaleApprove)
	adminMux.HandleFunc("POST /admin/wholesale/{id}/decline", deps.handleAdminWholesaleDecline)
	adminMux.HandleFunc("POST /admin/wholesale/{id}/suspend", deps.handleAdminWholesaleSuspend)

	// Admin invoices
	adminMux.HandleFunc("GET /admin/invoices/{id}", deps.handleAdminInvoiceShow)
	adminMux.HandleFunc("POST /admin/invoices", deps.handleAdminInvoiceCreate)
	adminMux.HandleFunc("POST /admin/invoices/{id}/send", deps.handleAdminInvoiceSend)
	adminMux.HandleFunc("POST /admin/invoices/{id}/payment", deps.handleAdminInvoiceRecordPayment)
	adminMux.HandleFunc("POST /admin/invoices/{id}/void", deps.handleAdminInvoiceVoid)

	// Staff logout (requires session)
	adminMux.HandleFunc("POST /auth/staff/logout", deps.handleStaffLogout)

	// Dev/test route
	adminMux.HandleFunc("GET /admin/dev/error", func(w http.ResponseWriter, r *http.Request) {
		Error(w, r, errors.New("simulated server error for testing"))
	})

	// Mount admin mux behind session middleware
	mux.Handle("/admin/", deps.requireStaffSession(adminMux))
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
	})
	mux.Handle("/auth/staff/logout", deps.requireStaffSession(adminMux))

	// Webhooks
	mux.HandleFunc("POST /webhooks/stripe", deps.handleStripeWebhook)

	// Apply middleware stack
	var handler http.Handler = mux
	handler = requestIDMiddleware(handler)
	handler = loggingMiddleware(handler, deps.Logger, deps.Metrics)

	return handler
}
