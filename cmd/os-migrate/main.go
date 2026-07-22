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
	"net/url"
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

// OrderSpace API types

type osToken struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

type osCustomer struct {
	ID              string      `json:"id"`
	CompanyName     string      `json:"company_name"`
	CreatedAt       string      `json:"created_at"`
	Status          string      `json:"status"` // new, active, closed
	Reference       string      `json:"reference"`
	InternalNote    string      `json:"internal_note"`
	Buyers          []osBuyer   `json:"buyers"`
	Phone           string      `json:"phone"`
	EmailAddresses  osEmails    `json:"email_addresses"`
	TaxNumber       string      `json:"tax_number"`
	Addresses       []osAddress `json:"addresses"`
	MinimumSpend    *float64    `json:"minimum_spend"`
	CustomerGroupID string      `json:"customer_group_id"`
	PriceListID     string      `json:"price_list_id"`
	PaymentTermsID  string      `json:"payment_terms_id"`
}

type osBuyer struct {
	Name         string `json:"name"`
	EmailAddress string `json:"email_address"`
}

type osEmails struct {
	Orders     string `json:"orders"`
	Dispatches string `json:"dispatches"`
	Invoices   string `json:"invoices"`
}

type osAddress struct {
	CompanyName string `json:"company_name"`
	ContactName string `json:"contact_name"`
	Line1       string `json:"line1"`
	Line2       string `json:"line2"`
	City        string `json:"city"`
	State       string `json:"state"`
	PostalCode  string `json:"postal_code"`
	Country     string `json:"country"`
}

type osOrder struct {
	ID               string        `json:"id"`
	Number           int           `json:"number"`
	Created          string        `json:"created"`
	Status           string        `json:"status"`
	CustomerID       string        `json:"customer_id"`
	CompanyName      string        `json:"company_name"`
	Phone            string        `json:"phone"`
	EmailAddresses   osEmails      `json:"email_addresses"`
	DeliveryDate     string        `json:"delivery_date"`
	Reference        string        `json:"reference"`
	InternalNote     string        `json:"internal_note"`
	CustomerPONumber string        `json:"customer_po_number"`
	CustomerNote     string        `json:"customer_note"`
	ShippingType     string        `json:"shipping_type"`
	ShippingAddress  *osAddress    `json:"shipping_address"`
	BillingAddress   *osAddress    `json:"billing_address"`
	Currency         string        `json:"currency"`
	NetTotal         float64       `json:"net_total"`
	GrossTotal       float64       `json:"gross_total"`
	OrderLines       []osOrderLine `json:"order_lines"`
}

type osOrderLine struct {
	ID        string  `json:"id"`
	SKU       string  `json:"sku"`
	Name      string  `json:"name"`
	Options   string  `json:"options"`
	Shipping  bool    `json:"shipping"`
	Quantity  int     `json:"quantity"`
	UnitPrice float64 `json:"unit_price"`
	SubTotal  float64 `json:"sub_total"`
	TaxName   string  `json:"tax_name"`
	TaxRate   float64 `json:"tax_rate"`
	TaxAmount float64 `json:"tax_amount"`
}

type osCustomersResponse struct {
	Customers []osCustomer `json:"customers"`
}

type osOrdersResponse struct {
	Orders []osOrder `json:"orders"`
}

// Migration report

type migrationReport struct {
	CustomersCreated int
	CustomersFound   int
	AddressesCreated int
	OrdersCreated    int
	OrdersSkipped    int
	LineItemsCreated int
	Errors           []string
	Warnings         []string
}

