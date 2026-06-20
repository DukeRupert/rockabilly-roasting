// os-report is a READ-ONLY census of an Orderspace tenant.
// It fetches reference data (customer groups, price lists, payment terms,
// products, customers) and prints aggregate counts plus one sample record
// per resource so we can size the Hiri importer changes precisely.
//
// No writes. No state. Safe to run repeatedly.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	tokenURL = "https://identity.orderspace.com/oauth/token"
	apiBase  = "https://api.orderspace.com/v1"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	_ = godotenv.Load()
	clientID := os.Getenv("ORDERSPACE_CLIENT_ID")
	clientSecret := os.Getenv("ORDERSPACE_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		return fmt.Errorf("ORDERSPACE_CLIENT_ID and ORDERSPACE_CLIENT_SECRET must be set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	c := &client{id: clientID, secret: clientSecret, http: &http.Client{Timeout: 30 * time.Second}}
	if err := c.authenticate(ctx); err != nil {
		return fmt.Errorf("authenticate: %w", err)
	}

	fmt.Println("=== Orderspace Census ===")
	fmt.Println()

	// Customer groups
	groups, err := fetchAll[map[string]any](ctx, c, "/customer_groups", "customer_groups")
	if err != nil {
		fmt.Printf("[customer_groups] FAILED: %v\n\n", err)
	} else {
		fmt.Printf("Customer groups: %d\n", len(groups))
		for _, g := range groups {
			fmt.Printf("  - id=%v name=%q\n", g["id"], g["name"])
		}
		printSample("customer_group", groups)
		fmt.Println()
	}

	// Payment terms
	terms, err := fetchAll[map[string]any](ctx, c, "/payment_terms", "payment_terms")
	if err != nil {
		fmt.Printf("[payment_terms] FAILED: %v\n\n", err)
	} else {
		fmt.Printf("Payment terms: %d\n", len(terms))
		for _, t := range terms {
			fmt.Printf("  - id=%v name=%q days_due=%v deposit_pct=%v deposit_days=%v\n",
				t["id"], t["name"], t["days_due"], t["deposit_percentage"], t["deposit_days_due"])
		}
		printSample("payment_terms", terms)
		fmt.Println()
	}

	// Price lists
	lists, err := fetchAll[map[string]any](ctx, c, "/price_lists", "price_lists")
	if err != nil {
		fmt.Printf("[price_lists] FAILED: %v\n\n", err)
	} else {
		fmt.Printf("Price lists: %d\n", len(lists))
		for _, l := range lists {
			withTiers := countTiers(l)
			fmt.Printf("  - id=%v name=%q currency=%v items=%d items_with_tiers=%d\n",
				l["id"], l["name"], l["currency"], countItems(l), withTiers)
		}
		printSample("price_list", lists)
		fmt.Println()
	}

	// Categories — may be the gate for white-label
	categories, err := fetchAll[map[string]any](ctx, c, "/categories", "categories")
	if err != nil {
		fmt.Printf("[categories] FAILED: %v\n\n", err)
	} else {
		fmt.Printf("Categories: %d\n", len(categories))
		for _, ca := range categories {
			fmt.Printf("  - id=%v name=%q keys=%v\n", ca["id"], ca["name"], topKeys(ca))
		}
		printSample("category", categories)
		fmt.Println()
	}

	// Products + tier/visibility deep dive
	products, err := fetchAll[map[string]any](ctx, c, "/products", "products")
	if err != nil {
		fmt.Printf("[products] FAILED: %v\n\n", err)
	} else {
		total := len(products)
		// Enumerate ALL distinct top-level + variant-level keys so we can spot
		// any visibility / restriction fields we haven't accounted for.
		topKS := stringSet{}
		varKS := stringSet{}
		// Per-price-list override count: how many variant-level rows reference each list
		listOverrides := map[string]int{}
		listOverrideTiers := map[string]int{}
		for _, p := range products {
			for k := range p {
				topKS[k] = struct{}{}
			}
			variants, _ := p["product_variants"].([]any)
			for _, v := range variants {
				vm, _ := v.(map[string]any)
				for k := range vm {
					varKS[k] = struct{}{}
				}
				prices, _ := vm["price_list_prices"].([]any)
				for _, pr := range prices {
					prm, _ := pr.(map[string]any)
					id, _ := prm["price_list_id"].(string)
					listOverrides[id]++
					// tier breaks could be under "breaks", "tiers", "quantity_breaks"
					for _, tk := range []string{"breaks", "tiers", "quantity_breaks", "price_breaks"} {
						if t, ok := prm[tk].([]any); ok && len(t) > 0 {
							listOverrideTiers[id]++
							break
						}
					}
				}
			}
		}
		fmt.Printf("Products: %d\n", total)
		fmt.Printf("  Distinct top-level keys: %v\n", sortedKeys(topKS))
		fmt.Printf("  Distinct variant-level keys: %v\n", sortedKeys(varKS))
		fmt.Println("  Variant-level price_list overrides per price list:")
		for id, n := range listOverrides {
			tiers := listOverrideTiers[id]
			fmt.Printf("    %4d overrides (%d with tiers) -> %s\n", n, tiers, id)
		}
		// dump first product fully — for visibility-field discovery
		if len(products) > 0 {
			b, _ := json.MarshalIndent(products[0], "    ", "  ")
			fmt.Printf("  Full first product:\n    %s\n", b)
		}
		// dump a variant that DOES have price_list_prices, if any
		for _, p := range products {
			variants, _ := p["product_variants"].([]any)
			for _, v := range variants {
				vm, _ := v.(map[string]any)
				if pls, ok := vm["price_list_prices"].([]any); ok && len(pls) > 0 {
					b, _ := json.MarshalIndent(vm, "    ", "  ")
					fmt.Printf("  Sample variant WITH price_list_prices:\n    %s\n", b)
					goto donedump
				}
			}
		}
	donedump:
		fmt.Println()
	}

	// Customers — aggregate assignments
	customers, err := fetchAll[map[string]any](ctx, c, "/customers", "customers")
	if err != nil {
		fmt.Printf("[customers] FAILED: %v\n\n", err)
		return nil
	}
	fmt.Printf("Customers: %d\n", len(customers))

	byGroup := map[string]int{}
	byList := map[string]int{}
	byTerms := map[string]int{}
	directList := 0
	for _, cu := range customers {
		g, _ := cu["customer_group_id"].(string)
		pl, _ := cu["price_list_id"].(string)
		pt, _ := cu["payment_terms_id"].(string)
		byGroup[g]++
		byList[pl]++
		byTerms[pt]++
		if pl != "" {
			directList++
		}
	}
	fmt.Printf("  Customers with a price_list_id assigned: %d\n", directList)

	// --- Data hygiene: customers needing fixes before migration ---
	fmt.Println()
	fmt.Println("=== Cleanup needed (fix in OS before migration) ===")
	listNames := idLookup(lists)
	groupNames := idLookup(groups)
	termNames := idLookup(terms)
	for _, cu := range customers {
		var issues []string
		pl, _ := cu["price_list_id"].(string)
		g, _ := cu["customer_group_id"].(string)
		pt, _ := cu["payment_terms_id"].(string)
		if pl == "" {
			issues = append(issues, "no price_list")
		} else if listNames[pl] == "2024 Wholesale Price" {
			issues = append(issues, "on legacy 2024 price list")
		}
		if g == "" {
			issues = append(issues, "no group")
		}
		if pt == "" {
			issues = append(issues, "no payment_terms")
		}
		if len(issues) == 0 {
			continue
		}
		name, _ := cu["company_name"].(string)
		id, _ := cu["id"].(string)
		emails, _ := cu["email_addresses"].(map[string]any)
		orderEmail := ""
		if emails != nil {
			orderEmail, _ = emails["orders"].(string)
		}
		fmt.Printf("  %-25s | %-30s | %s\n    issues: %s\n    list=%q group=%q terms=%q\n",
			id, truncate(name, 30), orderEmail, strings.Join(issues, ", "),
			listNames[pl], groupNames[g], termNames[pt])
	}

	fmt.Println()
	fmt.Println("  By customer_group_id:")
	printDistribution(byGroup, idLookup(groups))
	fmt.Println("  By price_list_id:")
	printDistribution(byList, idLookup(lists))
	fmt.Println("  By payment_terms_id:")
	printDistribution(byTerms, idLookup(terms))

	return nil
}

