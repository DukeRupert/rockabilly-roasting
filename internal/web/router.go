package web

import (
	"log/slog"
	"net/http"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/sessions"
)

// Deps holds all dependencies needed by HTTP handlers.
type Deps struct {
	Logger        *slog.Logger
	Metrics       *metrics.Registry
	Sessions      *sessions.Manager
	OrderService  *app.OrderService
	CustomerService *app.CustomerService
	CatalogService  *app.CatalogService
	CheckoutService *app.CheckoutService
	FulfillmentService *app.FulfillmentService
	SubscriptionService *app.SubscriptionService
	AuthService  *app.AuthService
}

// NewRouter creates a new HTTP router with all routes and middleware registered.
func NewRouter(deps *Deps) http.Handler {
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	})

	// Storefront routes
	// TODO: register storefront handlers

	// Admin routes
	// TODO: register admin handlers

	// API routes
	// TODO: register API handlers

	// Apply middleware stack
	var handler http.Handler = mux
	handler = requestIDMiddleware(handler)
	handler = loggingMiddleware(handler, deps.Logger, deps.Metrics)

	return handler
}