func (r *migrationReport) addError(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

func (r *migrationReport) addWarning(format string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

// API client

type osClient struct {
	clientID     string
	clientSecret string
	token        string
	tokenExpiry  time.Time
	httpClient   *http.Client
}

func newOSClient(clientID, clientSecret string) *osClient {
	return &osClient{
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *osClient) authenticate(ctx context.Context) error {
	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return nil
	}

	data := url.Values{
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"grant_type":    {"client_credentials"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://identity.orderspace.com/oauth/token",
		strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("auth request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read auth response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("auth failed (%d): %s", resp.StatusCode, string(body[:min(200, len(body))]))
	}

	var tok osToken
	if err := json.Unmarshal(body, &tok); err != nil {
		return fmt.Errorf("decode token: %w", err)
	}

	c.token = tok.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(tok.ExpiresIn-60) * time.Second) // refresh 1 min early
	return nil
}

func (c *osClient) get(ctx context.Context, path string) ([]byte, error) {
	if err := c.authenticate(ctx); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.orderspace.com/v1"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		// Rate limited — wait and retry once
		time.Sleep(2 * time.Second)
		return c.get(ctx, path)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s returned %d: %s", path, resp.StatusCode, string(body[:min(200, len(body))]))
	}

	return body, nil
}

func (c *osClient) fetchAllCustomers(ctx context.Context) ([]osCustomer, error) {
	var all []osCustomer
	startingAfter := ""

	for {
		path := "/customers?limit=200"
		if startingAfter != "" {
			path += "&starting_after=" + startingAfter
		}

		body, err := c.get(ctx, path)
		if err != nil {
			return nil, err
		}

		var resp osCustomersResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("decode customers: %w", err)
		}

		if len(resp.Customers) == 0 {
			break
		}

		all = append(all, resp.Customers...)
		if len(resp.Customers) < 200 {
			break
		}
		startingAfter = resp.Customers[len(resp.Customers)-1].ID
	}
	return all, nil
}

func (c *osClient) fetchAllOrders(ctx context.Context) ([]osOrder, error) {
	var all []osOrder
	startingAfter := ""

	for {
		path := "/orders?limit=200"
		if startingAfter != "" {
			path += "&starting_after=" + startingAfter
		}

		body, err := c.get(ctx, path)
		if err != nil {
			return nil, err
		}

		var resp osOrdersResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("decode orders: %w", err)
		}

		if len(resp.Orders) == 0 {
			break
		}

		all = append(all, resp.Orders...)
		if len(resp.Orders) < 200 {
			break
		}
		startingAfter = resp.Orders[len(resp.Orders)-1].ID
	}
	return all, nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	dryRun := flag.Bool("dry-run", false, "Validate and report without importing")
	customersOnly := flag.Bool("customers-only", false, "Import only customers, skip orders")
	only := flag.String("only", "", "comma-separated OrderSpace customer IDs to import (default: all)")
	flag.Parse()

	_ = godotenv.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	clientID := os.Getenv("ORDERSPACE_CLIENT_ID")
	clientSecret := os.Getenv("ORDERSPACE_CLIENT_SECRET")
	dbURL := os.Getenv("DATABASE_URL")

	if clientID == "" || clientSecret == "" {
		return fmt.Errorf("ORDERSPACE_CLIENT_ID and ORDERSPACE_CLIENT_SECRET are required")
	}
	if dbURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	ctx := context.Background()
	client := newOSClient(clientID, clientSecret)

	// Fetch customers
	logger.Info("fetching OrderSpace customers...")
	customers, err := client.fetchAllCustomers(ctx)
	if err != nil {
		return fmt.Errorf("fetch customers: %w", err)
	}
	logger.Info("fetched customers", "count", len(customers))

	// Fetch orders
	var orders []osOrder
	if !*customersOnly {
		logger.Info("fetching OrderSpace orders...")
		orders, err = client.fetchAllOrders(ctx)
		if err != nil {
			return fmt.Errorf("fetch orders: %w", err)
		}
		logger.Info("fetched orders", "count", len(orders))
	}

	// Restrict to a specific batch of customers if --only was given.
	if *only != "" {
		want := make(map[string]bool)
		for _, id := range strings.Split(*only, ",") {
			if id = strings.TrimSpace(id); id != "" {
				want[id] = true
			}
		}
		var keptC []osCustomer
		for _, c := range customers {
			if want[c.ID] {
				keptC = append(keptC, c)
			}
		}
		customers = keptC
		logger.Info("restricted to --only batch", "count", len(customers))
	}

	// Only migrate successful order history (skip cancelled/incomplete) and only
	// orders belonging to the selected customers.
	if len(orders) > 0 {
		selected := make(map[string]bool, len(customers))
		for _, c := range customers {
			selected[c.ID] = true
		}
		var keptO []osOrder
		var droppedStatus, droppedCustomer int
		for _, o := range orders {
			if !selected[o.CustomerID] {
				droppedCustomer++
				continue
			}
			if !isSuccessfulOrder(o.Status) {
				droppedStatus++
				continue
			}
			keptO = append(keptO, o)
		}
		orders = keptO
		logger.Info("filtered orders", "kept", len(orders),
			"dropped_unsuccessful", droppedStatus, "dropped_other_customer", droppedCustomer)
	}

	// Connect to database
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	report := &migrationReport{}

	if *dryRun {
		logger.Info("=== DRY RUN MODE ===")
		dryRunValidation(customers, orders, report, logger)
	} else {
		// Each migrated customer gets the Hiri price list matching the OS list they
		// were on (see hiriPriceListName): OS 2025 → Wholesale 2025, OS 2026 →
		// Wholesale 2026 (kept — NOT grandfathered down to 2025), OS Tailwinds →
		// Tailwinds, legacy OS 2024 / no list → the Wholesale 2025 floor. NET 7 for
		// everyone. Resolve only the Hiri lists this batch actually needs, by exact
		// name, and fail fast before writing anything if any is missing (e.g. the
		// Tailwinds list must be seeded before a Tailwinds customer can import).
		needed := map[string]bool{}
		for _, c := range customers {
			needed[hiriPriceListName(c)] = true
		}
		priceListIDs := map[string]uuid.UUID{}
		for name := range needed {
			var id uuid.UUID
			if err := pool.QueryRow(ctx,
				`SELECT id FROM price_lists WHERE name = $1`, name).Scan(&id); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("price list %q (required by this batch) does not exist in Hiri — create it before importing", name)
				}
				return fmt.Errorf("look up %q price list: %w", name, err)
			}
			priceListIDs[name] = id
		}

		importData(ctx, pool, customers, orders, *customersOnly, priceListIDs, report, logger)
	}

	printReport(report, logger)
	return nil
}

