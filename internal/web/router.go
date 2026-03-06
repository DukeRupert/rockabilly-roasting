package web

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/platform/sessions"
	"github.com/dukerupert/hiri/internal/store"
)

// Deps holds all dependencies needed by HTTP handlers.
type Deps struct {
	Pool          *pgxpool.Pool
	Logger        *slog.Logger
	Metrics       *metrics.Registry
	Sessions      *sessions.Manager
	OrderService        *app.OrderService
	CustomerService     *app.CustomerService
	CatalogService      *app.CatalogService
	CheckoutService     *app.CheckoutService
	FulfillmentService  *app.FulfillmentService
	SubscriptionService *app.SubscriptionService
	DiscountService     *app.DiscountService
	AuthService     *app.AuthService
	PricingService  *app.PricingService
	CartService     *app.CartService
	PaymentProvider payments.Provider
	WebhookStore    *store.WebhookStore
	CustomerStore   *store.CustomerStore
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

	// Admin routes
	mux.HandleFunc("GET /admin", deps.handleAdminDashboard)

	// Admin catalog — categories (registered before product wildcard routes)
	mux.HandleFunc("GET /admin/categories", deps.handleAdminCategoryList)
	mux.HandleFunc("POST /admin/categories", deps.handleAdminCategoryCreate)
	mux.HandleFunc("POST /admin/categories/{id}", deps.handleAdminCategoryUpdate)
	mux.HandleFunc("POST /admin/categories/{id}/delete", deps.handleAdminCategoryDelete)

	// Admin catalog — products
	mux.HandleFunc("GET /admin/catalog", deps.handleAdminProductList)
	mux.HandleFunc("GET /admin/catalog/new", deps.handleAdminProductNew)
	mux.HandleFunc("POST /admin/catalog", deps.handleAdminProductCreate)
	mux.HandleFunc("GET /admin/catalog/{id}", deps.handleAdminProductEdit)
	mux.HandleFunc("POST /admin/catalog/{id}", deps.handleAdminProductUpdate)
	mux.HandleFunc("POST /admin/catalog/{id}/status", deps.handleAdminProductStatusUpdate)
	mux.HandleFunc("POST /admin/catalog/{id}/subscribable", deps.handleAdminProductSubscribableUpdate)
	mux.HandleFunc("POST /admin/catalog/{id}/delete", deps.handleAdminProductDelete)
	mux.HandleFunc("POST /admin/catalog/{id}/variants", deps.handleAdminVariantCreate)
	mux.HandleFunc("POST /admin/catalog/{id}/variants/{variantID}", deps.handleAdminVariantUpdate)
	mux.HandleFunc("POST /admin/catalog/{id}/variants/{variantID}/delete", deps.handleAdminVariantDelete)
	mux.HandleFunc("POST /admin/catalog/{id}/variants/{variantID}/price", deps.handleAdminVariantPriceUpdate)
	mux.HandleFunc("POST /admin/catalog/{id}/options", deps.handleAdminOptionCreate)
	mux.HandleFunc("POST /admin/catalog/{id}/options/{optionID}/delete", deps.handleAdminOptionDelete)
	mux.HandleFunc("POST /admin/catalog/{id}/options/{optionID}/values", deps.handleAdminOptionValueCreate)
	mux.HandleFunc("POST /admin/catalog/{id}/options/{optionID}/values/{valueID}/delete", deps.handleAdminOptionValueDelete)

	// Admin orders
	mux.HandleFunc("GET /admin/orders", deps.handleAdminOrderList)
	mux.HandleFunc("GET /admin/orders/{id}", deps.handleAdminOrderShow)

	// Admin customers
	mux.HandleFunc("GET /admin/customers", deps.handleAdminCustomerList)
	mux.HandleFunc("GET /admin/customers/{id}", deps.handleAdminCustomerShow)

	// Admin subscription plans
	mux.HandleFunc("GET /admin/plans", deps.handleAdminPlanList)
	mux.HandleFunc("POST /admin/plans", deps.handleAdminPlanCreate)
	mux.HandleFunc("POST /admin/plans/{id}/deactivate", deps.handleAdminPlanDeactivate)
	mux.HandleFunc("POST /admin/plans/{id}/activate", deps.handleAdminPlanActivate)
	mux.HandleFunc("POST /admin/plans/{id}/discount", deps.handleAdminPlanUpdateDiscount)

	// Admin subscriptions
	mux.HandleFunc("GET /admin/subscriptions", deps.handleAdminSubscriptionList)
	mux.HandleFunc("GET /admin/subscriptions/{id}", deps.handleAdminSubscriptionShow)
	mux.HandleFunc("POST /admin/subscriptions/{id}/pause", deps.handleAdminSubscriptionPause)
	mux.HandleFunc("POST /admin/subscriptions/{id}/resume", deps.handleAdminSubscriptionResume)
	mux.HandleFunc("POST /admin/subscriptions/{id}/cancel", deps.handleAdminSubscriptionCancel)

	// Admin fulfillment
	mux.HandleFunc("GET /admin/fulfillment", deps.handleAdminFulfillmentList)

	// Admin discounts
	mux.HandleFunc("GET /admin/discounts", deps.handleAdminDiscountList)

	// Dev/test route — triggers a server error for toast testing
	mux.HandleFunc("GET /admin/dev/error", func(w http.ResponseWriter, r *http.Request) {
		Error(w, r, errors.New("simulated server error for testing"))
	})

	// Webhooks
	mux.HandleFunc("POST /webhooks/stripe", deps.handleStripeWebhook)

	// API routes
	// TODO: register API handlers

	// Apply middleware stack
	var handler http.Handler = mux
	handler = requestIDMiddleware(handler)
	handler = loggingMiddleware(handler, deps.Logger, deps.Metrics)

	return handler
}
