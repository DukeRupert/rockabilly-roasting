package geocode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultGoogleBaseURL = "https://maps.googleapis.com/maps/api/geocode/json"

// GoogleGeocoder calls the Google Geocoding API. A GoogleGeocoder with an empty
// APIKey is disabled and returns ErrNotConfigured from every call — unlike the
// newsletter client, it cannot no-op, since a missing coordinate has no safe
// default (see ErrNotConfigured).
type GoogleGeocoder struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
	// Region biases ambiguous results toward a country. "us" keeps
	// "Richland, WA" from resolving to Richland in New Zealand.
	Region string
}

// NewGoogleGeocoder returns a geocoder with a 10-second timeout. Pass an empty
// key to construct a disabled client (local dev without billing configured).
func NewGoogleGeocoder(apiKey string) *GoogleGeocoder {
	return &GoogleGeocoder{
		BaseURL: defaultGoogleBaseURL,
		APIKey:  strings.TrimSpace(apiKey),
		HTTP:    &http.Client{Timeout: 10 * time.Second},
		Region:  "us",
	}
}

// Enabled reports whether this geocoder will actually call out.
func (g *GoogleGeocoder) Enabled() bool {
	return g != nil && g.APIKey != ""
}

// googleResponse is the subset of the Geocoding API payload we read.
type googleResponse struct {
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message"`
	Results      []struct {
		FormattedAddress string `json:"formatted_address"`
		Geometry         struct {
			Location struct {
				Lat float64 `json:"lat"`
				Lng float64 `json:"lng"`
			} `json:"location"`
			LocationType string `json:"location_type"`
		} `json:"geometry"`
	} `json:"results"`
}

// Geocode resolves one address to coordinates.
//
// Must not be called inside a database transaction — it is a network call to a
// third party, and holding a pgx transaction open across it is the pattern
// CLAUDE.md forbids. The cache-first wrapper in app/ is what sequences the
// read tx, this call, and the write tx.
func (g *GoogleGeocoder) Geocode(ctx context.Context, address string) (Result, error) {
	if !g.Enabled() {
		return Result{}, ErrNotConfigured
	}
	address = strings.TrimSpace(address)
	if address == "" {
		return Result{}, fmt.Errorf("%w: empty address", ErrNotFound)
	}

	base := g.BaseURL
	if base == "" {
		base = defaultGoogleBaseURL
	}
	q := url.Values{}
	q.Set("address", address)
	q.Set("key", g.APIKey)
	// Constrain to the US so a mistyped address fails loudly instead of
	// resolving to a same-named town on another continent.
	q.Set("components", "country:US")
	if g.Region != "" {
		q.Set("region", g.Region)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?"+q.Encode(), nil)
	if err != nil {
		return Result{}, fmt.Errorf("geocode: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	httpClient := g.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		// Transport failures are transient by nature — DNS, timeout, reset.
		return Result{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	// Bounded read so a misbehaving upstream can't stream us to death.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Result{}, fmt.Errorf("%w: read body: %v", ErrUnavailable, err)
	}
	if resp.StatusCode >= 500 {
		return Result{}, fmt.Errorf("%w: http %d", ErrUnavailable, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("geocode: http %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed googleResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Result{}, fmt.Errorf("geocode: decode response: %w", err)
	}

	// Google signals failure in the body, not the status code — a quota
	// breach is a 200 with status OVER_QUERY_LIMIT.
	switch parsed.Status {
	case "OK":
	case "ZERO_RESULTS":
		return Result{}, fmt.Errorf("%w: %s", ErrNotFound, address)
	case "OVER_QUERY_LIMIT", "UNKNOWN_ERROR", "OVER_DAILY_LIMIT":
		return Result{}, fmt.Errorf("%w: %s: %s", ErrUnavailable, parsed.Status, parsed.ErrorMessage)
	case "REQUEST_DENIED", "INVALID_REQUEST":
		// Configuration faults — bad key, unenabled API, malformed query.
		// Retrying is pointless and the deploy needs fixing.
		return Result{}, fmt.Errorf("geocode: %s: %s", parsed.Status, parsed.ErrorMessage)
	default:
		return Result{}, fmt.Errorf("geocode: unexpected status %q: %s", parsed.Status, parsed.ErrorMessage)
	}

	if len(parsed.Results) == 0 {
		// OK with no results shouldn't happen, but treating it as a match
		// would hand back (0,0).
		return Result{}, fmt.Errorf("%w: %s", ErrNotFound, address)
	}

	// First result only. Google returns candidates best-first, and a delivery
	// address that is genuinely ambiguous should be reviewed by staff via the
	// confidence flag rather than guessed at here.
	top := parsed.Results[0]
	return Result{
		Lat:              top.Geometry.Location.Lat,
		Lng:              top.Geometry.Location.Lng,
		Confidence:       top.Geometry.LocationType,
		FormattedAddress: top.FormattedAddress,
	}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
