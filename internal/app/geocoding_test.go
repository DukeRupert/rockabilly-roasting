package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/geocode"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// stubGeocoder stands in for Google. It records every address it was asked
// about, which is how these tests assert the cache actually prevents billable
// calls rather than merely returning the right answer.
type stubGeocoder struct {
	mu      sync.Mutex
	calls   []string
	result  geocode.Result
	err     error
	results map[string]geocode.Result
}

func (s *stubGeocoder) Geocode(_ context.Context, address string) (geocode.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, address)
	if s.err != nil {
		return geocode.Result{}, s.err
	}
	if s.results != nil {
		if r, ok := s.results[address]; ok {
			return r, nil
		}
		return geocode.Result{}, geocode.ErrNotFound
	}
	return s.result, nil
}

func (s *stubGeocoder) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func kennewickResult() geocode.Result {
	return geocode.Result{
		Lat:              46.2087,
		Lng:              -119.1372,
		Confidence:       string(domain.GeocodeRooftop),
		FormattedAddress: "1234 W 4th Ave, Kennewick, WA 99336, USA",
	}
}

// newGeocodingService wires the service against a stub provider and clears the
// cache table afterwards. The service commits its own transactions (it has to —
// the provider call must not happen inside one), so the usual rollback-per-test
// isolation is not available here.
func newGeocodingService(t *testing.T, g geocode.Geocoder) *app.GeocodingService {
	t.Helper()
	t.Cleanup(func() {
		_, err := testPool.Exec(context.Background(), `TRUNCATE geocoded_addresses`)
		require.NoError(t, err)
	})
	return app.NewGeocodingService(store.NewGeocodeStore(nil), g)
}

// The whole point of the feature: the second ask for an address must not reach
// the provider.
func TestGeocodingService_CacheHitOnSecondCall(t *testing.T) {
	ctx := context.Background()
	stub := &stubGeocoder{result: kennewickResult()}
	svc := newGeocodingService(t, stub)
	addr := "1234 W 4th Ave, Kennewick, WA 99336"

	first, err := svc.Resolve(ctx, testPool, addr)
	require.NoError(t, err)
	assert.InDelta(t, 46.2087, first.Lat, 0.0001)
	assert.InDelta(t, -119.1372, first.Lng, 0.0001)
	assert.Equal(t, domain.GeocodeRooftop, first.Confidence)
	assert.Equal(t, "google", first.Provider)
	assert.Equal(t, 1, stub.callCount())

	second, err := svc.Resolve(ctx, testPool, addr)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID, "second resolve should return the cached row")
	assert.Equal(t, 1, stub.callCount(), "cached address must not be looked up again")
}

// Different spellings of one address share a cache row — the reason
// NormalizeAddress exists at all.
func TestGeocodingService_SpellingVariantsShareOneLookup(t *testing.T) {
	ctx := context.Background()
	stub := &stubGeocoder{result: kennewickResult()}
	svc := newGeocodingService(t, stub)

	variants := []string{
		"1234 W 4th Ave, Kennewick, WA 99336",
		"1234 West 4th Avenue, Kennewick, WA 99336",
		"1234 w. 4th ave., kennewick, wa 99336-1234",
		"1234 W 4th Ave, Kennewick, Washington 99336, USA",
	}
	var firstID string
	for i, v := range variants {
		g, err := svc.Resolve(ctx, testPool, v)
		require.NoError(t, err, "variant: %s", v)
		if i == 0 {
			firstID = g.ID.String()
		}
		assert.Equal(t, firstID, g.ID.String(), "variant %q should hit the same cache row", v)
	}
	assert.Equal(t, 1, stub.callCount(), "four spellings of one address is one lookup")
}

