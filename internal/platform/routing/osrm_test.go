package routing_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/platform/routing"
)

// Real coordinates from the prod acceptance test (ops/osrm/README.md).
var (
	roastery = routing.Coordinate{Lat: 46.2087, Lng: -119.1372} // Kennewick
	pasco    = routing.Coordinate{Lat: 46.2396, Lng: -119.1006}
	richland = routing.Coordinate{Lat: 46.2857, Lng: -119.2845}
	middle   = routing.Coordinate{Lat: 46.2540, Lng: -119.1600}
)

func newStubRouter(t *testing.T, status int, body string) (*routing.Client, *string) {
	t.Helper()
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	c := routing.NewClient(srv.URL)
	return c, &gotPath
}

// The response shape prod actually returned: input order [0,3,1,2], meaning
// input 0 is visited first, input 2 second, input 3 third, input 1 last.
const prodTripResponse = `{
  "code": "Ok",
  "trips": [{"duration": 3099.6, "distance": 44921.3}],
  "waypoints": [
    {"waypoint_index": 0, "name": "", "distance": 9.5},
    {"waypoint_index": 3, "name": "North 8th Avenue", "distance": 3.1},
    {"waypoint_index": 1, "name": "Williams Boulevard", "distance": 3.9},
    {"waypoint_index": 2, "name": "West Dradie Street", "distance": 37.8}
  ]
}`

func TestTripReturnsVisitingOrder(t *testing.T) {
	c, _ := newStubRouter(t, http.StatusOK, prodTripResponse)

	res, err := c.Trip(context.Background(),
		[]routing.Coordinate{roastery, pasco, richland, middle},
		routing.TripOptions{Roundtrip: true})
	require.NoError(t, err)

	// OSRM said: input1→position3, input2→position1, input3→position2.
	// Inverted, the driver visits inputs in this order.
	assert.Equal(t, []int{0, 2, 3, 1}, res.Order)
	assert.InDelta(t, 3099.6, res.DurationSeconds, 0.01)
	assert.InDelta(t, 44921.3, res.DistanceMeters, 0.01)
}

// The single easiest mistake against OSRM. Reversed axes don't error — they
// route through the wrong hemisphere — so this asserts the exact path.
func TestTripSendsLngLatInThatOrder(t *testing.T) {
	c, gotPath := newStubRouter(t, http.StatusOK, prodTripResponse)

	_, err := c.Trip(context.Background(),
		[]routing.Coordinate{roastery, pasco, richland, middle},
		routing.TripOptions{Roundtrip: true})
	require.NoError(t, err)

	assert.Contains(t, *gotPath, "/trip/v1/driving/")
	// lng first, lat second, semicolon-separated, six decimals.
	assert.Contains(t, *gotPath, "-119.137200,46.208700;-119.100600,46.239600;-119.284500,46.285700;-119.160000,46.254000")
	assert.Contains(t, *gotPath, "source=first")
	assert.Contains(t, *gotPath, "roundtrip=true")
}

func TestTripRoundtripFalse(t *testing.T) {
	c, gotPath := newStubRouter(t, http.StatusOK, prodTripResponse)

	_, err := c.Trip(context.Background(),
		[]routing.Coordinate{roastery, pasco, richland, middle},
		routing.TripOptions{Roundtrip: false})
	require.NoError(t, err)
	assert.Contains(t, *gotPath, "roundtrip=false")
}