// ---- helpers ----

type client struct {
	id, secret string
	token      string
	expiry     time.Time
	http       *http.Client
}

func (c *client) authenticate(ctx context.Context) error {
	if c.token != "" && time.Now().Before(c.expiry) {
		return nil
	}
	data := url.Values{
		"client_id":     {c.id},
		"client_secret": {c.secret},
		"grant_type":    {"client_credentials"},
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("auth %d: %s", resp.StatusCode, string(body[:min(200, len(body))]))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return err
	}
	c.token = tok.AccessToken
	c.expiry = time.Now().Add(time.Duration(tok.ExpiresIn-60) * time.Second)
	return nil
}

func (c *client) get(ctx context.Context, path string) ([]byte, error) {
	if err := c.authenticate(ctx); err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+path, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusTooManyRequests {
		time.Sleep(2 * time.Second)
		return c.get(ctx, path)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s -> %d: %s", path, resp.StatusCode, string(body[:min(200, len(body))]))
	}
	return body, nil
}

func fetchAll[T any](ctx context.Context, c *client, path, key string) ([]T, error) {
	var all []T
	startingAfter := ""
	for {
		p := path + "?limit=200"
		if startingAfter != "" {
			p += "&starting_after=" + startingAfter
		}
		body, err := c.get(ctx, p)
		if err != nil {
			return nil, err
		}
		var env map[string]json.RawMessage
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("decode envelope: %w", err)
		}
		raw, ok := env[key]
		if !ok {
			return all, nil
		}
		var page []T
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("decode %s page: %w", key, err)
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		if len(page) < 200 {
			break
		}
		// last id for cursor
		var last map[string]any
		_ = json.Unmarshal(raw, &[]map[string]any{})
		// re-decode last item only as map to grab id
		var pageMaps []map[string]any
		_ = json.Unmarshal(raw, &pageMaps)
		if len(pageMaps) == 0 {
			break
		}
		last = pageMaps[len(pageMaps)-1]
		id, _ := last["id"].(string)
		if id == "" {
			break
		}
		startingAfter = id
	}
	return all, nil
}

