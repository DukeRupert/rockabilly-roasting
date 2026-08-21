// Package routing orders delivery stops using a self-hosted OSRM instance.
//
// OSRM answers one question for us: given a set of coordinates, what order
// should a driver visit them in? It never talks to Google or Apple Maps — the
// driver's phone does turn-by-turn navigation from URL schemes, receiving the
// stops in the order decided here.
//
// Deployment lives in ops/osrm/README.md. The router has no authentication and
// is reachable only on the internal Docker network.
package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Coordinate is a WGS84 point in the order humans and maps URLs use: lat, lng.
//
// OSRM's own API takes lng,lat — the reverse — which is the single easiest
// mistake to make against this service and produces a route through Kazakhstan
// rather than an error. The conversion happens once, in coordinatePath, and
// nowhere else.
type Coordinate struct {
	Lat float64
	Lng float64
}

// TripResult is the outcome of an optimized trip.
type TripResult struct {
	// Order lists input indices in visiting order: Order[0] is the index of
	// the coordinate visited first. It is always a permutation of the inputs.
	//
	// This is deliberately NOT what OSRM returns. OSRM gives each waypoint its
	// position in the trip (waypoint_index); callers want the inverse — the
	// stop to visit next — and inverting it once here means every caller
	// doesn't have to get it right.
	Order []int
	// DurationSeconds and DistanceMeters cover the whole trip, including the
	// return leg when roundtrip is set.
	DurationSeconds float64
	DistanceMeters  float64
}

var (
	// ErrNotConfigured — no base URL. Route planning is unavailable rather
	// than silently returning stops in input order, which would look like a
	// working feature while sending drivers on an unoptimized route.
	ErrNotConfigured = errors.New("routing: OSRM base URL not configured")

	// ErrUnavailable — the router is unreachable or failing. Transient:
	// the same request may succeed once the container is back.
	ErrUnavailable = errors.New("routing: OSRM unavailable")

	// ErrNoRoute — OSRM has the coordinates but cannot connect them by road
	// (an address across water with no bridge in the extract, say).
	ErrNoRoute = errors.New("routing: no route between the given stops")

	// ErrCoordinateOffNetwork — a coordinate has no road segment near it,
	// which in practice means it is outside the regional extract.
	ErrCoordinateOffNetwork = errors.New("routing: coordinate is not on the road network")

	// ErrTooManyStops — more stops than the router's max-table-size allows.
	// See ops/osrm/README.md; the deployed limit is 100 locations.
	ErrTooManyStops = errors.New("routing: too many stops for one trip")

	// ErrTooFewStops — a trip needs an origin and at least one stop.
	ErrTooFewStops = errors.New("routing: a trip needs at least two coordinates")
)

// Client calls a self-hosted OSRM instance. A Client with an empty BaseURL is
// disabled and returns ErrNotConfigured from every call.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	// Profile is the OSRM routing profile the dataset was built with. The
	// deployed dataset uses the car profile; changing this without rebuilding
	// the data produces a 400 from the router.
	Profile string
}

// NewClient returns a Client with a 15-second timeout. Trip planning is an
// interactive admin action, so the timeout is generous enough for a cold
// mmap'd dataset to page in but short enough that a wedged router doesn't hang
// the request forever.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		HTTP:    &http.Client{Timeout: 15 * time.Second},
		Profile: "driving",
	}
}

// Enabled reports whether this client will actually call out.
func (c *Client) Enabled() bool {
	return c != nil && c.BaseURL != ""
}

// TripOptions controls how the trip is solved.
type TripOptions struct {
	// Roundtrip returns the driver to the first coordinate — the van goes back
	// to the roastery at the end of the run. Ignored when PinDestination is
	// set: a run that finishes at the driver's house does not also loop home.
	Roundtrip bool
	// PinDestination fixes the LAST coordinate as the end of the trip, the way
	// the first is always fixed as the origin. Set it when the caller appended
	// a finishing point that is not a delivery — the driver's house on a run
	// where the van doesn't come back to the shop.
	//
	// Without it OSRM is free to end wherever the solver likes, which for a
	// route that has to finish somewhere specific means an order that looks
	// optimal and isn't.
	PinDestination bool
}

// maxTripCoordinates mirrors --max-table-size on the deployed router. Exceeding
// it makes OSRM answer TooBig; checking here turns a confusing 400 into a clear
// error before the request leaves.
const maxTripCoordinates = 100

// osrmTripResponse is the subset of the /trip payload we read.
type osrmTripResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Waypoints []struct {
		WaypointIndex int     `json:"waypoint_index"`
		Name          string  `json:"name"`
		Distance      float64 `json:"distance"`
	} `json:"waypoints"`
	Trips []struct {
		Duration float64 `json:"duration"`
		Distance float64 `json:"distance"`
	} `json:"trips"`
}