func TestGeocodingService_ResolveManyCountsHitsAndLookups(t *testing.T) {
	ctx := context.Background()
	stub := &stubGeocoder{results: map[string]geocode.Result{
		"1234 W 4th Ave, Kennewick, WA 99336": kennewickResult(),
		"500 Road 68, Pasco, WA 99301": {
			Lat: 46.2540, Lng: -119.1600, Confidence: string(domain.GeocodeRooftop),
		},
	}}
	svc := newGeocodingService(t, stub)

	// Warm one of the two.
	_, err := svc.Resolve(ctx, testPool, "1234 W 4th Ave, Kennewick, WA 99336")
	require.NoError(t, err)

	res, err := svc.ResolveMany(ctx, testPool, []string{
		"1234 W 4th Ave, Kennewick, WA 99336",
		"500 Road 68, Pasco, WA 99301",
	})
	require.NoError(t, err)
	assert.Len(t, res.Resolved, 2)
	assert.Empty(t, res.Failed)
	assert.Equal(t, 1, res.CacheHits)
	assert.Equal(t, 1, res.Lookups)
}

// A repeated address inside one batch must not be billed twice — a route with
// two orders going to the same office is ordinary.
func TestGeocodingService_ResolveManyDeduplicatesWithinBatch(t *testing.T) {
	ctx := context.Background()
	stub := &stubGeocoder{result: kennewickResult()}
	svc := newGeocodingService(t, stub)

	res, err := svc.ResolveMany(ctx, testPool, []string{
		"1234 W 4th Ave, Kennewick, WA 99336",
		"1234 West 4th Avenue, Kennewick, WA 99336",
		"1234 W 4th Ave, Kennewick, WA 99336",
	})
	require.NoError(t, err)
	assert.Len(t, res.Resolved, 2, "keyed by raw input, so two distinct strings")
	assert.Equal(t, 1, stub.callCount())
}

// One bad address must not sink the batch — the driver still gets the rest of
// the route, and staff get told which stop needs fixing.
func TestGeocodingService_ResolveManyIsolatesFailures(t *testing.T) {
	ctx := context.Background()
	stub := &stubGeocoder{results: map[string]geocode.Result{
		"1234 W 4th Ave, Kennewick, WA 99336": kennewickResult(),
	}}
	svc := newGeocodingService(t, stub)

	res, err := svc.ResolveMany(ctx, testPool, []string{
		"1234 W 4th Ave, Kennewick, WA 99336",
		"asdf jkl nowhere",
		"   ",
	})
	require.NoError(t, err, "a bad address is a per-address failure, not a batch error")
	assert.Len(t, res.Resolved, 1)
	assert.Len(t, res.Failed, 2)
	assert.ErrorIs(t, res.Failed["asdf jkl nowhere"], app.ErrAddressNotGeocodable)
	assert.ErrorIs(t, res.Failed["   "], app.ErrAddressNotGeocodable)
}

