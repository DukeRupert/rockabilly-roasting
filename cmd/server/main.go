package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/platform/logging"
	"github.com/dukerupert/hiri/internal/platform/media"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/platform/quickbooks"
	"github.com/dukerupert/hiri/internal/platform/ratelimit"
	"github.com/dukerupert/hiri/internal/platform/sessions"
	"github.com/dukerupert/hiri/internal/platform/shipping"
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
	rateLimitStore := ratelimit.NewMemoryStore(5 * time.Minute)
	rateLimiter := ratelimit.NewLimiter(rateLimitStore)

	// Configure trusted reverse proxy CIDRs for accurate client IP extraction.
	// Comma-separated list of CIDRs, e.g. "10.0.0.0/8,172.16.0.1/32".
	if proxyCIDRs := os.Getenv("TRUSTED_PROXIES"); proxyCIDRs != "" {
		cidrs := strings.Split(proxyCIDRs, ",")
		for i := range cidrs {
			cidrs[i] = strings.TrimSpace(cidrs[i])
		}
		if err := ratelimit.SetTrustedProxies(cidrs); err != nil {
			return fmt.Errorf("trusted proxies: %w", err)
		}
		logger.Info("trusted proxies configured", "cidrs", cidrs)
	}
	sessionStore := store.NewSessionStore()
	sessionMgr := sessions.NewManager(sessionStore)
	paymentProvider := payments.NewStripeProvider(
		os.Getenv("STRIPE_SECRET_KEY"),
		os.Getenv("STRIPE_WEBHOOK_SECRET"),
	)
	labelProvider := shipping.NewEasyPostProvider(os.Getenv("EASYPOST_API_KEY"))
	mailer := email.NewPostmarkSender(os.Getenv("POSTMARK_SERVER_TOKEN"))
	emailRenderer, err := emailtemplates.New()
	if err != nil {
		return fmt.Errorf("create email renderer: %w", err)
	}
	fromAddr := os.Getenv("EMAIL_FROM")
	baseURL := os.Getenv("BASE_URL")
	storeName := os.Getenv("STORE_NAME")
	staffEmail := os.Getenv("STAFF_NOTIFICATION_EMAIL")

	// QuickBooks Online integration
	qbCredStore := store.NewQBCredentialStore()
	var qbClient quickbooks.Client
	var qbConfig quickbooks.ClientConfig
	qbWebhookVerifier := os.Getenv("QB_WEBHOOK_VERIFIER_TOKEN")
	// HMAC key for OAuth state cookie signing (derived from webhook verifier or encryption key)
	var qbOAuthHMACKey []byte
	if qbClientID := os.Getenv("QB_CLIENT_ID"); qbClientID != "" {
		qbEncKeyB64 := os.Getenv("QB_TOKEN_ENCRYPTION_KEY")
		qbEncKey, decodeErr := base64DecodeKey(qbEncKeyB64)
		if decodeErr != nil {
			return fmt.Errorf("decode QB_TOKEN_ENCRYPTION_KEY: %w", decodeErr)
		}
		// TenantID: for single-tenant, use a fixed UUID or derive from config.
		// In a multi-tenant setup this would come from the request context.
		tenantID := tenantIDFromEnv()

		qbConfig = quickbooks.ClientConfig{
			ClientID:      qbClientID,
			ClientSecret:  os.Getenv("QB_CLIENT_SECRET"),
			EncryptionKey: qbEncKey,
			Environment:   os.Getenv("QB_ENVIRONMENT"),
			RedirectURI:   os.Getenv("QB_REDIRECT_URI"),
		}
		qbClient = quickbooks.NewQBClient(qbConfig, tenantID, qbCredStore, pool)
		qbOAuthHMACKey = qbEncKey // reuse the encryption key for HMAC signing
		logger.Info("quickbooks integration configured", "environment", os.Getenv("QB_ENVIRONMENT"))
	}

	// Media (R2 storage + Cloudflare Image Transformations)
	mediaConfig := &media.Config{
		MediaBaseURL: os.Getenv("MEDIA_BASE_URL"),
	}
	r2Client, err := media.NewR2Client(ctx, media.R2Config{
		AccountID:      os.Getenv("R2_ACCOUNT_ID"),
		AccessKeyID:    os.Getenv("R2_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("R2_SECRET_ACCESS_KEY"),
		Bucket:         os.Getenv("R2_BUCKET"),
	})
	if err != nil {
		return fmt.Errorf("create r2 client: %w", err)
	}

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
	settingsStore := store.NewSettingsStore()
	attributeStore := store.NewAttributeStore()
	auditStore := store.NewAuditStore()

	// Services
	orderSvc := app.NewOrderService(orderStore, auditWriter, metricsReg)
	customerSvc := app.NewCustomerService(customerStore, auditWriter, metricsReg)
	catalogSvc := app.NewCatalogService(catalogStore, auditWriter, metricsReg)
	subscriptionSvc := app.NewSubscriptionService(subscriptionStore, orderStore, auditWriter, metricsReg)
	fulfillmentSvc := app.NewFulfillmentService(fulfillmentStore, shippingStore, orderStore, labelProvider, auditWriter, metricsReg)
	discountSvc := app.NewDiscountService(discountStore, auditWriter, metricsReg)
	checkoutSvc := app.NewCheckoutService(orderStore, customerStore, discountStore, settingsStore, paymentProvider, auditWriter, metricsReg)
	pricingSvc := app.NewPricingService(pricingStore)
	cartSvc := app.NewCartService(cartStore, pricingStore)
	staffStore := store.NewStaffStore()
	customerGroupStore := store.NewCustomerGroupStore()
	invoiceStore := store.NewInvoiceStore()
	magicLinkStore := store.NewMagicLinkStore()
	authSvc := app.NewAuthService(staffStore, customerStore, magicLinkStore, sessionMgr, auditWriter, metricsReg)
	renewalSvc := app.NewRenewalService(subscriptionStore, orderStore, customerStore, pricingStore, paymentProvider, auditWriter, metricsReg)
	wholesaleSvc := app.NewWholesaleService(customerStore, customerGroupStore, catalogStore, orderStore, cartStore, auditWriter, metricsReg)
	attributeSvc := app.NewAttributeService(attributeStore, auditWriter, metricsReg)
	invoiceSvc := app.NewInvoiceService(invoiceStore, orderStore, auditWriter, metricsReg)

	// River job workers
	workers := river.NewWorkers()
	river.AddWorker(workers, jobs.NewSubscriptionRenewalWorker(renewalSvc, pool, metricsReg))
	river.AddWorker(workers, jobs.NewBatchRenewalWorker(renewalSvc, pool, metricsReg))
	river.AddWorker(workers, jobs.NewMagicLinkSendWorker(customerStore, pool, mailer, emailRenderer, fromAddr, baseURL, storeName))
	river.AddWorker(workers, jobs.NewInvoiceSendWorker(invoiceStore, orderStore, customerStore, pool, mailer, emailRenderer, fromAddr, baseURL, storeName))
	river.AddWorker(workers, jobs.NewWholesaleApplicationNotifyWorker(customerStore, pool, mailer, emailRenderer, fromAddr, staffEmail, baseURL, storeName))
	river.AddWorker(workers, jobs.NewWholesaleApprovedWorker(customerStore, magicLinkStore, pool, mailer, emailRenderer, fromAddr, baseURL, storeName))
	river.AddWorker(workers, jobs.NewWholesaleSuspendedWorker(customerStore, pool, mailer, emailRenderer, fromAddr, baseURL, storeName))
	river.AddWorker(workers, jobs.NewOrderConfirmEmailWorker(orderStore, customerStore, catalogStore, pool, mailer, emailRenderer, fromAddr, baseURL, storeName))
	river.AddWorker(workers, jobs.NewSubscriptionConfirmEmailWorker(subscriptionStore, customerStore, catalogStore, pool, mailer, emailRenderer, fromAddr, baseURL, storeName))
	river.AddWorker(workers, jobs.NewR2ImageDeleteWorker(r2Client))
	river.AddWorker(workers, jobs.NewStoreLabelToR2Worker(fulfillmentSvc, pool, r2Client))

	// QB workers are registered after the river client is created (they need it for job chaining)
	// See below after riverClient creation.

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
	river.AddWorker(workers, jobs.NewRenewalSchedulerWorker(subscriptionSvc, pool, riverClient, metricsReg))

	// Register QB workers (need riverClient for job chaining)
	if qbClient != nil {
		river.AddWorker(workers, jobs.NewEnsureQBCustomerWorker(customerStore, qbClient, auditWriter, pool, riverClient, metricsReg))
		river.AddWorker(workers, jobs.NewCreateQBInvoiceWorker(orderStore, catalogStore, qbClient, auditWriter, pool, metricsReg))
		river.AddWorker(workers, jobs.NewProcessQBInvoiceUpdateWorker(orderStore, qbClient, auditWriter, pool, riverClient, metricsReg))
		river.AddWorker(workers, jobs.NewSyncQBCustomerWorker(customerStore, qbClient, auditWriter, pool, metricsReg))
		river.AddWorker(workers, jobs.NewSyncQBPaymentWorker(orderStore, customerStore, qbClient, auditWriter, pool, metricsReg))
		logger.Info("quickbooks workers registered")
	}

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

	// Background metrics collectors
	metrics.CollectPoolMetrics(ctx, metricsReg, pool, 15*time.Second)
	metrics.CollectRiverMetrics(ctx, metricsReg, pool, 15*time.Second)
	metrics.CollectSubscriptionMetrics(ctx, metricsReg, pool, 60*time.Second)

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
		CartService:      cartSvc,
		WholesaleService: wholesaleSvc,
		AttributeService:    attributeSvc,
		InvoiceService:   invoiceSvc,
		PaymentProvider:  paymentProvider,
		AuditStore:      auditStore,
		WebhookStore:    webhookStore,
		CustomerStore:   customerStore,
		MagicLinkStore:     magicLinkStore,
		CustomerGroupStore: customerGroupStore,
		SettingsStore:      settingsStore,
		RiverClient:        riverClient,
		R2Client:           r2Client,
		MediaConfig:        mediaConfig,
		QBClient:               qbClient,
		QBConfig:               qbConfig,
		QBCredentialStore:      qbCredStore,
		QBWebhookVerifierToken: qbWebhookVerifier,
		QBOAuthHMACKey:         qbOAuthHMACKey,
		QBHTTPClient:           &http.Client{Timeout: 30 * time.Second},
		RateLimiter:            rateLimiter,
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

	// Internal metrics server (localhost-only)
	metricsAddr := os.Getenv("METRICS_ADDR")
	if metricsAddr == "" {
		metricsAddr = "127.0.0.1:9090"
	}
	metricsSrv := &http.Server{
		Addr:         metricsAddr,
		Handler:      web.MetricsMux(metricsReg),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	// Graceful shutdown
	errCh := make(chan error, 2)
	go func() {
		logger.Info("metrics server starting", "addr", metricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("metrics server: %w", err)
		}
	}()
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

	// Stop rate limit cleanup goroutine
	rateLimitStore.Stop()

	// Stop River gracefully
	if err := riverClient.Stop(shutdownCtx); err != nil {
		logger.Error("river stop", "error", err)
	}

	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("metrics server shutdown", "error", err)
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}

	logger.Info("server stopped")
	return nil
}

// base64DecodeKey decodes a base64-encoded AES-256 key (32 bytes).
func base64DecodeKey(encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, fmt.Errorf("empty encryption key")
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(key))
	}
	return key, nil
}

// tenantIDFromEnv returns the tenant ID from TENANT_ID env var, or a fixed UUID for single-tenant.
func tenantIDFromEnv() uuid.UUID {
	if s := os.Getenv("TENANT_ID"); s != "" {
		id, err := uuid.Parse(s)
		if err == nil {
			return id
		}
	}
	// Single-tenant default — a deterministic UUID for this deployment
	return uuid.MustParse("00000000-0000-0000-0000-000000000001")
}