func dryRunValidation(customers []osCustomer, orders []osOrder, report *migrationReport, logger *slog.Logger) {
	// Analyze customers
	statusCounts := make(map[string]int)
	noBuyers := 0
	noAddresses := 0

	for _, c := range customers {
		statusCounts[c.Status]++

		if len(c.Buyers) == 0 {
			noBuyers++
			report.addWarning("customer %s (%s): no buyers (contacts)", c.ID, c.CompanyName)
		}

		if len(c.Addresses) == 0 {
			noAddresses++
			report.addWarning("customer %s (%s): no addresses", c.ID, c.CompanyName)
		}

		// Check for email
		email := primaryEmail(c)
		if email == "" {
			report.addError("customer %s (%s): no email address found", c.ID, c.CompanyName)
		}
	}

	logger.Info("customer status breakdown")
	for status, count := range statusCounts {
		logger.Info("  status", "status", status, "count", count)
	}
	logger.Info("customers without buyers", "count", noBuyers)
	logger.Info("customers without addresses", "count", noAddresses)

	// Analyze orders
	if len(orders) > 0 {
		orderStatusCounts := make(map[string]int)
		customerOrderCounts := make(map[string]int)

		for _, o := range orders {
			orderStatusCounts[o.Status]++
			customerOrderCounts[o.CustomerID]++
		}

		logger.Info("order status breakdown")
		for status, count := range orderStatusCounts {
			logger.Info("  status", "status", status, "count", count)
		}
		logger.Info("customers with orders", "count", len(customerOrderCounts))

		// Check for orders with unknown customers
		customerIDs := make(map[string]bool)
		for _, c := range customers {
			customerIDs[c.ID] = true
		}
		for _, o := range orders {
			if !customerIDs[o.CustomerID] {
				report.addWarning("order %s (#%d): customer %s not in customer list", o.ID, o.Number, o.CustomerID)
			}
		}
	}
}

// OrderSpace price list IDs (stable; update if OS ever recreates a list).
const (
	osPriceList2024      = "pl_v1xq3yj0" // OS "2024 Wholesale Price" (legacy)
	osPriceList2025      = "pl_q1m82yl5" // OS "2025"
	osPriceList2026      = "pl_yjg926l9" // OS "2026"
	osTailwindsPriceList = "pl_v1x78pl0" // OS "Tailwinds" (concessions markup)
)

