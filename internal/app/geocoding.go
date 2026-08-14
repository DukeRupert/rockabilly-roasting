package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/geocode"
	"github.com/dukerupert/hiri/internal/store"
)

// GeocodingService resolves street addresses to coordinates, cache first.
//
// Route planning needs a coordinate per stop and Google bills per lookup, so
// the cache is the whole point: the same two dozen households order week after
// week, and after the first run almost every planning click should resolve
// without a single external call.
//
// Transaction discipline: every method here takes *pgxpool.Pool rather than a
// pgx.Tx, which is this codebase's signal for "manages its own transactions"
// (see RenewalService). That is not a stylistic choice — the provider call is
// an HTTP round trip to a third party, and holding a transaction open across it
// is exactly what CLAUDE.md forbids. The sequence is: read tx → close → HTTP →
// write tx. Callers must not wrap these methods in a transaction of their own.
type GeocodingService struct {
	geocodes *store.GeocodeStore
	geocoder geocode.Geocoder
}

// NewGeocodingService creates a GeocodingService. geocoder may be nil or
// unconfigured — in that case cached addresses still resolve and misses return
// ErrGeocoderNotConfigured, which keeps local dev working against a warm cache
// without a billing account.
func NewGeocodingService(geocodes *store.GeocodeStore, geocoder geocode.Geocoder) *GeocodingService {
	return &GeocodingService{geocodes: geocodes, geocoder: geocoder}
}

// Resolution is the outcome of resolving a batch of addresses. Inputs are keyed
// by the raw address string as passed in, so a caller can map results back onto
// its own stops without re-normalizing.
type Resolution struct {
	// Resolved holds the addresses that now have coordinates, from cache or
	// from a fresh lookup.
	Resolved map[string]domain.GeocodedAddress
	// Failed holds addresses that could not be resolved, with the reason.
	// Route planning surfaces these to staff rather than dropping the stop —
	// a silently missing stop is a missed delivery.
	Failed map[string]error
	// CacheHits and Lookups count where the answers came from. Used by the
	// warming tool's report, and by the cache-hit-rate metric in a later step.
	CacheHits int
	Lookups   int
}

// LowConfidence returns the resolved addresses the provider could not pin
// precisely — the ones staff should eyeball before a driver is sent to them.
func (r Resolution) LowConfidence() []domain.GeocodedAddress {
	var out []domain.GeocodedAddress
	for _, g := range r.Resolved {
		if !g.Confidence.Precise() {
			out = append(out, g)
		}
	}
	return out
}

// Resolve resolves a single address, cache first.
//
// Must not be called inside a transaction — see the type comment.
func (s *GeocodingService) Resolve(ctx context.Context, pool *pgxpool.Pool, rawAddress string) (*domain.GeocodedAddress, error) {
	res, err := s.ResolveMany(ctx, pool, []string{rawAddress})
	if err != nil {
		return nil, err
	}
	if g, ok := res.Resolved[rawAddress]; ok {
		return &g, nil
	}
	if failure, ok := res.Failed[rawAddress]; ok {
		return nil, failure
	}
	// Unreachable unless ResolveMany drops an input on the floor.
	return nil, fmt.Errorf("%w: %s", ErrAddressNotGeocodable, rawAddress)
}

// ResolveMany resolves a batch of addresses, cache first.
//
// One cache read covers the whole batch, then each miss is looked up
// individually and written in its own transaction. Writing per result rather
// than batching at the end means a provider failure partway through still banks
// the addresses that did resolve — on a paid API, throwing away completed
// lookups because a later one timed out is money on the floor.
//
// Never returns a partial-failure error: an address that cannot be resolved
// lands in Failed, and the error return is reserved for database faults that
// make the whole batch meaningless.
//
// Must not be called inside a transaction — see the type comment.
func (s *GeocodingService) ResolveMany(ctx context.Context, pool *pgxpool.Pool, rawAddresses []string) (Resolution, error) {
	res := Resolution{
		Resolved: make(map[string]domain.GeocodedAddress, len(rawAddresses)),
		Failed:   make(map[string]error),
	}
	if len(rawAddresses) == 0 {
		return res, nil
	}

	// Normalize once. Several raw spellings can share a key — that is the
	// cache doing its job — so keep both directions of the mapping.
	keyFor := make(map[string]string, len(rawAddresses))
	uniqueKeys := make([]string, 0, len(rawAddresses))
	seenKey := make(map[string]bool, len(rawAddresses))
	for _, raw := range rawAddresses {
		key := domain.NormalizeAddress(raw)
		if key == "" {
			res.Failed[raw] = fmt.Errorf("%w: blank address", ErrAddressNotGeocodable)
			continue
		}
		keyFor[raw] = key
		if !seenKey[key] {
			seenKey[key] = true
			uniqueKeys = append(uniqueKeys, key)
		}
	}
	if len(uniqueKeys) == 0 {
		return res, nil
	}

	// Phase 1 — read the cache. This transaction closes before any HTTP call.
	var cached map[string]domain.GeocodedAddress
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		var txErr error
		cached, txErr = s.geocodes.ListByNormalized(ctx, tx, uniqueKeys)
		return txErr
	}); err != nil {
		return Resolution{}, fmt.Errorf("read geocode cache: %w", err)
	}

	// Phase 2 — look up whatever the cache didn't have, one address at a time,
	// each written in its own transaction. No transaction is open here.
	for _, raw := range rawAddresses {
		key, ok := keyFor[raw]
		if !ok {
			continue // already recorded as failed (blank)
		}
		if hit, ok := cached[key]; ok {
			res.Resolved[raw] = hit
			res.CacheHits++
			continue
		}

		geocoded, err := s.lookupAndCache(ctx, pool, raw, key)
		if err != nil {
			res.Failed[raw] = err
			continue
		}
		res.Lookups++
		res.Resolved[raw] = *geocoded
		// Seed the local map so a repeat of the same address later in the
		// batch is not a second billable lookup.
		cached[key] = *geocoded
	}

	return res, nil
}