func printSample(label string, items []map[string]any) {
	if len(items) == 0 {
		return
	}
	b, _ := json.MarshalIndent(items[0], "    ", "  ")
	fmt.Printf("  Sample %s:\n    %s\n", label, b)
}

func countItems(list map[string]any) int {
	if v, ok := list["prices"].([]any); ok {
		return len(v)
	}
	if v, ok := list["items"].([]any); ok {
		return len(v)
	}
	return 0
}

// countTiers returns the number of price-list items with quantity-break tiers.
func countTiers(list map[string]any) int {
	count := 0
	for _, k := range []string{"prices", "items"} {
		v, ok := list[k].([]any)
		if !ok {
			continue
		}
		for _, it := range v {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			for _, tk := range []string{"breaks", "tiers", "quantity_breaks"} {
				if t, ok := m[tk].([]any); ok && len(t) > 0 {
					count++
					break
				}
			}
		}
	}
	return count
}

func idLookup(items []map[string]any) map[string]string {
	m := map[string]string{}
	for _, it := range items {
		id, _ := it["id"].(string)
		name, _ := it["name"].(string)
		if id != "" {
			m[id] = name
		}
	}
	return m
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

type stringSet map[string]struct{}

func sortedKeys(s stringSet) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func topKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func printDistribution(counts map[string]int, names map[string]string) {
	type kv struct {
		k string
		v int
	}
	rows := make([]kv, 0, len(counts))
	for k, v := range counts {
		rows = append(rows, kv{k, v})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].v > rows[j].v })
	for _, r := range rows {
		label := r.k
		if label == "" {
			label = "(none)"
		} else if n, ok := names[r.k]; ok {
			label = fmt.Sprintf("%s [%s]", n, r.k)
		}
		fmt.Printf("    %4d  %s\n", r.v, label)
	}
}