// A malformed permutation would silently drop a stop from the route — a missed
// delivery nobody notices until the customer calls.
func TestTripRejectsMalformedWaypointOrder(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "duplicate index",
			body: `{"code":"Ok","trips":[{"duration":1,"distance":1}],
			        "waypoints":[{"waypoint_index":0},{"waypoint_index":0}]}`,
		},
		{
			name: "index out of range",
			body: `{"code":"Ok","trips":[{"duration":1,"distance":1}],
			        "waypoints":[{"waypoint_index":0},{"waypoint_index":7}]}`,
		},
		{
			name: "negative index",
			body: `{"code":"Ok","trips":[{"duration":1,"distance":1}],
			        "waypoints":[{"waypoint_index":0},{"waypoint_index":-1}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newStubRouter(t, http.StatusOK, tt.body)
			_, err := c.Trip(context.Background(),
				[]routing.Coordinate{roastery, pasco}, routing.TripOptions{Roundtrip: true})
			require.Error(t, err)
		})
	}
}

// A waypoint count that doesn't match the input means we cannot map results
// back onto orders at all.
func TestTripRejectsWaypointCountMismatch(t *testing.T) {
	c, _ := newStubRouter(t, http.StatusOK,
		`{"code":"Ok","trips":[{"duration":1,"distance":1}],"waypoints":[{"waypoint_index":0}]}`)

	_, err := c.Trip(context.Background(),
		[]routing.Coordinate{roastery, pasco}, routing.TripOptions{Roundtrip: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "waypoints")
}

func TestTripStatusMapping(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr error
	}{
		{"no route", 400, `{"code":"NoRoute","message":"Impossible route"}`, routing.ErrNoRoute},
		{"no trips", 400, `{"code":"NoTrips","message":"No trip visiting all destinations"}`, routing.ErrNoRoute},
		{"off network", 400, `{"code":"NoSegment","message":"Could not find a matching segment"}`, routing.ErrCoordinateOffNetwork},
		{"too big", 400, `{"code":"TooBig","message":"Too many trip coordinates"}`, routing.ErrTooManyStops},
		{"server error", 502, `<html>bad gateway</html>`, routing.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newStubRouter(t, tt.status, tt.body)
			_, err := c.Trip(context.Background(),
				[]routing.Coordinate{roastery, pasco}, routing.TripOptions{Roundtrip: true})
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

// A deployment fault (wrong profile, bad URL) must not look transient, or the
// caller will retry forever against a router that will never answer.
func TestTripConfigurationFaultsAreNotTransient(t *testing.T) {
	c, _ := newStubRouter(t, http.StatusBadRequest,
		`{"code":"ProfileNotFound","message":"ProfileNotFound"}`)

	_, err := c.Trip(context.Background(),
		[]routing.Coordinate{roastery, pasco}, routing.TripOptions{Roundtrip: true})
	require.Error(t, err)
	assert.NotErrorIs(t, err, routing.ErrUnavailable)
	assert.NotErrorIs(t, err, routing.ErrNoRoute)
}

func TestTripUnreachableRouterIsTransient(t *testing.T) {
	c := routing.NewClient("http://192.0.2.1:1")
	c.HTTP = &http.Client{Timeout: 100 * time.Millisecond}

	_, err := c.Trip(context.Background(),
		[]routing.Coordinate{roastery, pasco}, routing.TripOptions{Roundtrip: true})
	assert.ErrorIs(t, err, routing.ErrUnavailable)
}

func TestTripDisabledWithoutBaseURL(t *testing.T) {
	c := routing.NewClient("")
	assert.False(t, c.Enabled())

	_, err := c.Trip(context.Background(),
		[]routing.Coordinate{roastery, pasco}, routing.TripOptions{Roundtrip: true})
	assert.ErrorIs(t, err, routing.ErrNotConfigured)
}

func TestTripCoordinateCountGuards(t *testing.T) {
	c, _ := newStubRouter(t, http.StatusOK, prodTripResponse)

	_, err := c.Trip(context.Background(), []routing.Coordinate{roastery}, routing.TripOptions{})
	assert.ErrorIs(t, err, routing.ErrTooFewStops)

	// 101 coordinates: one past the router's max-table-size, caught before
	// the request leaves rather than coming back as a confusing 400.
	many := make([]routing.Coordinate, 101)
	for i := range many {
		many[i] = roastery
	}
	_, err = c.Trip(context.Background(), many, routing.TripOptions{})
	assert.ErrorIs(t, err, routing.ErrTooManyStops)
}

func TestTripMalformedJSON(t *testing.T) {
	c, _ := newStubRouter(t, http.StatusOK, `{"code":"Ok","trips":[`)
	_, err := c.Trip(context.Background(),
		[]routing.Coordinate{roastery, pasco}, routing.TripOptions{})
	require.Error(t, err)
	assert.NotErrorIs(t, err, routing.ErrNoRoute)
}

// Integration test against a real OSRM. Skipped unless OSRM_TEST_URL is set,
// since the router is not published publicly and there is no local instance:
//
//	# from the prod host, or through an SSH tunnel to it
//	OSRM_TEST_URL=http://localhost:5000 go test ./internal/platform/routing/ -run Live -v
//
// The expectations mirror the known-good baseline in ops/osrm/README.md.
func TestTripAgainstLiveRouter(t *testing.T) {
	baseURL := os.Getenv("OSRM_TEST_URL")
	if baseURL == "" {
		t.Skip("set OSRM_TEST_URL to run against a live OSRM instance")
	}
	c := routing.NewClient(baseURL)

	res, err := c.Trip(context.Background(),
		[]routing.Coordinate{roastery, pasco, richland, middle},
		routing.TripOptions{Roundtrip: true})
	require.NoError(t, err)

	// A real optimizer must not simply echo the input order for these four
	// coordinates — the optimal loop genuinely reorders them.
	assert.Equal(t, 0, res.Order[0], "source=first must pin the roastery")
	assert.NotEqual(t, []int{0, 1, 2, 3}, res.Order, "input order is not the optimal loop here")
	assert.Len(t, res.Order, 4)

	// Tri-Cities loop: tens of minutes and tens of kilometres, not zero and
	// not a continental crossing.
	assert.Greater(t, res.DurationSeconds, 600.0)
	assert.Less(t, res.DurationSeconds, 10800.0)
	assert.Greater(t, res.DistanceMeters, 5000.0)
	assert.Less(t, res.DistanceMeters, 200000.0)
	t.Logf("live trip: order=%v duration=%.0fs distance=%.0fm", res.Order, res.DurationSeconds, res.DistanceMeters)
}

// Two stops that are the same place is legal and should not error — two orders
// to one office is an ordinary delivery day.
func TestTripAcceptsDuplicateCoordinates(t *testing.T) {
	c, _ := newStubRouter(t, http.StatusOK,
		`{"code":"Ok","trips":[{"duration":10,"distance":20}],
		  "waypoints":[{"waypoint_index":0},{"waypoint_index":1},{"waypoint_index":2}]}`)

	res, err := c.Trip(context.Background(),
		[]routing.Coordinate{roastery, pasco, pasco}, routing.TripOptions{Roundtrip: true})
	require.NoError(t, err)
	assert.Equal(t, []int{0, 1, 2}, res.Order)
}
