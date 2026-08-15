package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
)

// GeocodeStore provides database access to the address geocoding cache.
type GeocodeStore struct {
	metrics QueryRecorder
}

// NewGeocodeStore creates a new GeocodeStore. Pass nil for metrics to disable
// query timing instrumentation (e.g. in tests or one-off CLI tools).
func NewGeocodeStore(metrics QueryRecorder) *GeocodeStore {
	return &GeocodeStore{metrics: metrics}
}

const geocodedAddressColumns = `id, normalized_address, raw_address, lat, lng, provider, confidence, geocoded_at`

func scanGeocodedAddress(row pgx.Row) (*domain.GeocodedAddress, error) {
	var g domain.GeocodedAddress
	var confidence string
	if err := row.Scan(&g.ID, &g.NormalizedAddress, &g.RawAddress, &g.Lat, &g.Lng, &g.Provider, &confidence, &g.GeocodedAt); err != nil {
		return nil, err
	}
	g.Confidence = domain.GeocodeConfidence(confidence)
	return &g, nil
}

// GetByNormalized returns the cached geocode for a normalized address key.
// Returns pgx.ErrNoRows on a cache miss — callers translate that into "call
// the provider", not into an error.
func (s *GeocodeStore) GetByNormalized(ctx context.Context, tx pgx.Tx, normalized string) (_ *domain.GeocodedAddress, err error) {
	defer trackQuery(s.metrics, "geocoded_addresses.get_by_normalized", time.Now(), &err)
	row := tx.QueryRow(ctx,
		`SELECT `+geocodedAddressColumns+` FROM geocoded_addresses WHERE normalized_address = $1`,
		normalized,
	)
	g, err := scanGeocodedAddress(row)
	if err != nil {
		return nil, err
	}
	return g, nil
}

// ListByNormalized returns every cached geocode whose key is in keys, mapped by
// normalized address. Missing keys are simply absent from the map.
//
// Route planning resolves a whole van's worth of addresses at once, so this
// exists to keep that a single round trip instead of one query per stop.
func (s *GeocodeStore) ListByNormalized(ctx context.Context, tx pgx.Tx, keys []string) (_ map[string]domain.GeocodedAddress, err error) {
	defer trackQuery(s.metrics, "geocoded_addresses.list_by_normalized", time.Now(), &err)
	out := make(map[string]domain.GeocodedAddress, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx,
		`SELECT `+geocodedAddressColumns+` FROM geocoded_addresses WHERE normalized_address = ANY($1)`,
		keys,
	)
	if err != nil {
		return nil, fmt.Errorf("list geocoded addresses: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		g, scanErr := scanGeocodedAddress(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan geocoded address: %w", scanErr)
		}
		out[g.NormalizedAddress] = *g
	}
	return out, rows.Err()
}

// UpsertGeocodedAddressParams holds the fields needed to cache a geocode.
type UpsertGeocodedAddressParams struct {
	NormalizedAddress string
	RawAddress        string
	Lat               float64
	Lng               float64
	Provider          string
	Confidence        domain.GeocodeConfidence
}

// Upsert writes a geocode result, replacing any existing row for the same
// normalized address.
//
// Upsert rather than insert for two reasons: two route plans running at once
// can race on the same new address, and a re-geocode after a provider or
// normalization change should overwrite the stale coordinate rather than
// conflict with it. geocoded_at is refreshed so the row's age always reflects
// when its coordinates were last confirmed.
func (s *GeocodeStore) Upsert(ctx context.Context, tx pgx.Tx, p UpsertGeocodedAddressParams) (_ *domain.GeocodedAddress, err error) {
	defer trackQuery(s.metrics, "geocoded_addresses.upsert", time.Now(), &err)
	row := tx.QueryRow(ctx,
		`INSERT INTO geocoded_addresses (id, normalized_address, raw_address, lat, lng, provider, confidence, geocoded_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		 ON CONFLICT (normalized_address) DO UPDATE SET
		     raw_address = EXCLUDED.raw_address,
		     lat         = EXCLUDED.lat,
		     lng         = EXCLUDED.lng,
		     provider    = EXCLUDED.provider,
		     confidence  = EXCLUDED.confidence,
		     geocoded_at = now()
		 RETURNING `+geocodedAddressColumns,
		uuid.New(), p.NormalizedAddress, p.RawAddress, p.Lat, p.Lng, p.Provider, string(p.Confidence),
	)
	g, err := scanGeocodedAddress(row)
	if err != nil {
		return nil, fmt.Errorf("upsert geocoded address %q: %w", p.NormalizedAddress, err)
	}
	return g, nil
}

