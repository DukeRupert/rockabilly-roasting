// Geocode cache warmer — resolves every address that has appeared on a
// local-delivery order and caches the coordinates, so the first real route plan
// is a cache read instead of a burst of billable Google lookups.
//
// The second job it does is arguably the more useful one: it prints the
// addresses Google could not pin precisely. Those are the stops that would send
// a driver to the wrong building, and finding them at a desk beats finding them
// at a curb.
//
// Safe to re-run — already-cached addresses cost nothing.
//
// Usage:
//
//	go run ./cmd/geocode-warm --dry-run     # show what would be looked up
//	go run ./cmd/geocode-warm               # warm the cache
//	go run ./cmd/geocode-warm --limit 50    # cap the working set
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/geocode"
	"github.com/dukerupert/hiri/internal/store"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "list the addresses that would be geocoded, then exit without calling the provider")
	limit := flag.Int("limit", 1000, "maximum number of distinct addresses to process")
	flag.Parse()

	_ = godotenv.Load()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is empty — set it in .env")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	geocodeStore := store.NewGeocodeStore(nil)

	// Dry run stops before any provider call, so it works without a key.
	if *dryRun {
		if err := reportDryRun(ctx, pool, geocodeStore, *limit); err != nil {
			log.Fatalf("dry run: %v", err)
		}
		return
	}

	apiKey := os.Getenv("GOOGLE_GEOCODING_API_KEY")
	if apiKey == "" {
		log.Fatal("GOOGLE_GEOCODING_API_KEY is empty — set it in .env, or pass --dry-run to see the working set without geocoding")
	}
	svc := app.NewGeocodingService(geocodeStore, geocode.NewGoogleGeocoder(apiKey))

	before, err := countCached(ctx, pool, geocodeStore)
	if err != nil {
		log.Fatalf("count cache: %v", err)
	}

	res, err := svc.WarmCache(ctx, pool, *limit)
	if err != nil {
		log.Fatalf("warm cache: %v", err)
	}

	after, err := countCached(ctx, pool, geocodeStore)
	if err != nil {
		log.Fatalf("count cache: %v", err)
	}

	fmt.Printf("\nGeocode cache warmed\n")
	fmt.Printf("  addresses processed : %d\n", len(res.Resolved)+len(res.Failed))
	fmt.Printf("  already cached      : %d\n", res.CacheHits)
	fmt.Printf("  new lookups (billed): %d\n", res.Lookups)
	fmt.Printf("  failed              : %d\n", len(res.Failed))
	fmt.Printf("  cache size          : %d → %d\n", before, after)

	if low := res.LowConfidence(); len(low) > 0 {
		fmt.Printf("\n%d address(es) could not be pinned precisely — review before routing:\n", len(low))
		sort.Slice(low, func(i, j int) bool { return low[i].RawAddress < low[j].RawAddress })
		for _, g := range low {
			fmt.Printf("  [%s] %s  (%.5f, %.5f)\n", g.Confidence, g.RawAddress, g.Lat, g.Lng)
		}
	}

	if len(res.Failed) > 0 {
		fmt.Printf("\n%d address(es) could not be geocoded at all:\n", len(res.Failed))
		addrs := make([]string, 0, len(res.Failed))
		for a := range res.Failed {
			addrs = append(addrs, a)
		}
		sort.Strings(addrs)
		for _, a := range addrs {
			fmt.Printf("  %s\n    → %v\n", a, res.Failed[a])
		}
		// Non-zero exit: a failed address is a stop that cannot be routed, and
		// a human needs to look at it.
		os.Exit(1)
	}
}

func reportDryRun(ctx context.Context, pool *pgxpool.Pool, geocodeStore *store.GeocodeStore, limit int) error {
	var addresses []domain.Address
	var cached map[string]domain.GeocodedAddress
	err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		var txErr error
		addresses, txErr = geocodeStore.ListLocalDeliveryAddresses(ctx, tx, limit)
		if txErr != nil {
			return txErr
		}
		keys := make([]string, 0, len(addresses))
		for _, a := range addresses {
			keys = append(keys, domain.NormalizeAddress(domain.FormatAddressForGeocoding(a)))
		}
		cached, txErr = geocodeStore.ListByNormalized(ctx, tx, keys)
		return txErr
	})
	if err != nil {
		return err
	}

	var hits, misses int
	fmt.Printf("Local-delivery addresses on file: %d\n\n", len(addresses))
	for _, a := range addresses {
		formatted := domain.FormatAddressForGeocoding(a)
		key := domain.NormalizeAddress(formatted)
		if _, ok := cached[key]; ok {
			hits++
			fmt.Printf("  [cached] %s\n", formatted)
			continue
		}
		misses++
		fmt.Printf("  [LOOKUP] %s\n", formatted)
	}
	fmt.Printf("\n%d already cached, %d would be looked up (billed).\n", hits, misses)
	return nil
}

func countCached(ctx context.Context, pool *pgxpool.Pool, geocodeStore *store.GeocodeStore) (int, error) {
	var n int
	err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		var txErr error
		n, txErr = geocodeStore.CountGeocodedAddresses(ctx, tx)
		return txErr
	})
	return n, err
}
