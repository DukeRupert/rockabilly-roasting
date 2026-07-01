package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/dukerupert/hiri/docs"
	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/build"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/platform/help"
	"github.com/dukerupert/hiri/internal/platform/logging"
	"github.com/dukerupert/hiri/internal/platform/media"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/platform/quickbooks"
	"github.com/dukerupert/hiri/internal/platform/ratelimit"
	hiresentry "github.com/dukerupert/hiri/internal/platform/sentry"
	"github.com/dukerupert/hiri/internal/platform/sessions"
	"github.com/dukerupert/hiri/internal/platform/shipping"
	"github.com/dukerupert/hiri/internal/platform/turnstile"
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

	// Sentry — must init before the logger so slog records can be forwarded.
	sentryEnabled, err := hiresentry.Init(hiresentry.Config{
		DSN:              os.Getenv("SENTRY_DSN"),
		Environment:      os.Getenv("SENTRY_ENVIRONMENT"),
		Release:          os.Getenv("SENTRY_RELEASE"),
		TracesSampleRate: 0,
	})
	if err != nil {
		return fmt.Errorf("sentry init: %w", err)
	}
	if sentryEnabled {
		defer hiresentry.Flush(2 * time.Second)
	}

	// Logger — JSON to stdout, plus Sentry fanout when enabled.
	logger := buildLogger(sentryEnabled)
	slog.SetDefault(logger)
	logger.Info("starting hiri", "version", build.Version, "commit", build.Commit)
	if sentryEnabled {
		logger.Info("sentry enabled", "environment", os.Getenv("SENTRY_ENVIRONMENT"))
	}

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

	turnstileSiteKey := strings.TrimSpace(os.Getenv("TURNSTILE_SITE_KEY"))
	turnstileVerifier := turnstile.New(os.Getenv("TURNSTILE_SECRET_KEY"))
	if turnstileVerifier.Enabled() {
		logger.Info("turnstile verification enabled")
	} else {
		logger.Info("turnstile verification disabled (no secret configured)")
	}

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
	// Shipping label provider. Shippo is the active provider; EasyPost code
	// stays compiled (internal/platform/shipping/easypost.go) as a fallback.
	var labelProvider shipping.LabelProvider
	if key := os.Getenv("SHIPPO_API_KEY"); key != "" {
		// SHIPPO_LABEL_FORMAT controls the label_file_type Shippo returns
		// (PNG/PDF/PDF_4x6). Defaults to PNG — the format the merchant's label
		// printer prints cleanest. Invalid values fall back to the default.
		labelFormat := os.Getenv("SHIPPO_LABEL_FORMAT")
		if labelFormat == "" {
			labelFormat = "PNG"
		}
		if !shipping.ValidLabelFileType(labelFormat) {
			logger.Warn("SHIPPO_LABEL_FORMAT is invalid; falling back to PNG", "value", labelFormat, "allowed", shipping.AllowedLabelFileTypes)
			labelFormat = "PNG"
		}
		labelProvider = shipping.NewShippoProvider(key).WithDefaultLabelFileType(labelFormat)
		logger.Info("shippo label provider configured", "label_format", labelFormat)
		if os.Getenv("SHIPPO_WEBHOOK_SECRET") == "" {
			logger.Warn("SHIPPO_WEBHOOK_SECRET is not set; the inbound tracking webhook endpoint is disabled")
		}
	} else {
		// Fall back to EasyPost for envs that haven't migrated yet. Once
		// every environment has SHIPPO_API_KEY set this branch can go.
		labelProvider = shipping.NewEasyPostProvider(os.Getenv("EASYPOST_API_KEY"))
	}
	shippoWebhookSecret := os.Getenv("SHIPPO_WEBHOOK_SECRET")
	mailer := email.NewPostmarkSender(os.Getenv("POSTMARK_SERVER_TOKEN"))
	emailRenderer, err := emailtemplates.New()
	if err != nil {
		return fmt.Errorf("create email renderer: %w", err)
	}
	fromAddr := os.Getenv("EMAIL_FROM")
	baseURL := os.Getenv("BASE_URL")
	storeName := os.Getenv("STORE_NAME")
	staffEmail := os.Getenv("STAFF_NOTIFICATION_EMAIL")

	merchantTZName := os.Getenv("MERCHANT_TIMEZONE")
	if merchantTZName == "" {
		merchantTZName = "America/Los_Angeles"
	}
	merchantTZ, err := time.LoadLocation(merchantTZName)
	if err != nil {
		return fmt.Errorf("load MERCHANT_TIMEZONE %q: %w", merchantTZName, err)
	}
	logger.Info("merchant timezone configured", "tz", merchantTZName)

	// Subscriptions renew at this hour (merchant-local) instead of each sub's
	// signup time-of-day, so the day's renewals batch into one pre-dawn window
	// and orders are ready for staff to fulfill in the morning. Default 2am.
	renewalAnchorHour := 2
	if raw := os.Getenv("RENEWAL_ANCHOR_HOUR"); raw != "" {
		h, convErr := strconv.Atoi(raw)
		if convErr != nil || h < 0 || h > 23 {
			return fmt.Errorf("RENEWAL_ANCHOR_HOUR must be an integer 0–23, got %q", raw)
		}
		renewalAnchorHour = h
	}
	logger.Info("subscription renewal anchor configured", "hour", renewalAnchorHour, "tz", merchantTZName)

	// Renewal notification emails (receipt, past-due, subscription-ended) that
	// would otherwise fire from the pre-dawn batch are held until this hour
	// (merchant-local) so customers aren't emailed at 2am. Default 8am. Daytime
	// renewals (e.g. manual admin "renew now") still email immediately.
	renewalEmailSendHour := 8
	if raw := os.Getenv("RENEWAL_EMAIL_SEND_HOUR"); raw != "" {
		h, convErr := strconv.Atoi(raw)
		if convErr != nil || h < 0 || h > 23 {
			return fmt.Errorf("RENEWAL_EMAIL_SEND_HOUR must be an integer 0–23, got %q", raw)
		}
		renewalEmailSendHour = h
	}
	logger.Info("renewal notification send hour configured", "hour", renewalEmailSendHour, "tz", merchantTZName)

	// QuickBooks Online integration
	qbCredStore := store.NewQBCredentialStore()
	var qbClient quickbooks.Client
	var qbOAuthManager *quickbooks.OAuthManager
	qbWebhookVerifier := os.Getenv("QB_WEBHOOK_VERIFIER_TOKEN")
	secureCookies := os.Getenv("INSECURE_COOKIES") != "true"
	qbHTTPClient := &http.Client{Timeout: 30 * time.Second}
	if qbClientID := os.Getenv("QB_CLIENT_ID"); qbClientID != "" {
		if qbWebhookVerifier == "" {
			return fmt.Errorf("QB_WEBHOOK_VERIFIER_TOKEN is required when QB_CLIENT_ID is set")
		}
		qbEncKeyB64 := os.Getenv("QB_TOKEN_ENCRYPTION_KEY")
		qbEncKey, decodeErr := base64DecodeKey(qbEncKeyB64)
		if decodeErr != nil {
			return fmt.Errorf("decode QB_TOKEN_ENCRYPTION_KEY: %w", decodeErr)
		}
		// TenantID: for single-tenant, use a fixed UUID or derive from config.
		// In a multi-tenant setup this would come from the request context.
		tenantID := tenantIDFromEnv()

		qbConfig := quickbooks.ClientConfig{
			ClientID:      qbClientID,
			ClientSecret:  os.Getenv("QB_CLIENT_SECRET"),
			EncryptionKey: qbEncKey,
			Environment:   os.Getenv("QB_ENVIRONMENT"),
			RedirectURI:   os.Getenv("QB_REDIRECT_URI"),
		}
		qbConcrete := quickbooks.NewQBClient(qbConfig, tenantID, qbCredStore, pool)
		qbClient = qbConcrete
		qbOAuthManager = quickbooks.NewOAuthManager(
			qbConfig, qbConcrete, qbCredStore, tenantID,
			qbEncKey, // reuse the encryption key for HMAC signing
			qbHTTPClient, secureCookies,
		)
		logger.Info("quickbooks integration configured", "environment", os.Getenv("QB_ENVIRONMENT"))
	}

	// Media (R2 storage + Cloudflare Image Transformations)
	mediaConfig := &media.Config{
		MediaBaseURL: os.Getenv("MEDIA_BASE_URL"),
	}
	r2Client, err := media.NewR2Client(ctx, media.R2Config{
		AccountID:       os.Getenv("R2_ACCOUNT_ID"),
		AccessKeyID:     os.Getenv("R2_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("R2_SECRET_ACCESS_KEY"),
		Bucket:          os.Getenv("R2_BUCKET"),
	})
	if err != nil {
		return fmt.Errorf("create r2 client: %w", err)
	}

	// Stores
	orderStore := store.NewOrderStore(metricsReg)
	customerStore := store.NewCustomerStore()
	catalogStore := store.NewCatalogStore()
	subscriptionStore := store.NewSubscriptionStore(metricsReg)
	fulfillmentStore := store.NewFulfillmentStore()
	shippingStore := store.NewShippingStore()
	boxPresetStore := store.NewBoxPresetStore()
	webhookStore := store.NewWebhookStore()
	discountStore := store.NewDiscountStore()
	pricingStore := store.NewPricingStore()
	cartStore := store.NewCartStore()
	settingsStore := store.NewSettingsStore()
	attributeStore := store.NewAttributeStore()
	auditStore := store.NewAuditStore()
	staffStore := store.NewStaffStore()
	customerGroupStore := store.NewCustomerGroupStore()
	priceListStore := store.NewPriceListStore()
	invoiceStore := store.NewInvoiceStore()
	magicLinkStore := store.NewMagicLinkStore()
	staffInviteTokenStore := store.NewStaffInviteTokenStore()

	// Email environment shared by every service that sends transactional email.
	emailEnv := app.EmailEnv{
		Mailer:     mailer,
		Renderer:   emailRenderer,
		FromAddr:   fromAddr,
		BaseURL:    baseURL,
		StoreName:  storeName,
		StaffEmail: staffEmail,
	}

	// Services. Those that send email have email-capable variants attached via WithEmail.
	catalogSvc := app.NewCatalogService(catalogStore, customerStore, customerGroupStore, auditWriter, metricsReg)
	orderSvc := app.NewOrderService(orderStore, auditWriter, metricsReg).
		WithEmail(emailEnv, customerStore, catalogStore, subscriptionStore).
		WithShipments(shippingStore).
		WithDiscounts(discountStore).
		WithPricing(pricingStore)
	customerSvc := app.NewCustomerService(customerStore, auditWriter, metricsReg)
	subscriptionSvc := app.NewSubscriptionService(subscriptionStore, orderStore, auditWriter, metricsReg).
		WithEmail(emailEnv, customerStore, catalogStore).
		WithCatalog(catalogStore, pricingStore).
		WithRenewalAnchor(merchantTZ, renewalAnchorHour)
	fulfillmentSvc := app.NewFulfillmentService(fulfillmentStore, shippingStore, orderStore, boxPresetStore, customerStore, catalogStore, labelProvider, auditWriter, metricsReg)
	discountSvc := app.NewDiscountService(discountStore, auditWriter, metricsReg)
	checkoutSvc := app.NewCheckoutService(orderStore, customerStore, discountStore, settingsStore, shippingStore, paymentProvider, auditWriter, metricsReg)
	pricingSvc := app.NewPricingService(pricingStore, customerStore).
		WithSettings(settingsStore)
	cartSvc := app.NewCartService(cartStore, catalogStore, pricingSvc, catalogSvc)
	authSvc := app.NewAuthService(staffStore, customerStore, magicLinkStore, staffInviteTokenStore, sessionMgr, auditWriter, metricsReg).
		WithEmail(emailEnv)
	staffSvc := app.NewStaffService(staffStore, auditWriter, metricsReg).
		WithEmail(emailEnv, authSvc)
	renewalSvc := app.NewRenewalService(subscriptionStore, orderStore, customerStore, pricingStore, shippingStore, paymentProvider, auditWriter, metricsReg).
		WithTaxCalc(settingsStore, catalogStore).
		WithRenewalAnchor(merchantTZ, renewalAnchorHour)
	wholesaleSvc := app.NewWholesaleService(customerStore, customerGroupStore, catalogStore, orderStore, cartStore, auditWriter, metricsReg).
		WithEmail(emailEnv, authSvc)
	whiteLabelSvc := app.NewWhiteLabelService(catalogSvc, pricingSvc, catalogStore, customerStore, auditWriter, metricsReg).
		WithEmail(emailEnv, authSvc)
	attributeSvc := app.NewAttributeService(attributeStore, auditWriter, metricsReg)
	invoiceSvc := app.NewInvoiceService(invoiceStore, orderStore, auditWriter, metricsReg).
		WithEmail(emailEnv, customerStore)
	customerGroupSvc := app.NewCustomerGroupService(customerGroupStore, auditWriter, metricsReg)
	priceListSvc := app.NewPriceListService(priceListStore, auditWriter, metricsReg).
		WithSettings(settingsStore)
	auditQuerySvc := app.NewAuditQueryService(auditStore)
	webhookSvc := app.NewWebhookService(webhookStore)

	// River job workers. Email workers are thin delegators over the services above.
	workers := river.NewWorkers()
	river.AddWorker(workers, jobs.NewSubscriptionRenewalWorker(renewalSvc, pool, metricsReg))
	river.AddWorker(workers, jobs.NewBatchRenewalWorker(renewalSvc, pool, metricsReg))
	river.AddWorker(workers, jobs.NewMagicLinkSendWorker(authSvc, pool))
	river.AddWorker(workers, jobs.NewEmailVerifySendWorker(authSvc, pool))
	river.AddWorker(workers, jobs.NewInvoiceSendWorker(invoiceSvc, pool))
	river.AddWorker(workers, jobs.NewWholesaleApplicationNotifyWorker(wholesaleSvc, pool))
	river.AddWorker(workers, jobs.NewWholesaleApprovedWorker(wholesaleSvc, pool))
	river.AddWorker(workers, jobs.NewWholesaleSuspendedWorker(wholesaleSvc, pool))
	river.AddWorker(workers, jobs.NewWhiteLabelInviteWorker(whiteLabelSvc, pool))
	river.AddWorker(workers, jobs.NewStaffInviteWorker(staffSvc, pool))
	river.AddWorker(workers, jobs.NewWhiteLabelSubmittedWorker(whiteLabelSvc, pool))
	river.AddWorker(workers, jobs.NewOrderConfirmEmailWorker(orderSvc, pool))
	river.AddWorker(workers, jobs.NewSubscriptionConfirmEmailWorker(subscriptionSvc, pool))
	river.AddWorker(workers, jobs.NewSubscriptionRenewalReceiptWorker(orderSvc, pool))
	river.AddWorker(workers, jobs.NewSubscriptionPastDueWorker(subscriptionSvc, pool))
	river.AddWorker(workers, jobs.NewSubscriptionCancelledWorker(subscriptionSvc, pool))
	river.AddWorker(workers, jobs.NewSubscriptionDunningEndedWorker(subscriptionSvc, pool))
	river.AddWorker(workers, jobs.NewRefundConfirmationWorker(orderSvc, pool))
	river.AddWorker(workers, jobs.NewOrderShippedEmailWorker(orderSvc, pool))
	river.AddWorker(workers, jobs.NewOrderReadyForPickupEmailWorker(orderSvc, pool))
	river.AddWorker(workers, jobs.NewOrderOutForDeliveryEmailWorker(orderSvc, pool))
	river.AddWorker(workers, jobs.NewInvoicePaidEmailWorker(orderSvc, pool))
	river.AddWorker(workers, jobs.NewInvoicePastDueEmailWorker(orderSvc, pool))
	river.AddWorker(workers, jobs.NewR2ImageDeleteWorker(r2Client))
	river.AddWorker(workers, jobs.NewStoreLabelToR2Worker(fulfillmentSvc, pool, r2Client))
	river.AddWorker(workers, jobs.NewShippoTrackingUpdateWorker(fulfillmentSvc, orderSvc, pool))
	river.AddWorker(workers, jobs.NewAbandonedOrderCleanupWorker(orderSvc, pool))
	river.AddWorker(workers, jobs.NewShippedOrderAutoDeliverWorker(orderSvc, pool))

	// QB workers are registered after the river client is created (they need it for job chaining)
	// See below after riverClient creation.

	// Periodic jobs.
	periodicJobs := []*river.PeriodicJob{
		// Cancel orders that were pre-created at PI time but never had
		// payment confirmed (customer closed browser, etc.). Stripe auto-
		// cancels most async PIs at 48h via the canceled webhook; this
		// catches card-path orders that Stripe leaves in
		// requires_payment_method indefinitely.
		river.NewPeriodicJob(
			river.PeriodicInterval(1*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) {
				return jobs.AbandonedOrderCleanupArgs{}, &river.InsertOpts{
					UniqueOpts: river.UniqueOpts{
						ByPeriod: 1 * time.Hour,
					},
				}
			},
			&river.PeriodicJobOpts{RunOnStart: false},
		),
		// Mark long-shipped orders delivered. Carrier delivery is never reported
		// for legacy orders (no shipments rows) and can be missed for live ones
		// (dropped Shippo webhook), so without this they sit in the fulfillment
		// dashboard's "shipped" bucket forever. RunOnStart clears the standing
		// backlog as soon as this ships; daily thereafter is plenty.
		river.NewPeriodicJob(
			river.PeriodicInterval(24*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) {
				return jobs.ShippedOrderAutoDeliverArgs{}, &river.InsertOpts{
					UniqueOpts: river.UniqueOpts{
						ByPeriod: 24 * time.Hour,
					},
				}
			},
			&river.PeriodicJobOpts{RunOnStart: true},
		),
	}
	// Subscription renewal scheduler — scans for due subscriptions every minute
	// and enqueues renewal charges (which call Stripe). Defaults on so staging
	// and prod always renew; set DISABLE_RENEWAL_SCHEDULER in local dev to stop
	// it firing on boot against whatever Stripe key is configured.
	if os.Getenv("DISABLE_RENEWAL_SCHEDULER") == "" {
		periodicJobs = append(periodicJobs, river.NewPeriodicJob(
			river.PeriodicInterval(1*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) {
				return jobs.RenewalSchedulerArgs{}, &river.InsertOpts{
					UniqueOpts: river.UniqueOpts{
						ByPeriod: 1 * time.Minute,
					},
				}
			},
			&river.PeriodicJobOpts{RunOnStart: true},
		))
	} else {
		logger.Warn("subscription renewal scheduler disabled via DISABLE_RENEWAL_SCHEDULER")
	}
	if qbClient != nil {
		// Reconcile open wholesale QB invoices daily. This is the safety net for
		// missed Intuit webhooks and the detector that flips unpaid invoices to
		// overdue and sends the milestone past-due reminders.
		periodicJobs = append(periodicJobs, river.NewPeriodicJob(
			river.PeriodicInterval(24*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) {
				return jobs.ReconcileQBInvoicesArgs{}, &river.InsertOpts{
					UniqueOpts: river.UniqueOpts{
						ByPeriod: 24 * time.Hour,
					},
				}
			},
			&river.PeriodicJobOpts{RunOnStart: false},
		))
	}

	// River client — we create it first, then pass it to the scheduler worker
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
		},
		Workers:      workers,
		PeriodicJobs: periodicJobs,
	})
	if err != nil {
		return fmt.Errorf("create river client: %w", err)
	}

	// Now that the river client exists, attach the enqueuer to services that
	// fan out jobs from inside their own transactions (e.g. renewal-receipt
	// and past-due email enqueues atomic with the renewal write).
	enqueuer := jobs.NewEnqueuer(riverClient).WithQuietHours(merchantTZ, renewalEmailSendHour)
	renewalSvc.WithJobEnqueuer(enqueuer)
	checkoutSvc.WithCheckoutConfirmDeps(cartStore, enqueuer)
	orderSvc.WithEnqueuer(enqueuer)

	// Register scheduler worker (needs the client for transactional inserts)
	river.AddWorker(workers, jobs.NewRenewalSchedulerWorker(subscriptionSvc, pool, riverClient, metricsReg))

	// BuyLabel needs the river client to enqueue the StoreLabelToR2 follow-up
	// job atomically with the shipment write.
	river.AddWorker(workers, jobs.NewBuyLabelWorker(fulfillmentSvc, pool, riverClient))

	// Register QB workers (need riverClient for job chaining)
	if qbClient != nil {
		river.AddWorker(workers, jobs.NewEnsureQBCustomerWorker(customerStore, qbClient, auditWriter, pool, riverClient, metricsReg))
		river.AddWorker(workers, jobs.NewCreateQBInvoiceWorker(orderStore, catalogStore, qbClient, auditWriter, pool, metricsReg))
		river.AddWorker(workers, jobs.NewProcessQBInvoiceUpdateWorker(orderSvc, qbClient, pool, metricsReg))
		river.AddWorker(workers, jobs.NewReconcileQBInvoicesWorker(orderSvc, qbClient, pool, metricsReg))
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

	// Help documentation
	helpRegistry, err := help.New(docs.GuideFS)
	if err != nil {
		return fmt.Errorf("create help registry: %w", err)
	}
	logger.Info("help articles loaded")

	// Router
	deps := &web.Deps{
		Pool:                   pool,
		Logger:                 logger,
		Metrics:                metricsReg,
		Sessions:               sessionMgr,
		OrderService:           orderSvc,
		CustomerService:        customerSvc,
		CatalogService:         catalogSvc,
		CheckoutService:        checkoutSvc,
		FulfillmentService:     fulfillmentSvc,
		SubscriptionService:    subscriptionSvc,
		DiscountService:        discountSvc,
		AuthService:            authSvc,
		StaffService:           staffSvc,
		PricingService:         pricingSvc,
		CartService:            cartSvc,
		WholesaleService:       wholesaleSvc,
		WhiteLabelService:      whiteLabelSvc,
		AttributeService:       attributeSvc,
		InvoiceService:         invoiceSvc,
		CustomerGroupService:   customerGroupSvc,
		PriceListService:       priceListSvc,
		AuditQueryService:      auditQuerySvc,
		WebhookService:         webhookSvc,
		AuditWriter:            auditWriter,
		PaymentProvider:        paymentProvider,
		RiverClient:            riverClient,
		Enqueuer:               enqueuer,
		R2Client:               r2Client,
		MediaConfig:            mediaConfig,
		QBClient:               qbClient,
		QBOAuthManager:         qbOAuthManager,
		QBWebhookVerifierToken: qbWebhookVerifier,
		ShippoWebhookSecret:    shippoWebhookSecret,
		QBHTTPClient:           qbHTTPClient,
		HelpRegistry:           helpRegistry,
		RateLimiter:            rateLimiter,
		TurnstileVerifier:      turnstileVerifier,
		TurnstileSiteKey:       turnstileSiteKey,
		SecureCookies:          secureCookies,
		BaseURL:                baseURL,
		Mailer:                 mailer,
		EmailFrom:              fromAddr,
		StaffEmail:             staffEmail,
		MerchantTZ:             merchantTZ,
	}

	handler := web.NewRouter(deps)

	// HTTP server
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	srv := &http.Server{
		Addr:           addr,
		Handler:        handler,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   30 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1 MB
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
			errCh <- fmt.Errorf("http server: %w", err)
		}
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

// buildLogger assembles the slog logger. Always writes JSON to stdout; when
// Sentry is enabled, also forwards records to Sentry via the fanout handler.
func buildLogger(sentryEnabled bool) *slog.Logger {
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	if !sentryEnabled {
		return slog.New(jsonHandler)
	}
	return logging.NewWithHandlers(jsonHandler, hiresentry.NewSlogHandler(slog.LevelInfo))
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