// ListLowConfidence returns cached addresses the provider could not pin
// precisely — the report staff work through before a route goes out, since
// these are the stops most likely to send a driver to the wrong building.
// Newest first, so addresses from the current run surface at the top.
func (s *GeocodeStore) ListLowConfidence(ctx context.Context, tx pgx.Tx, limit int) (_ []domain.GeocodedAddress, err error) {
	defer trackQuery(s.metrics, "geocoded_addresses.list_low_confidence", time.Now(), &err)
	if limit <= 0 {
		limit = 100
	}
	// Matches the partial index in migration 066 — keep the two in step.
	rows, err := tx.Query(ctx,
		`SELECT `+geocodedAddressColumns+`
		   FROM geocoded_addresses
		  WHERE confidence NOT IN ('ROOFTOP', 'RANGE_INTERPOLATED')
		  ORDER BY geocoded_at DESC
		  LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list low confidence geocodes: %w", err)
	}
	defer rows.Close()

	var out []domain.GeocodedAddress
	for rows.Next() {
		g, scanErr := scanGeocodedAddress(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan geocoded address: %w", scanErr)
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

// ListLocalDeliveryAddresses returns the distinct shipping addresses that have
// been used on local-delivery orders, most recently ordered first.
//
// This is the cache-warming set: geocoding it up front means the first real
// route plan is a cache read rather than a burst of billable lookups, and any
// address the provider can't pin surfaces in the office instead of on the road.
// Deliberately unfiltered by order status — a household that ordered six months
// ago is still a household that will order again.
func (s *GeocodeStore) ListLocalDeliveryAddresses(ctx context.Context, tx pgx.Tx, limit int) (_ []domain.Address, err error) {
	defer trackQuery(s.metrics, "geocoded_addresses.list_local_delivery_addresses", time.Now(), &err)
	if limit <= 0 {
		limit = 1000
	}
	rows, err := tx.Query(ctx,
		`SELECT DISTINCT ON (a.line1, a.line2, a.city, a.state, a.postal_code)
		        a.id, a.customer_id, a.first_name, a.last_name, a.company,
		        a.line1, a.line2, a.city, a.state, a.postal_code, a.country_code, a.is_default
		   FROM orders o
		   JOIN addresses a ON a.id = o.shipping_address_id
		  WHERE o.shipping_method = 'local_delivery'
		  ORDER BY a.line1, a.line2, a.city, a.state, a.postal_code, o.placed_at DESC
		  LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list local delivery addresses: %w", err)
	}
	defer rows.Close()

	var out []domain.Address
	for rows.Next() {
		var a domain.Address
		if err := rows.Scan(&a.ID, &a.CustomerID, &a.FirstName, &a.LastName, &a.Company,
			&a.Line1, &a.Line2, &a.City, &a.State, &a.PostalCode, &a.CountryCode, &a.IsDefault); err != nil {
			return nil, fmt.Errorf("scan delivery address: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountGeocodedAddresses returns how many addresses are cached. Used by the
// cache-warming tool to report what it did.
func (s *GeocodeStore) CountGeocodedAddresses(ctx context.Context, tx pgx.Tx) (_ int, err error) {
	defer trackQuery(s.metrics, "geocoded_addresses.count", time.Now(), &err)
	var n int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM geocoded_addresses`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count geocoded addresses: %w", err)
	}
	return n, nil
}
