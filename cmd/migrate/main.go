package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// WooCommerce API types

type wcSubscription struct {
	ID              int              `json:"id"`
	ParentID        int              `json:"parent_id"`
	Status          string           `json:"status"`
	CustomerID      int              `json:"customer_id"`
	BillingPeriod   string           `json:"billing_period"`
	BillingInterval string           `json:"billing_interval"`
	Total           string           `json:"total"`
	Billing         wcAddress        `json:"billing"`
	Shipping        wcAddress        `json:"shipping"`
	PaymentMethod   string           `json:"payment_method"`
	LineItems       []wcLineItem     `json:"line_items"`
	ShippingLines   []wcShippingLine `json:"shipping_lines"`
	StartDate       string           `json:"start_date"`
	NextPaymentDate string           `json:"next_payment_date"`
	EndDate         string           `json:"end_date"`
	DateCreated     string           `json:"date_created"`
	DatePaid        string           `json:"date_paid"`
	MetaData        []wcMeta         `json:"meta_data"`
}

type wcAddress struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Company   string `json:"company"`
	Address1  string `json:"address_1"`
	Address2  string `json:"address_2"`
	City      string `json:"city"`
	State     string `json:"state"`
	Postcode  string `json:"postcode"`
	Country   string `json:"country"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
}

type wcLineItem struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	ProductID   int      `json:"product_id"`
	VariationID int      `json:"variation_id"`
	Quantity    int      `json:"quantity"`
	Price       string   `json:"price"`
	Meta        []wcMeta `json:"meta"`
}

type wcShippingLine struct {
	MethodTitle string `json:"method_title"`
	MethodID    string `json:"method_id"`
	Total       string `json:"total"`
}

type wcMeta struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// Mapping configuration

type variantMapping struct {
	WCVariationID int
	HiriVariantID uuid.UUID
	Description   string
}

type planMapping struct {
	WCPeriod   string // "week", "month"
	WCInterval string // "1", "2", "3", "4"
	HiriPlanID uuid.UUID
}

// Migration report

type migrationReport struct {
	CustomersCreated     int
	CustomersFound       int
	AddressesCreated     int
	SubscriptionsCreated int
	SubscriptionsSkipped int
	Errors               []string
	Warnings             []string
}

func (r *migrationReport) addError(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

func (r *migrationReport) addWarning(format string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	dryRun := flag.Bool("dry-run", false, "Validate mappings and report issues without importing")
	dumpMeta := flag.Bool("dump-meta", false, "Print all metadata keys from the first 3 subscriptions and exit")
	onHoldDays := flag.Int("on-hold-days", 60, "Only import on-hold subs with next_payment_date within this many days")
	mappingFile := flag.String("mapping", "", "Path to variant mapping JSON file (required for import)")
	emailFilter := flag.String("email", "", "Migrate only subscriptions for this customer email")
	emailsFile := flag.String("emails-file", "", "Migrate only subscriptions for emails listed in this file (one per line)")
	flag.Parse()

	_ = godotenv.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	wcKey := os.Getenv("WC_CONSUMER_KEY")
	wcSecret := os.Getenv("WC_CONSUMER_SECRET")
	wcBaseURL := os.Getenv("WC_BASE_URL")
	dbURL := os.Getenv("DATABASE_URL")

	if wcKey == "" || wcSecret == "" {
		return fmt.Errorf("WC_CONSUMER_KEY and WC_CONSUMER_SECRET are required")
	}
	if wcBaseURL == "" {
		wcBaseURL = "https://rockabillyroasting.com"
	}
	if dbURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	ctx := context.Background()

	// Load variant mapping
	variantMap, err := loadVariantMapping(*mappingFile)
	if err != nil && !*dryRun {
		return fmt.Errorf("load variant mapping: %w", err)
	}

	// Fetch subscriptions from WooCommerce
	logger.Info("fetching WooCommerce subscriptions...")

	activeSubs, err := fetchSubscriptions(ctx, wcBaseURL, wcKey, wcSecret, "active")
	if err != nil {
		return fmt.Errorf("fetch active subscriptions: %w", err)
	}
	logger.Info("fetched active subscriptions", "count", len(activeSubs))

	if *dumpMeta {
		limit := min(3, len(activeSubs))
		for i := 0; i < limit; i++ {
			sub := activeSubs[i]
			fmt.Printf("\n=== Sub %d (customer %d, %s) ===\n", sub.ID, sub.CustomerID, sub.Billing.Email)
			fmt.Printf("Payment method: %s\n", sub.PaymentMethod)
			for _, m := range sub.MetaData {
				fmt.Printf("  %s = %v\n", m.Key, m.Value)
			}
		}
		return nil
	}

	onHoldSubs, err := fetchSubscriptions(ctx, wcBaseURL, wcKey, wcSecret, "on-hold")
	if err != nil {
		return fmt.Errorf("fetch on-hold subscriptions: %w", err)
	}
	logger.Info("fetched on-hold subscriptions", "count", len(onHoldSubs))

	// Filter on-hold subs by recency
	cutoff := time.Now().AddDate(0, 0, -*onHoldDays)
	var recentOnHold []wcSubscription
	for _, s := range onHoldSubs {
		if nextDate := parseWCDate(s.NextPaymentDate); !nextDate.IsZero() && nextDate.After(cutoff) {
			recentOnHold = append(recentOnHold, s)
		}
	}
	logger.Info("filtered on-hold subscriptions", "recent", len(recentOnHold), "total", len(onHoldSubs))

	allSubs := append(activeSubs, recentOnHold...)

	// Apply email filter if specified
	emailSet, err := buildEmailFilter(*emailFilter, *emailsFile)
	if err != nil {
		return fmt.Errorf("build email filter: %w", err)
	}
	if len(emailSet) > 0 {
		allSubs = filterByEmail(allSubs, emailSet)
		logger.Info("filtered by email", "emails", len(emailSet), "matching_subs", len(allSubs))
	}

	logger.Info("total subscriptions to process", "count", len(allSubs))

	// Connect to database
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	customerStore := store.NewCustomerStore()
	subscriptionStore := store.NewSubscriptionStore()

	// Build plan mapping by querying existing plans
	planMap, err := buildPlanMapping(ctx, pool, subscriptionStore)
	if err != nil {
		return fmt.Errorf("build plan mapping: %w", err)
	}

	report := &migrationReport{}

	if *dryRun {
		logger.Info("=== DRY RUN MODE ===")
		dryRunValidation(ctx, allSubs, variantMap, planMap, wcBaseURL, wcKey, wcSecret, report, logger)
	} else {
		if len(variantMap) == 0 {
			return fmt.Errorf("variant mapping is required for import (use --mapping flag)")
		}
		importSubscriptions(ctx, pool, customerStore, subscriptionStore, allSubs, variantMap, planMap, wcBaseURL, wcKey, wcSecret, report, logger)
	}

	// Print report
	printReport(report, logger)
	return nil
}

func fetchSubscriptions(ctx context.Context, baseURL, key, secret, status string) ([]wcSubscription, error) {
	var all []wcSubscription
	page := 1
	for {
		url := fmt.Sprintf("%s/wp-json/wc/v1/subscriptions?consumer_key=%s&consumer_secret=%s&per_page=100&page=%d&status=%s",
			baseURL, key, secret, page, status)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request page %d: %w", page, err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response page %d: %w", page, err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("WC API returned %d: %s", resp.StatusCode, string(body[:min(200, len(body))]))
		}

		var subs []wcSubscription
		if err := json.Unmarshal(body, &subs); err != nil {
			return nil, fmt.Errorf("decode page %d: %w", page, err)
		}

		if len(subs) == 0 {
			break
		}
		all = append(all, subs...)

		if len(subs) < 100 {
			break
		}
		page++
	}
	return all, nil
}

func loadVariantMapping(path string) (map[int]uuid.UUID, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Expected format: {"242": "uuid-string", "244": "uuid-string", ...}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse mapping JSON: %w", err)
	}
	result := make(map[int]uuid.UUID, len(raw))
	for k, v := range raw {
		var wcID int
		if _, err := fmt.Sscanf(k, "%d", &wcID); err != nil {
			return nil, fmt.Errorf("invalid WC variation ID %q: %w", k, err)
		}
		uid, err := uuid.Parse(v)
		if err != nil {
			return nil, fmt.Errorf("invalid UUID for WC variation %s: %w", k, err)
		}
		result[wcID] = uid
	}
	return result, nil
}

func buildEmailFilter(single, filePath string) (map[string]bool, error) {
	result := make(map[string]bool)
	if single != "" {
		result[strings.ToLower(strings.TrimSpace(single))] = true
	}
	if filePath != "" {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read emails file: %w", err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			email := strings.ToLower(strings.TrimSpace(line))
			if email != "" && !strings.HasPrefix(email, "#") {
				result[email] = true
			}
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func filterByEmail(subs []wcSubscription, emails map[string]bool) []wcSubscription {
	var filtered []wcSubscription
	for _, sub := range subs {
		email := strings.ToLower(strings.TrimSpace(sub.Billing.Email))
		if emails[email] {
			filtered = append(filtered, sub)
		}
	}
	return filtered
}

func buildPlanMapping(ctx context.Context, pool *pgxpool.Pool, ss *store.SubscriptionStore) (map[string]uuid.UUID, error) {
	mapping := make(map[string]uuid.UUID)
	err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		plans, err := ss.ListPlans(ctx, tx)
		if err != nil {
			return err
		}
		for _, p := range plans {
			mapping[string(p.Interval)] = p.ID
		}
		return nil
	})
	return mapping, err
}

func wcIntervalToHiri(period, interval string) (domain.SubscriptionInterval, error) {
	key := period + "/" + interval
	switch key {
	case "week/1":
		return domain.SubscriptionIntervalEvery7Days, nil
	case "week/2":
		return domain.SubscriptionIntervalEvery14Days, nil
	case "week/3":
		return domain.SubscriptionIntervalEvery21Days, nil
	case "week/4":
		// 4 weeks ≈ 30 days, close enough
		return domain.SubscriptionIntervalEvery30Days, nil
	case "month/1":
		return domain.SubscriptionIntervalEvery30Days, nil
	case "month/2":
		return domain.SubscriptionIntervalEvery60Days, nil
	case "month/3":
		return domain.SubscriptionIntervalEvery90Days, nil
	default:
		return "", fmt.Errorf("unknown WC interval: %s", key)
	}
}

func parseWCDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	// WC dates are in format "2026-03-09T03:34:14"
	t, err := time.Parse("2006-01-02T15:04:05", s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// fetchParentOrder fetches a single order by ID via the v3 API.
// The v3 endpoint exposes meta_data (including Stripe IDs) that v1 strips out.
func fetchParentOrder(ctx context.Context, baseURL, key, secret string, orderID int) ([]wcMeta, error) {
	if orderID == 0 {
		return nil, nil
	}
	url := fmt.Sprintf("%s/wp-json/wc/v3/orders/%d?consumer_key=%s&consumer_secret=%s",
		baseURL, orderID, key, secret)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch order %d: %w", orderID, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read order %d: %w", orderID, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("order %d returned %d: %s", orderID, resp.StatusCode, string(body[:min(200, len(body))]))
	}

	var order struct {
		MetaData []wcMeta `json:"meta_data"`
	}
	if err := json.Unmarshal(body, &order); err != nil {
		return nil, fmt.Errorf("decode order %d: %w", orderID, err)
	}
	return order.MetaData, nil
}

// findMetaValue searches one or more metadata slices for the first matching key.
func findMetaValue(keys []string, metaSources ...[]wcMeta) string {
	for _, meta := range metaSources {
		for _, m := range meta {
			for _, key := range keys {
				if m.Key == key {
					if s, ok := m.Value.(string); ok && s != "" {
						return s
					}
				}
			}
		}
	}
	return ""
}

func getStripeCustomerID(meta ...[]wcMeta) string {
	return findMetaValue([]string{
		"_stripe_customer_id",  // WC Stripe Gateway plugin
		"_wc_stripe_customer",  // alternate key
		"_wcpay_customer_id",   // WooCommerce Payments (Stripe-powered)
	}, meta...)
}

func getStripeSourceID(meta ...[]wcMeta) string {
	return findMetaValue([]string{
		"_stripe_source_id",        // WC Stripe Gateway plugin (legacy sources)
		"_stripe_payment_method",   // WC Stripe Gateway plugin (PaymentIntents)
		"_wcpay_payment_method_id", // WooCommerce Payments
	}, meta...)
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// dryRunValidation checks all mappings without touching the database.
func dryRunValidation(ctx context.Context, subs []wcSubscription, variantMap map[int]uuid.UUID, planMap map[string]uuid.UUID, wcBaseURL, wcKey, wcSecret string, report *migrationReport, logger *slog.Logger) {
	customerEmails := make(map[string]int)   // email → WC customer ID
	variantIDs := make(map[int]string)        // WC variation ID → product name
	missingVariants := make(map[int]string)   // unmapped variation IDs
	parentOrderCache := make(map[int][]wcMeta) // parent_id → metadata

	for _, sub := range subs {
		email := strings.ToLower(strings.TrimSpace(sub.Billing.Email))
		if email == "" {
			report.addError("sub %d: no billing email", sub.ID)
			continue
		}
		customerEmails[email] = sub.CustomerID

		// Check interval mapping
		hiriInterval, err := wcIntervalToHiri(sub.BillingPeriod, sub.BillingInterval)
		if err != nil {
			report.addError("sub %d: %v", sub.ID, err)
		} else if _, ok := planMap[string(hiriInterval)]; !ok {
			report.addError("sub %d: no Hiri plan for interval %s (create plan first)", sub.ID, hiriInterval)
		}

		// Check variant mapping
		for _, li := range sub.LineItems {
			variantIDs[li.VariationID] = li.Name
			if variantMap != nil {
				if _, ok := variantMap[li.VariationID]; !ok {
					missingVariants[li.VariationID] = li.Name
				}
			}
		}

		// Fetch parent order metadata (cached) for Stripe IDs
		parentMeta, ok := parentOrderCache[sub.ParentID]
		if !ok && sub.ParentID != 0 {
			parentMeta, err = fetchParentOrder(ctx, wcBaseURL, wcKey, wcSecret, sub.ParentID)
			if err != nil {
				report.addWarning("sub %d: failed to fetch parent order %d: %v", sub.ID, sub.ParentID, err)
			}
			parentOrderCache[sub.ParentID] = parentMeta
		}

		// Check Stripe customer ID (subscription meta, then parent order meta)
		stripeID := getStripeCustomerID(sub.MetaData, parentMeta)
		if stripeID == "" {
			report.addWarning("sub %d (customer %d, %s): no Stripe customer ID", sub.ID, sub.CustomerID, email)
		}

		// Check Stripe source ID (subscription meta, then parent order meta)
		sourceID := getStripeSourceID(sub.MetaData, parentMeta)
		if sourceID == "" {
			report.addWarning("sub %d: no Stripe source/payment method ID", sub.ID)
		}

		// Check next payment date
		if sub.Status == "active" && sub.NextPaymentDate == "" {
			report.addWarning("sub %d: active but no next_payment_date", sub.ID)
		}

		// Check multi-item
		if len(sub.LineItems) > 1 {
			items := make([]string, len(sub.LineItems))
			for i, li := range sub.LineItems {
				items[i] = li.Name
			}
			report.addWarning("sub %d: multi-item subscription (%s) — will be split into %d Hiri subscriptions",
				sub.ID, strings.Join(items, ", "), len(sub.LineItems))
		}
	}

	logger.Info("unique customers by email", "count", len(customerEmails))
	logger.Info("unique WC variation IDs", "count", len(variantIDs))

	if len(missingVariants) > 0 {
		for id, name := range missingVariants {
			report.addError("WC variation %d (%s) has no Hiri mapping", id, name)
		}
	}

	// List all needed variant mappings
	if variantMap == nil {
		logger.Info("variant mapping file not provided — listing all needed mappings:")
		for id, name := range variantIDs {
			fmt.Printf("  %d → ? (%s)\n", id, name)
		}
	}
}

// importSubscriptions performs the actual migration.
func importSubscriptions(
	ctx context.Context,
	pool *pgxpool.Pool,
	cs *store.CustomerStore,
	ss *store.SubscriptionStore,
	subs []wcSubscription,
	variantMap map[int]uuid.UUID,
	planMap map[string]uuid.UUID,
	wcBaseURL, wcKey, wcSecret string,
	report *migrationReport,
	logger *slog.Logger,
) {
	// Track customers by email to avoid duplicates
	customerByEmail := make(map[string]uuid.UUID)
	// Cache parent order metadata to avoid duplicate fetches
	parentOrderCache := make(map[int][]wcMeta)

	for _, sub := range subs {
		email := strings.ToLower(strings.TrimSpace(sub.Billing.Email))
		if email == "" {
			report.addError("sub %d: no billing email, skipping", sub.ID)
			report.SubscriptionsSkipped++
			continue
		}

		// Map interval
		hiriInterval, err := wcIntervalToHiri(sub.BillingPeriod, sub.BillingInterval)
		if err != nil {
			report.addError("sub %d: %v, skipping", sub.ID, err)
			report.SubscriptionsSkipped++
			continue
		}

		planID, ok := planMap[string(hiriInterval)]
		if !ok {
			report.addError("sub %d: no plan for interval %s, skipping", sub.ID, hiriInterval)
			report.SubscriptionsSkipped++
			continue
		}

		// Fetch parent order metadata for Stripe IDs
		parentMeta, ok := parentOrderCache[sub.ParentID]
		if !ok && sub.ParentID != 0 {
			var fetchErr error
			parentMeta, fetchErr = fetchParentOrder(ctx, wcBaseURL, wcKey, wcSecret, sub.ParentID)
			if fetchErr != nil {
				report.addWarning("sub %d: failed to fetch parent order %d: %v", sub.ID, sub.ParentID, fetchErr)
			}
			parentOrderCache[sub.ParentID] = parentMeta
		}

		// Process each line item as a separate subscription (handles multi-item subs)
		for liIdx, li := range sub.LineItems {
			hiriVariantID, ok := variantMap[li.VariationID]
			if !ok {
				report.addError("sub %d item %d: no variant mapping for WC variation %d (%s), skipping",
					sub.ID, liIdx, li.VariationID, li.Name)
				report.SubscriptionsSkipped++
				continue
			}

			err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
				// 1. Find or create customer
				customerID, err := findOrCreateCustomer(ctx, tx, cs, sub, parentMeta, email, customerByEmail, report)
				if err != nil {
					return fmt.Errorf("customer: %w", err)
				}

				// 2. Create shipping address
				addressID, err := createShippingAddress(ctx, tx, cs, sub.Shipping, customerID, report)
				if err != nil {
					return fmt.Errorf("address: %w", err)
				}

				// 3. Determine subscription status and dates
				status := domain.SubscriptionStatusActive
				if sub.Status == "on-hold" {
					status = domain.SubscriptionStatusPaused
				}

				periodStart := parseWCDate(sub.DatePaid)
				if periodStart.IsZero() {
					periodStart = parseWCDate(sub.StartDate)
				}
				if periodStart.IsZero() {
					periodStart = time.Now()
				}

				nextOrderAt := parseWCDate(sub.NextPaymentDate)
				if nextOrderAt.IsZero() && status == domain.SubscriptionStatusActive {
					// Active sub with no next date — set to 30 days from now as safety
					nextOrderAt = time.Now().AddDate(0, 0, 30)
					report.addWarning("sub %d: active with no next_payment_date, defaulting to 30 days out", sub.ID)
				}
				if nextOrderAt.IsZero() {
					nextOrderAt = periodStart.AddDate(0, 0, 30)
				}

				var endsAt *time.Time
				if sub.EndDate != "" {
					t := parseWCDate(sub.EndDate)
					if !t.IsZero() {
						endsAt = &t
					}
				}

				// Build metadata
				metadata := map[string]any{
					"wc_subscription_id": sub.ID,
					"wc_customer_id":     sub.CustomerID,
					"wc_product_name":    li.Name,
					"wc_variation_id":    li.VariationID,
				}
				if stripeSource := getStripeSourceID(sub.MetaData, parentMeta); stripeSource != "" {
					metadata["stripe_payment_method_id"] = stripeSource
				}
				for _, sl := range sub.ShippingLines {
					metadata["wc_shipping_method"] = sl.MethodTitle
					break
				}

				// 4. Create subscription
				_, err = ss.Create(ctx, tx, store.CreateSubscriptionParams{
					CustomerID:         customerID,
					PlanID:             planID,
					VariantID:          hiriVariantID,
					Quantity:           max(li.Quantity, 1),
					Status:             status,
					ShippingAddressID:  addressID,
					CurrentPeriodStart: periodStart,
					CurrentPeriodEnd:   nextOrderAt,
					NextOrderAt:        nextOrderAt,
					EndsAt:             endsAt,
					Metadata:           metadata,
				})
				if err != nil {
					return fmt.Errorf("create subscription: %w", err)
				}

				report.SubscriptionsCreated++
				logger.Info("imported subscription",
					"wc_id", sub.ID,
					"product", li.Name,
					"status", status,
					"next_order", nextOrderAt.Format("2006-01-02"),
				)
				return nil
			})
			if err != nil {
				report.addError("sub %d item %d: %v", sub.ID, liIdx, err)
				report.SubscriptionsSkipped++
			}
		}
	}
}

func findOrCreateCustomer(
	ctx context.Context,
	tx pgx.Tx,
	cs *store.CustomerStore,
	sub wcSubscription,
	parentMeta []wcMeta,
	email string,
	cache map[string]uuid.UUID,
	report *migrationReport,
) (uuid.UUID, error) {
	// Check cache first
	if id, ok := cache[email]; ok {
		report.CustomersFound++
		return id, nil
	}

	stripeID := getStripeCustomerID(sub.MetaData, parentMeta)

	// Try to find existing customer by email
	existing, err := cs.GetByEmail(ctx, tx, email)
	if err == nil {
		cache[email] = existing.ID
		report.CustomersFound++

		// Update Stripe customer ID if not set
		if existing.StripeCustomerID == nil && stripeID != "" {
			_, err := cs.UpdateStripeCustomerID(ctx, tx, existing.ID, stripeID)
			if err != nil {
				report.addWarning("customer %s: failed to update Stripe ID: %v", email, err)
			}
		}
		return existing.ID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("lookup customer %s: %w", email, err)
	}

	// Create new customer
	phone := sub.Billing.Phone
	customer, err := cs.Create(ctx, tx, store.CreateCustomerParams{
		Email:     email,
		FirstName: sub.Billing.FirstName,
		LastName:  sub.Billing.LastName,
		Phone:     strPtr(phone),
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("create customer %s: %w", email, err)
	}

	// Set Stripe customer ID
	if stripeID != "" {
		_, err := cs.UpdateStripeCustomerID(ctx, tx, customer.ID, stripeID)
		if err != nil {
			report.addWarning("customer %s: failed to set Stripe ID: %v", email, err)
		}
	}

	cache[email] = customer.ID
	report.CustomersCreated++
	return customer.ID, nil
}

func createShippingAddress(
	ctx context.Context,
	tx pgx.Tx,
	cs *store.CustomerStore,
	addr wcAddress,
	customerID uuid.UUID,
	report *migrationReport,
) (uuid.UUID, error) {
	address, err := cs.CreateAddress(ctx, tx, store.CreateAddressParams{
		CustomerID:  &customerID,
		FirstName:   addr.FirstName,
		LastName:    addr.LastName,
		Company:     strPtr(addr.Company),
		Line1:       addr.Address1,
		Line2:       strPtr(addr.Address2),
		City:        addr.City,
		State:       addr.State,
		PostalCode:  addr.Postcode,
		CountryCode: addr.Country,
		IsDefault:   true,
	})
	if err != nil {
		return uuid.Nil, err
	}
	report.AddressesCreated++
	return address.ID, nil
}

func printReport(report *migrationReport, logger *slog.Logger) {
	fmt.Println()
	fmt.Println("=== Migration Report ===")
	fmt.Printf("Customers created:      %d\n", report.CustomersCreated)
	fmt.Printf("Customers found:        %d\n", report.CustomersFound)
	fmt.Printf("Addresses created:      %d\n", report.AddressesCreated)
	fmt.Printf("Subscriptions created:  %d\n", report.SubscriptionsCreated)
	fmt.Printf("Subscriptions skipped:  %d\n", report.SubscriptionsSkipped)

	if len(report.Warnings) > 0 {
		fmt.Printf("\nWarnings (%d):\n", len(report.Warnings))
		for _, w := range report.Warnings {
			fmt.Printf("  WARN: %s\n", w)
		}
	}

	if len(report.Errors) > 0 {
		fmt.Printf("\nErrors (%d):\n", len(report.Errors))
		for _, e := range report.Errors {
			fmt.Printf("  ERROR: %s\n", e)
		}
	}

	if len(report.Errors) == 0 {
		fmt.Println("\nMigration completed successfully.")
	} else {
		fmt.Printf("\nMigration completed with %d errors.\n", len(report.Errors))
	}
}