// lookupAndCache calls the provider and writes the result in its own
// transaction. Failures are classified so the caller can tell "fix this
// address" from "try again later".
func (s *GeocodingService) lookupAndCache(ctx context.Context, pool *pgxpool.Pool, raw, key string) (*domain.GeocodedAddress, error) {
	if s.geocoder == nil {
		return nil, ErrGeocoderNotConfigured
	}

	result, err := s.geocoder.Geocode(ctx, raw)
	if err != nil {
		switch {
		case errors.Is(err, geocode.ErrNotFound):
			return nil, fmt.Errorf("%w: %s", ErrAddressNotGeocodable, raw)
		case errors.Is(err, geocode.ErrNotConfigured):
			return nil, ErrGeocoderNotConfigured
		case errors.Is(err, geocode.ErrUnavailable):
			return nil, fmt.Errorf("%w: %v", ErrGeocoderUnavailable, err)
		default:
			// Configuration faults (bad key, API not enabled) land here.
			// Deliberately not ErrGeocoderUnavailable — retrying will not fix
			// a key that was never valid.
			return nil, fmt.Errorf("geocode %q: %w", raw, err)
		}
	}

	// A provider that answers OK with a null island coordinate is answering
	// wrong; routing a driver to (0,0) is worse than failing the stop.
	if result.Lat == 0 && result.Lng == 0 {
		return nil, fmt.Errorf("%w: provider returned a null coordinate for %s", ErrAddressNotGeocodable, raw)
	}

	var saved *domain.GeocodedAddress
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		var txErr error
		saved, txErr = s.geocodes.Upsert(ctx, tx, store.UpsertGeocodedAddressParams{
			NormalizedAddress: key,
			RawAddress:        raw,
			Lat:               result.Lat,
			Lng:               result.Lng,
			Provider:          "google",
			Confidence:        domain.GeocodeConfidence(result.Confidence),
		})
		return txErr
	}); err != nil {
		return nil, fmt.Errorf("cache geocode for %q: %w", raw, err)
	}
	return saved, nil
}

// ListLowConfidenceAddresses returns cached addresses the provider could not
// pin precisely, newest first. Read-only, so unlike the resolve path it takes a
// transaction and can be called from inside one.
func (s *GeocodingService) ListLowConfidenceAddresses(ctx context.Context, tx pgx.Tx, limit int) ([]domain.GeocodedAddress, error) {
	return s.geocodes.ListLowConfidence(ctx, tx, limit)
}

// CountCachedAddresses returns the size of the geocode cache.
func (s *GeocodingService) CountCachedAddresses(ctx context.Context, tx pgx.Tx) (int, error) {
	return s.geocodes.CountGeocodedAddresses(ctx, tx)
}

// WarmCache geocodes every address that has appeared on a local-delivery order,
// so the first real route plan reads from cache instead of firing a burst of
// billable lookups — and so any address the provider cannot pin is discovered
// in the office rather than by a driver at the curb.
//
// Safe to re-run: addresses already cached cost nothing and come back as hits.
//
// Must not be called inside a transaction — it resolves, and therefore calls
// out to the provider. See the type comment.
func (s *GeocodingService) WarmCache(ctx context.Context, pool *pgxpool.Pool, limit int) (Resolution, error) {
	var addresses []domain.Address
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		var txErr error
		addresses, txErr = s.geocodes.ListLocalDeliveryAddresses(ctx, tx, limit)
		return txErr
	}); err != nil {
		return Resolution{}, fmt.Errorf("list delivery addresses: %w", err)
	}

	raw := make([]string, 0, len(addresses))
	for _, a := range addresses {
		if formatted := domain.FormatAddressForGeocoding(a); formatted != "" {
			raw = append(raw, formatted)
		}
	}
	return s.ResolveMany(ctx, pool, raw)
}
