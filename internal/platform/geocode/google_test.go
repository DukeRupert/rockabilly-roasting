package geocode_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/platform/geocode"
)

// newStubGoogle points a GoogleGeocoder at a test server returning body.
func newStubGoogle(t *testing.T, status int, body string) (*geocode.GoogleGeocoder, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	g := geocode.NewGoogleGeocoder("test-key")
	g.BaseURL = srv.URL
	return g, srv
}

const okResponse = `{
  "status": "OK",
  "results": [{
    "formatted_address": "1234 W 4th Ave, Kennewick, WA 99336, USA",
    "geometry": {
      "location": {"lat": 46.2087, "lng": -119.1372},
      "location_type": "ROOFTOP"
    }
  }]
}`

func TestGoogleGeocodeSuccess(t *testing.T) {
	g, _ := newStubGoogle(t, http.StatusOK, okResponse)

	res, err := g.Geocode(context.Background(), "1234 W 4th Ave, Kennewick, WA 99336")
	require.NoError(t, err)
	assert.InDelta(t, 46.2087, res.Lat, 0.0001)
	assert.InDelta(t, -119.1372, res.Lng, 0.0001)
	assert.Equal(t, "ROOFTOP", res.Confidence)
	assert.Equal(t, "1234 W 4th Ave, Kennewick, WA 99336, USA", res.FormattedAddress)
}

// The request must carry the address, the key, and the US constraint —
// dropping components=country:US is how "Richland, WA" quietly becomes
// Richland, New Zealand.
func TestGoogleGeocodeRequestParams(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okResponse))
	}))
	defer srv.Close()

	g := geocode.NewGoogleGeocoder("test-key")
	g.BaseURL = srv.URL

	_, err := g.Geocode(context.Background(), "1234 W 4th Ave")
	require.NoError(t, err)
	assert.Equal(t, "1234 W 4th Ave", got.Get("address"))
	assert.Equal(t, "test-key", got.Get("key"))
	assert.Equal(t, "country:US", got.Get("components"))
	assert.Equal(t, "us", got.Get("region"))
}

// Google reports failure in the body with a 200 status, so each status string
// has to map onto the right error class: permanent (don't retry, ask a human)
// versus transient (retry later).
func TestGoogleGeocodeStatusMapping(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr error
	}{
		{"zero results", `{"status":"ZERO_RESULTS","results":[]}`, geocode.ErrNotFound},
		{"over query limit", `{"status":"OVER_QUERY_LIMIT","error_message":"quota"}`, geocode.ErrUnavailable},
		{"over daily limit", `{"status":"OVER_DAILY_LIMIT"}`, geocode.ErrUnavailable},
		{"unknown error", `{"status":"UNKNOWN_ERROR"}`, geocode.ErrUnavailable},
		{"ok but empty results", `{"status":"OK","results":[]}`, geocode.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _ := newStubGoogle(t, http.StatusOK, tt.body)
			_, err := g.Geocode(context.Background(), "somewhere")
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

// A bad key is a deploy fault, not an outage — it must NOT come back as
// ErrUnavailable, or the caller will retry forever against a key that will
// never work.
func TestGoogleGeocodeConfigurationFaultsAreNotTransient(t *testing.T) {
	for _, body := range []string{
		`{"status":"REQUEST_DENIED","error_message":"The provided API key is invalid."}`,
		`{"status":"INVALID_REQUEST"}`,
	} {
		g, _ := newStubGoogle(t, http.StatusOK, body)
		_, err := g.Geocode(context.Background(), "1234 W 4th Ave")
		require.Error(t, err)
		assert.NotErrorIs(t, err, geocode.ErrUnavailable)
		assert.NotErrorIs(t, err, geocode.ErrNotFound)
	}
}

func TestGoogleGeocodeServerErrorIsTransient(t *testing.T) {
	g, _ := newStubGoogle(t, http.StatusBadGateway, `<html>502</html>`)
	_, err := g.Geocode(context.Background(), "1234 W 4th Ave")
	assert.ErrorIs(t, err, geocode.ErrUnavailable)
}

func TestGoogleGeocodeUnreachableHostIsTransient(t *testing.T) {
	g := geocode.NewGoogleGeocoder("test-key")
	// Reserved TEST-NET-1 address; nothing listens there. Packets to it are
	// dropped rather than refused, so without a short timeout this test would
	// sit on the client's default 10s.
	g.BaseURL = "http://192.0.2.1:1/geocode"
	g.HTTP = &http.Client{Timeout: 100 * time.Millisecond}
	_, err := g.Geocode(context.Background(), "1234 W 4th Ave")
	assert.ErrorIs(t, err, geocode.ErrUnavailable)
}

func TestGoogleGeocodeDisabledWithoutKey(t *testing.T) {
	g := geocode.NewGoogleGeocoder("")
	assert.False(t, g.Enabled())

	_, err := g.Geocode(context.Background(), "1234 W 4th Ave")
	assert.ErrorIs(t, err, geocode.ErrNotConfigured)
}

func TestGoogleGeocodeEmptyAddress(t *testing.T) {
	g, _ := newStubGoogle(t, http.StatusOK, okResponse)
	_, err := g.Geocode(context.Background(), "   ")
	assert.ErrorIs(t, err, geocode.ErrNotFound)
}

func TestGoogleGeocodeMalformedJSON(t *testing.T) {
	g, _ := newStubGoogle(t, http.StatusOK, `{"status": "OK", "results": [`)
	_, err := g.Geocode(context.Background(), "1234 W 4th Ave")
	require.Error(t, err)
	assert.NotErrorIs(t, err, geocode.ErrNotFound)
}
