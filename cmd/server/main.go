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
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/jobs"
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

	// River job workers
	workers := river.NewWorkers()
	river.AddWorker(workers, jobs.NewSubscriptionRenewalWorker(renewalSvc, pool))

	// River client — we create it first, then pass it to the scheduler worker
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
		},
		Workers: workers,
		PeriodicJobs: []*river.PeriodicJob{
			river.NewPeriodicJob(
				river.PeriodicInterval(1*time.Minute),
				func() (river.JobArgs, *river.InsertOpts) {
					return jobs.RenewalSchedulerArgs{}, &river.InsertOpts{
						UniqueOpts: river.UniqueOpts{
							ByPeriod: 1 * time.Minute,
						},
					}
				},
				&river.PeriodicJobOpts{RunOnStart: true},
			),
		},
	})
	if err != nil {
		return fmt.Errorf("create river client: %w", err)
	}

	// Register scheduler worker (needs the client for transactional inserts)
	river.AddWorker(workers, jobs.NewRenewalSchedulerWorker(subscriptionSvc, pool, riverClient))

	// Run River migrations
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("create river migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("river migrate: %w", err)
	}
	logger.Info("river migrations complete")

	// Start River client
	if err := riverClient.Start(ctx); err != nil {
		return fmt.Errorf("start river client: %w", err)
	}
	logger.Info("river workers started")

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
		CustomerStore:   customerStore,
	}

	handler := web.NewRouter(deps)

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

	// Stop River gracefully
	if err := riverClient.Stop(shutdownCtx); err != nil {
		logger.Error("river stop", "error", err)
	}

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}

	logger.Info("server stopped")
	return nil
}