// hiriPriceListName returns the Hiri price list a migrated OS customer should
// receive. Rules, in order:
//  1. Special OS lists always win: Tailwinds → Tailwinds, legacy 2024 → the
//     dedicated Wholesale 2024 list (preserves those customers' exact pricing;
//     e.g. MOCHA stays on 2024, and moving it up to 2026 is a later business call).
//  2. Data-hygiene fallback: a customer with NO OS group gets current (2026)
//     pricing regardless of the (often stale) list on their record.
//  3. Otherwise keep the tier they were on: OS 2026 → 2026, OS 2025 → 2025.
//  4. Default (has group, no/unknown OS list): the Wholesale 2025 floor.
func hiriPriceListName(osCust osCustomer) string {
	switch osCust.PriceListID {
	case osTailwindsPriceList:
		return "Tailwinds"
	case osPriceList2024:
		return "Wholesale 2024"
	}
	if osCust.CustomerGroupID == "" {
		return "Wholesale 2026"
	}
	switch osCust.PriceListID {
	case osPriceList2026:
		return "Wholesale 2026"
	case osPriceList2025:
		return "Wholesale 2025"
	}
	return "Wholesale 2025"
}

func importData(
	ctx context.Context,
	pool *pgxpool.Pool,
	customers []osCustomer,
	orders []osOrder,
	customersOnly bool,
	priceListIDs map[string]uuid.UUID,
	report *migrationReport,
	logger *slog.Logger,
) {
	customerStore := store.NewCustomerStore()
	orderStore := store.NewOrderStore(nil)

	// Map OrderSpace customer ID → Hiri customer ID
	osToHiri := make(map[string]uuid.UUID)

	// Import customers
	for _, osCust := range customers {
		email := primaryEmail(osCust)
		if email == "" {
			report.addError("customer %s (%s): no email, skipping", osCust.ID, osCust.CompanyName)
			continue
		}

		// All names returned by hiriPriceListName were resolved up front, so this
		// lookup is always present.
		priceListID := priceListIDs[hiriPriceListName(osCust)]

		err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
			// Check if customer already exists by email
			existing, err := customerStore.GetByEmail(ctx, tx, email)
			if err == nil {
				// Upgrade the existing account to wholesale rather than leaving it
				// retail (otherwise its OS orders attach but it can't be priced).
				if err := setWholesaleFields(ctx, tx, existing.ID, mapOSStatus(osCust.Status), osCust.CompanyName, priceListID); err != nil {
					return fmt.Errorf("upgrade existing customer %s: %w", email, err)
				}
				osToHiri[osCust.ID] = existing.ID
				report.CustomersFound++
				logger.Info("customer exists — upgraded to wholesale", "email", email, "os_id", osCust.ID)
				return nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("lookup customer %s: %w", email, err)
			}

			// Parse name from first buyer
			firstName, lastName := parseBuyerName(osCust)

			customer, err := customerStore.Create(ctx, tx, store.CreateCustomerParams{
				Email:     email,
				FirstName: firstName,
				LastName:  lastName,
				Phone:     strPtr(osCust.Phone),
			})
			if err != nil {
				return fmt.Errorf("create customer %s: %w", email, err)
			}

			// Assign wholesale account type + status, resolved price list, NET 7.
			if err := setWholesaleFields(ctx, tx, customer.ID, mapOSStatus(osCust.Status), osCust.CompanyName, priceListID); err != nil {
				return fmt.Errorf("set wholesale fields: %w", err)
			}

			if osCust.TaxNumber != "" {
				if err := customerStore.UpdateTaxExempt(ctx, tx, customer.ID, true, &osCust.TaxNumber); err != nil {
					return fmt.Errorf("set tax exempt: %w", err)
				}
			}

			// Create addresses, deduplicating any repeats (OrderSpace data has many).
			seen := make(map[string]bool)
			for _, addr := range osCust.Addresses {
				if addr.Line1 == "" {
					continue
				}
				key := addrKey(addr.Line1, addr.PostalCode)
				if seen[key] {
					continue
				}
				contactFirst, contactLast := splitName(addr.ContactName)
				_, err := customerStore.CreateAddress(ctx, tx, store.CreateAddressParams{
					CustomerID:  &customer.ID,
					FirstName:   contactFirst,
					LastName:    contactLast,
					Company:     strPtr(addr.CompanyName),
					Line1:       addr.Line1,
					Line2:       strPtr(addr.Line2),
					City:        addr.City,
					State:       addr.State,
					PostalCode:  addr.PostalCode,
					CountryCode: mapCountryCode(addr.Country),
					IsDefault:   len(seen) == 0,
				})
				if err != nil {
					return fmt.Errorf("create address: %w", err)
				}
				seen[key] = true
				report.AddressesCreated++
			}

			osToHiri[osCust.ID] = customer.ID
			report.CustomersCreated++
			logger.Info("imported customer",
				"email", email,
				"company", osCust.CompanyName,
				"os_id", osCust.ID,
				"status", mapOSStatus(osCust.Status),
			)
			return nil
		})
		if err != nil {
			report.addError("customer %s (%s): %v", osCust.ID, osCust.CompanyName, err)
		}
	}

	if customersOnly {
		return
	}

	// Import orders
	for _, osOrd := range orders {
		hiriCustomerID, ok := osToHiri[osOrd.CustomerID]
		if !ok {
			report.addError("order %s (#%d): customer %s not imported, skipping", osOrd.ID, osOrd.Number, osOrd.CustomerID)
			report.OrdersSkipped++
			continue
		}

		err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
			// Get customer's default address for shipping/billing
			addresses, err := customerStore.ListAddresses(ctx, tx, hiriCustomerID)
			if err != nil || len(addresses) == 0 {
				return fmt.Errorf("no addresses for customer: %w", err)
			}
			defaultAddr := addresses[0]

			// Resolve the order's shipping address. Reuse an existing matching
			// address for this customer instead of creating a duplicate per order
			// (OrderSpace repeats the same address on every order — don't migrate
			// that bloat). Only create when it's genuinely new.
			shippingAddrID := defaultAddr.ID
			if osOrd.ShippingAddress != nil && osOrd.ShippingAddress.Line1 != "" {
				key := addrKey(osOrd.ShippingAddress.Line1, osOrd.ShippingAddress.PostalCode)
				matched := uuid.Nil
				for _, a := range addresses {
					if addrKey(a.Line1, a.PostalCode) == key {
						matched = a.ID
						break
					}
				}
				if matched != uuid.Nil {
					shippingAddrID = matched
				} else {
					contactFirst, contactLast := splitName(osOrd.ShippingAddress.ContactName)
					addr, err := customerStore.CreateAddress(ctx, tx, store.CreateAddressParams{
						CustomerID:  &hiriCustomerID,
						FirstName:   contactFirst,
						LastName:    contactLast,
						Company:     strPtr(osOrd.ShippingAddress.CompanyName),
						Line1:       osOrd.ShippingAddress.Line1,
						Line2:       strPtr(osOrd.ShippingAddress.Line2),
						City:        osOrd.ShippingAddress.City,
						State:       osOrd.ShippingAddress.State,
						PostalCode:  osOrd.ShippingAddress.PostalCode,
						CountryCode: mapCountryCode(osOrd.ShippingAddress.Country),
						IsDefault:   false,
					})
					if err != nil {
						return fmt.Errorf("create shipping address: %w", err)
					}
					shippingAddrID = addr.ID
					report.AddressesCreated++
				}
			}

			// Calculate totals in cents
			subtotalCents := dollarsTocents(osOrd.NetTotal)
			totalCents := dollarsTocents(osOrd.GrossTotal)
			taxCents := totalCents - subtotalCents
			var shippingCents int

			// Extract shipping from line items
			var productLines []osOrderLine
			for _, li := range osOrd.OrderLines {
				if li.Shipping {
					shippingCents += dollarsTocents(li.SubTotal)
				} else {
					productLines = append(productLines, li)
				}
			}

			placedAt := parseISO(osOrd.Created)
			if placedAt.IsZero() {
				placedAt = time.Now()
			}

			orderNumber := fmt.Sprintf("OS-%d", osOrd.Number)
			var poNumber *string
			if osOrd.CustomerPONumber != "" {
				poNumber = &osOrd.CustomerPONumber
			}

			var deliveryDate *time.Time
			if osOrd.DeliveryDate != "" {
				t := parseISO(osOrd.DeliveryDate)
				if !t.IsZero() {
					deliveryDate = &t
				}
			}

			var internalNote *string
			if osOrd.InternalNote != "" {
				internalNote = &osOrd.InternalNote
			}

			shippingMethod := domain.ShippingMethodShipped

			order, err := orderStore.CreateOrder(ctx, tx, store.CreateOrderParams{
				Number:                orderNumber,
				CustomerID:            &hiriCustomerID,
				Channel:               domain.OrderChannelWholesale,
				Status:                mapOSOrderStatus(osOrd.Status),
				PaymentStatus:         domain.PaymentStatusCaptured,
				FulfillmentStatus:     mapOSFulfillmentStatus(osOrd.Status),
				CurrencyCode:          strings.ToUpper(osOrd.Currency),
				Subtotal:              subtotalCents,
				ShippingTotal:         shippingCents,
				TaxTotal:              taxCents,
				Total:                 totalCents,
				ShippingAddressID:     shippingAddrID,
				BillingAddressID:      defaultAddr.ID,
				ShippingMethod:        &shippingMethod,
				RequestedDeliveryDate: deliveryDate,
				Notes:                 internalNote,
				Metadata: map[string]any{
					"orderspace_id":        osOrd.ID,
					"orderspace_number":    osOrd.Number,
					"orderspace_reference": osOrd.Reference,
					"imported_from":        "orderspace",
				},
				PlacedAt: placedAt,
			})
			if err != nil {
				return fmt.Errorf("create order: %w", err)
			}

			// Set customer PO number if present
			if poNumber != nil {
				_, _ = tx.Exec(ctx, `UPDATE orders SET customer_po_number = $1 WHERE id = $2`, *poNumber, order.ID)
			}

			// Create line items — translate the OrderSpace SKU to its Hiri
			// equivalent, then match by SKU. Unmappable lines are skipped; the
			// original OrderSpace SKU/name stay in the line metadata below.
			for _, li := range productLines {
				variantID := uuid.Nil
				if hiriSKU, ok := translateSKU(li.SKU); ok {
					err := tx.QueryRow(ctx, `SELECT id FROM variants WHERE sku = $1`, hiriSKU).Scan(&variantID)
					if err != nil {
						report.addWarning("order %s line %s: %q -> %q not in catalog", osOrd.ID, li.ID, li.SKU, hiriSKU)
					}
				} else if li.SKU != "" {
					report.addWarning("order %s line %s: no SKU mapping for %q (%s)", osOrd.ID, li.ID, li.SKU, li.Name)
				}

				if variantID == uuid.Nil {
					// Can't create line item without a variant — store in metadata
					report.addWarning("order %s line %s: no variant for %q (SKU: %s), skipped line item", osOrd.ID, li.ID, li.Name, li.SKU)
					continue
				}

				lineTaxCents := dollarsTocents(li.TaxAmount)

				_, err := orderStore.CreateLineItem(ctx, tx, store.CreateLineItemParams{
					OrderID:   order.ID,
					VariantID: variantID,
					Quantity:  li.Quantity,
					UnitPrice: dollarsTocents(li.UnitPrice),
					Subtotal:  dollarsTocents(li.SubTotal),
					TaxTotal:  lineTaxCents,
					Total:     dollarsTocents(li.SubTotal) + lineTaxCents,
					Metadata: map[string]any{
						"orderspace_line_id": li.ID,
						"orderspace_sku":     li.SKU,
						"orderspace_name":    li.Name,
						"orderspace_options": li.Options,
					},
				})
				if err != nil {
					return fmt.Errorf("create line item %s: %w", li.ID, err)
				}
				report.LineItemsCreated++
			}

			report.OrdersCreated++
			logger.Info("imported order",
				"os_number", osOrd.Number,
				"hiri_number", orderNumber,
				"status", osOrd.Status,
				"total", osOrd.GrossTotal,
			)
			return nil
		})
		if err != nil {
			report.addError("order %s (#%d): %v", osOrd.ID, osOrd.Number, err)
			report.OrdersSkipped++
		}
	}
}