// "Provider is down" and "this address is wrong" call for different responses
// from staff, so they must not arrive as the same error.
func TestGeocodingService_ClassifiesProviderFailures(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		stubErr error
		wantErr error
	}{
		{"not found", geocode.ErrNotFound, app.ErrAddressNotGeocodable},
		{"unavailable", geocode.ErrUnavailable, app.ErrGeocoderUnavailable},
		{"not configured", geocode.ErrNotConfigured, app.ErrGeocoderNotConfigured},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newGeocodingService(t, &stubGeocoder{err: tt.stubErr})
			_, err := svc.Resolve(ctx, testPool, "1234 W 4th Ave, Kennewick, WA 99336")
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

// A failed lookup must not be cached as a success — otherwise one outage
// poisons the address until someone notices.
func TestGeocodingService_FailuresAreNotCached(t *testing.T) {
	ctx := context.Background()
	stub := &stubGeocoder{err: geocode.ErrUnavailable}
	svc := newGeocodingService(t, stub)
	addr := "1234 W 4th Ave, Kennewick, WA 99336"

	_, err := svc.Resolve(ctx, testPool, addr)
	require.ErrorIs(t, err, app.ErrGeocoderUnavailable)

	// Provider recovers.
	stub.mu.Lock()
	stub.err = nil
	stub.result = kennewickResult()
	stub.mu.Unlock()

	g, err := svc.Resolve(ctx, testPool, addr)
	require.NoError(t, err)
	assert.InDelta(t, 46.2087, g.Lat, 0.0001)
	assert.Equal(t, 2, stub.callCount(), "the failed attempt should not have been cached")
}

// (0,0) is in the Atlantic. A provider that answers OK with it is answering
// wrong, and routing a driver there is worse than failing the stop.
func TestGeocodingService_RejectsNullIsland(t *testing.T) {
	ctx := context.Background()
	svc := newGeocodingService(t, &stubGeocoder{result: geocode.Result{
		Lat: 0, Lng: 0, Confidence: string(domain.GeocodeApproximate),
	}})

	_, err := svc.Resolve(ctx, testPool, "1234 W 4th Ave, Kennewick, WA 99336")
	assert.ErrorIs(t, err, app.ErrAddressNotGeocodable)
}

// Low-confidence results resolve, but must be flagged so staff can review them
// before a driver is sent.
func TestGeocodingService_SurfacesLowConfidence(t *testing.T) {
	ctx := context.Background()
	stub := &stubGeocoder{results: map[string]geocode.Result{
		"1234 W 4th Ave, Kennewick, WA 99336": kennewickResult(),
		"Somewhere Off Rd 100, Pasco, WA 99301": {
			Lat: 46.30, Lng: -119.20, Confidence: string(domain.GeocodeApproximate),
		},
	}}
	svc := newGeocodingService(t, stub)

	res, err := svc.ResolveMany(ctx, testPool, []string{
		"1234 W 4th Ave, Kennewick, WA 99336",
		"Somewhere Off Rd 100, Pasco, WA 99301",
	})
	require.NoError(t, err)
	require.Len(t, res.Resolved, 2)

	low := res.LowConfidence()
	require.Len(t, low, 1)
	assert.Equal(t, domain.GeocodeApproximate, low[0].Confidence)
	assert.False(t, low[0].Confidence.Precise())

	// And the same rows come back from the admin-facing report.
	tx := testutil.NewTestTx(t, testPool)
	listed, err := svc.ListLowConfidenceAddresses(ctx, tx, 50)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, low[0].ID, listed[0].ID)

	count, err := svc.CountCachedAddresses(ctx, tx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

// Local dev with no API key must still serve a warm cache rather than failing
// every call outright.
func TestGeocodingService_CacheServesWhenProviderUnconfigured(t *testing.T) {
	ctx := context.Background()
	addr := "1234 W 4th Ave, Kennewick, WA 99336"

	warm := newGeocodingService(t, &stubGeocoder{result: kennewickResult()})
	_, err := warm.Resolve(ctx, testPool, addr)
	require.NoError(t, err)

	// Same database, no provider at all.
	cold := app.NewGeocodingService(store.NewGeocodeStore(nil), nil)

	g, err := cold.Resolve(ctx, testPool, addr)
	require.NoError(t, err, "cached addresses resolve without a provider")
	assert.InDelta(t, 46.2087, g.Lat, 0.0001)

	_, err = cold.Resolve(ctx, testPool, "999 Unknown St, Pasco, WA 99301")
	assert.ErrorIs(t, err, app.ErrGeocoderNotConfigured)
}

func TestGeocodingService_ResolveManyEmptyInput(t *testing.T) {
	svc := newGeocodingService(t, &stubGeocoder{})
	res, err := svc.ResolveMany(context.Background(), testPool, nil)
	require.NoError(t, err)
	assert.Empty(t, res.Resolved)
	assert.Empty(t, res.Failed)
	assert.Equal(t, 0, res.CacheHits)
}

func TestGeocodingService_UnexpectedProviderErrorIsNotTransient(t *testing.T) {
	svc := newGeocodingService(t, &stubGeocoder{err: errors.New("REQUEST_DENIED: bad key")})
	_, err := svc.Resolve(context.Background(), testPool, "1234 W 4th Ave, Kennewick, WA 99336")
	require.Error(t, err)
	assert.NotErrorIs(t, err, app.ErrGeocoderUnavailable)
	assert.NotErrorIs(t, err, app.ErrAddressNotGeocodable)
}
