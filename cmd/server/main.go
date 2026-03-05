package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/logging"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/platform/ratelimit"
	"github.com/dukerupert/hiri/internal/platform/sessions"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Load .env file if present
	_ = godotenv.Load()

	// Logger
	logger := logging.New(slog.LevelInfo)
	slog.SetDefault(logger)

	// Database
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	logger.Info("connected to database")

	// Infrastructure
	auditWriter := audit.NewAuditWriter()
	metricsReg := metrics.NewRegistry()
	rateLimitStore := ratelimit.NewInMemoryStore()
	limiter := ratelimit.NewLimiter(rateLimitStore)
	sessionStore := store.NewSessionStore()
	_ = sessionStore // will be used when session.Store interface is implemented
	sessionMgr := sessions.NewManager(nil) // TODO: wire session store implementing sessions.Store
	paymentProvider := payments.NewStripeProvider(
		os.Getenv("STRIPE_SECRET_KEY"),
		os.Getenv("STRIPE_WEBHOOK_SECRET"),
	)

	// Stores
	orderStore := store.NewOrderStore()
	customerStore := store.NewCustomerStore()
	catalogStore := store.NewCatalogStore()
	subscriptionStore := store.NewSubscriptionStore()
	fulfillmentStore := store.NewFulfillmentStore()
	shippingStore := store.NewShippingStore()
	webhookStore := store.NewWebhookStore()
	discountStore := store.NewDiscountStore()
	pricingStore := store.NewPricingStore()
	cartStore := store.NewCartStore()
	_ = store.NewAuditStore()

	// Services
	orderSvc := app.NewOrderService(orderStore, auditWriter, metricsReg)
	customerSvc := app.NewCustomerService(customerStore, auditWriter, metricsReg)
	catalogSvc := app.NewCatalogService(catalogStore, auditWriter, metricsReg)
	subscriptionSvc := app.NewSubscriptionService(subscriptionStore, orderStore, auditWriter, metricsReg)
	fulfillmentSvc := app.NewFulfillmentService(fulfillmentStore, shippingStore, orderStore, nil, auditWriter, metricsReg) // TODO: wire label provider
	discountSvc := app.NewDiscountService(discountStore, auditWriter, metricsReg)
	checkoutSvc := app.NewCheckoutService(orderStore, customerStore, discountStore, paymentProvider, auditWriter, metricsReg)
	pricingSvc := app.NewPricingService(pricingStore)
	cartSvc := app.NewCartService(cartStore, pricingStore)
	authSvc := app.NewAuthService(customerStore, sessionMgr, limiter, auditWriter, metricsReg)
	renewalSvc := app.NewRenewalService(subscriptionStore, orderStore, customerStore, pricingStore, paymentProvider, auditWriter, metricsReg)
	_ = renewalSvc // TODO: wire into River job workers

	// Router
	deps := &web.Deps{
		Pool:                pool,
		Logger:              logger,
		Metrics:             metricsReg,
		Sessions:            sessionMgr,
		OrderService:        orderSvc,
		CustomerService:     customerSvc,
		CatalogService:      catalogSvc,
		CheckoutService:     checkoutSvc,
		FulfillmentService:  fulfillmentSvc,
		SubscriptionService: subscriptionSvc,
		DiscountService:     discountSvc,
		AuthService:     authSvc,
		PricingService:  pricingSvc,
		CartService:     cartSvc,
		PaymentProvider: paymentProvider,
		WebhookStore:    webhookStore,
	}

	handler := web.NewRouter(deps)

	// TODO: Wire River job workers
	// river.NewClient(...)

	// HTTP server
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	errCh := make(chan error, 1)
	go func() {
		logger.Info("server starting", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}

	logger.Info("server stopped")
	return nil
}