// Trip returns the optimal visiting order for coords. The first coordinate is
// pinned as the origin (source=first) — the route starts at the roastery, not
// wherever the solver finds convenient. With TripOptions.PinDestination the
// last coordinate is fixed as the end in the same way.
//
// Must not be called inside a database transaction: it is a network call, and
// holding a pgx transaction across it is the pattern CLAUDE.md forbids.
func (c *Client) Trip(ctx context.Context, coords []Coordinate, opts TripOptions) (TripResult, error) {
	if !c.Enabled() {
		return TripResult{}, ErrNotConfigured
	}
	if len(coords) < 2 {
		return TripResult{}, fmt.Errorf("%w: got %d", ErrTooFewStops, len(coords))
	}
	if len(coords) > maxTripCoordinates {
		return TripResult{}, fmt.Errorf("%w: %d coordinates, limit %d", ErrTooManyStops, len(coords), maxTripCoordinates)
	}

	profile := c.Profile
	if profile == "" {
		profile = "driving"
	}
	// destination=last only means anything with roundtrip=false — OSRM rejects
	// the combination otherwise, and a pinned end is by definition not a loop.
	roundtrip := opts.Roundtrip && !opts.PinDestination
	url := fmt.Sprintf("%s/trip/v1/%s/%s?source=first&roundtrip=%t",
		c.BaseURL, profile, coordinatePath(coords), roundtrip)
	if opts.PinDestination {
		url += "&destination=last"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return TripResult{}, fmt.Errorf("routing: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return TripResult{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<22))
	if err != nil {
		return TripResult{}, fmt.Errorf("%w: read body: %v", ErrUnavailable, err)
	}
	// 5xx is the router itself failing; 4xx carries an OSRM code in the body
	// and is handled below.
	if resp.StatusCode >= 500 {
		return TripResult{}, fmt.Errorf("%w: http %d", ErrUnavailable, resp.StatusCode)
	}

	var parsed osrmTripResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return TripResult{}, fmt.Errorf("routing: decode response: %w", err)
	}

	switch parsed.Code {
	case "Ok":
	case "NoTrips", "NoRoute":
		return TripResult{}, fmt.Errorf("%w: %s", ErrNoRoute, parsed.Message)
	case "NoSegment":
		return TripResult{}, fmt.Errorf("%w: %s", ErrCoordinateOffNetwork, parsed.Message)
	case "TooBig":
		return TripResult{}, fmt.Errorf("%w: %s", ErrTooManyStops, parsed.Message)
	case "":
		return TripResult{}, fmt.Errorf("routing: response carried no status code (http %d)", resp.StatusCode)
	default:
		// InvalidUrl, InvalidQuery, InvalidValue, ProfileNotFound — all
		// caller/deployment faults, none of them worth retrying.
		return TripResult{}, fmt.Errorf("routing: %s: %s", parsed.Code, parsed.Message)
	}

	if len(parsed.Trips) == 0 {
		return TripResult{}, fmt.Errorf("%w: router returned no trip", ErrNoRoute)
	}
	if len(parsed.Waypoints) != len(coords) {
		return TripResult{}, fmt.Errorf("routing: router returned %d waypoints for %d coordinates",
			len(parsed.Waypoints), len(coords))
	}

	order, err := invertWaypointIndices(parsed)
	if err != nil {
		return TripResult{}, err
	}

	return TripResult{
		Order:           order,
		DurationSeconds: parsed.Trips[0].Duration,
		DistanceMeters:  parsed.Trips[0].Distance,
	}, nil
}

// invertWaypointIndices turns OSRM's per-waypoint position into a visiting
// order, validating that it really is a permutation on the way.
//
// The validation is not paranoia about OSRM so much as insurance for the
// driver: a duplicated or out-of-range index would silently drop a stop from
// the route, and a missed delivery that nobody notices until the customer calls
// is exactly the failure this feature exists to prevent.
func invertWaypointIndices(parsed osrmTripResponse) ([]int, error) {
	n := len(parsed.Waypoints)
	order := make([]int, n)
	seen := make([]bool, n)
	for inputIdx, w := range parsed.Waypoints {
		pos := w.WaypointIndex
		if pos < 0 || pos >= n {
			return nil, fmt.Errorf("routing: waypoint_index %d out of range for %d stops", pos, n)
		}
		if seen[pos] {
			return nil, fmt.Errorf("routing: duplicate waypoint_index %d — stop order is not a permutation", pos)
		}
		seen[pos] = true
		order[pos] = inputIdx
	}
	return order, nil
}

// coordinatePath renders coordinates as OSRM's semicolon-separated
// lng,lat list. This is the ONLY place the axis order flips.
//
// Six decimal places is about 11cm — far finer than any street address needs,
// and short enough to keep a 100-stop URL manageable. 'f' format matters:
// scientific notation ("1.19e+02") is not something OSRM parses.
func coordinatePath(coords []Coordinate) string {
	var b strings.Builder
	for i, c := range coords {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(strconv.FormatFloat(c.Lng, 'f', 6, 64))
		b.WriteByte(',')
		b.WriteString(strconv.FormatFloat(c.Lat, 'f', 6, 64))
	}
	return b.String()
}
