// Package geocode resolves street addresses to coordinates. Route planning
// needs lat/lng — OSRM will not accept an address — and this is the only paid
// external call in the routing feature, so it sits behind an interface and
// everything above it goes through a cache first.
//
// Provider-neutral by design: the concrete Google client is one file over, and
// the free US Census geocoder is a plausible future swap if Google's billing
// ever becomes a nuisance. Nothing in this package imports the rest of Hiri.
package geocode

import (
	"context"
	"errors"
)

// Result is one provider's answer for one address.
type Result struct {
	Lat float64
	Lng float64
	// Confidence is the provider's own precision classification, passed through
	// verbatim (Google's location_type). The app layer maps it onto
	// domain.GeocodeConfidence — this package stays domain-free.
	Confidence string
	// FormattedAddress is the provider's canonical rendering, useful when a
	// staff member is looking at a low-confidence result and trying to work out
	// what the geocoder actually matched.
	FormattedAddress string
}

// Geocoder resolves a single address. Implementations must be safe for
// concurrent use.
type Geocoder interface {
	Geocode(ctx context.Context, address string) (Result, error)
}

var (
	// ErrNotFound — the provider ran the query and matched nothing. Permanent
	// for this address text: retrying will not help, and the address needs a
	// human to look at it.
	ErrNotFound = errors.New("geocode: no match for address")

	// ErrUnavailable — a transient provider-side failure (quota exceeded,
	// timeout, 5xx). The address may geocode fine later, so callers should
	// surface it as an outage rather than a bad address.
	ErrUnavailable = errors.New("geocode: provider unavailable")

	// ErrNotConfigured — no API key. Returned rather than silently no-op'ing
	// because there is no sensible zero value for a coordinate: a route planned
	// from (0,0) would send a driver into the Atlantic.
	ErrNotConfigured = errors.New("geocode: provider not configured")
)