// Helper functions

func primaryEmail(c osCustomer) string {
	// Prefer first buyer email, fall back to orders email
	for _, b := range c.Buyers {
		if b.EmailAddress != "" {
			return strings.ToLower(strings.TrimSpace(b.EmailAddress))
		}
	}
	if c.EmailAddresses.Orders != "" {
		return strings.ToLower(strings.TrimSpace(c.EmailAddresses.Orders))
	}
	return ""
}

func parseBuyerName(c osCustomer) (string, string) {
	if len(c.Buyers) > 0 && c.Buyers[0].Name != "" {
		return splitName(c.Buyers[0].Name)
	}
	// Fall back to company name as last name
	return "", c.CompanyName
}

func splitName(full string) (string, string) {
	full = strings.TrimSpace(full)
	if full == "" {
		return "", ""
	}
	parts := strings.SplitN(full, " ", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

func mapOSStatus(status string) domain.WholesaleStatus {
	switch status {
	case "active":
		return domain.WholesaleStatusApproved
	case "new":
		return domain.WholesaleStatusPending
	case "closed":
		return domain.WholesaleStatusSuspended
	default:
		return domain.WholesaleStatusPending
	}
}

// setWholesaleFields marks a customer as wholesale and assigns the migration's
// standard pricing: the given price list (matched to the customer's OS list via
// hiriPriceListName) and NET 7 terms.
// Existing company_name is preserved; for a freshly created customer (null
// company_name) the OrderSpace company name is used.
func setWholesaleFields(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.WholesaleStatus, company string, priceListID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE customers SET
			account_type = $2,
			wholesale_status = $3,
			company_name = COALESCE(company_name, NULLIF($4, '')),
			price_list_id = $5,
			payment_terms_days = 7,
			approved_at = CASE WHEN $3 = 'approved' THEN COALESCE(approved_at, NOW()) ELSE approved_at END
		WHERE id = $1`,
		id, string(domain.AccountTypeWholesale), string(status), company, priceListID,
	)
	return err
}

func mapOSOrderStatus(status string) domain.OrderStatus {
	switch status {
	case "new", "preorder":
		return domain.OrderStatusPending
	case "invoiced", "released":
		return domain.OrderStatusConfirmed
	case "part_fulfilled":
		return domain.OrderStatusProcessing
	case "fulfilled":
		return domain.OrderStatusComplete
	case "cancelled":
		return domain.OrderStatusCancelled
	default:
		return domain.OrderStatusComplete
	}
}

// mapOSFulfillmentStatus picks the Hiri fulfillment status for an imported
// order. Historical completed orders land terminal (delivered) — anything
// short of shipped/delivered sits in the admin "needs action" queue, and a
// migrated back-catalog must not show up as work to do. Only orders that were
// genuinely still open in OrderSpace at cutover (invoiced/released/
// part_fulfilled) stay actionable, in the wholesale queue.
func mapOSFulfillmentStatus(status string) domain.FulfillmentStatus {
	switch status {
	case "new", "invoiced", "released", "preorder":
		return domain.FulfillmentStatusUnfulfilled
	case "part_fulfilled":
		return domain.FulfillmentStatusPartiallyFulfilled
	case "fulfilled":
		return domain.FulfillmentStatusDelivered
	default:
		return domain.FulfillmentStatusDelivered
	}
}

func mapCountryCode(country string) string {
	// OrderSpace may send full names or codes
	if len(country) == 2 {
		return strings.ToUpper(country)
	}
	// Common mappings
	switch strings.ToLower(country) {
	case "united states", "usa", "us":
		return "US"
	case "canada":
		return "CA"
	case "united kingdom", "uk", "gb":
		return "GB"
	default:
		return country
	}
}

func dollarsTocents(dollars float64) int {
	return int(dollars*100 + 0.5) // round to nearest cent
}

func parseISO(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02",
	} {
		t, err := time.Parse(layout, s)
		if err == nil {
			return t
		}
	}
	return time.Time{}
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func printReport(report *migrationReport, logger *slog.Logger) {
	fmt.Println()
	fmt.Println("=== OrderSpace Migration Report ===")
	fmt.Printf("Customers created:  %d\n", report.CustomersCreated)
	fmt.Printf("Customers found:    %d\n", report.CustomersFound)
	fmt.Printf("Addresses created:  %d\n", report.AddressesCreated)
	fmt.Printf("Orders created:     %d\n", report.OrdersCreated)
	fmt.Printf("Orders skipped:     %d\n", report.OrdersSkipped)
	fmt.Printf("Line items created: %d\n", report.LineItemsCreated)

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
