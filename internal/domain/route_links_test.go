package domain_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// stopsAt builds n pending stops with distinguishable coordinates.
func stopsAt(n int) []domain.RouteStop {
	out := make([]domain.RouteStop, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, domain.RouteStop{
			Position: i,
			Lat:      46.0 + float64(i)/1000,
			Lng:      -119.0 - float64(i)/1000,
			Status:   domain.RouteStopPending,
		})
	}
	return out
}

// The chunk math is the whole reason this code exists: a link with more than
// ten stops is silently truncated by the maps app, which on a delivery route
// means stops vanishing from the driver's navigation.
func TestGoogleMapsNavChunkSizes(t *testing.T) {
	tests := []struct {
		stops      int
		wantChunks []int // stops per chunk
	}{
		{1, []int{1}},
		{9, []int{9}},
		{10, []int{10}},
		{11, []int{10, 1}},
		{20, []int{10, 10}},
		{21, []int{10, 10, 1}},
		{45, []int{10, 10, 10, 10, 5}},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d stops", tt.stops), func(t *testing.T) {
			chunks := domain.GoogleMapsNavChunks(stopsAt(tt.stops))
			require.Len(t, chunks, len(tt.wantChunks))

			total := 0
			for i, c := range chunks {
				assert.Equal(t, tt.wantChunks[i], c.Count, "chunk %d size", i)
				total += c.Count
			}
			assert.Equal(t, tt.stops, total, "every stop must appear in exactly one chunk")
		})
	}
}

// Chunks must tile the route: contiguous, in order, no gaps, no repeats.
func TestGoogleMapsNavChunksAreContiguous(t *testing.T) {
	chunks := domain.GoogleMapsNavChunks(stopsAt(25))
	require.Len(t, chunks, 3)

	assert.Equal(t, 1, chunks[0].FromPosition)
	assert.Equal(t, 10, chunks[0].ToPosition)
	assert.Equal(t, 11, chunks[1].FromPosition)
	assert.Equal(t, 20, chunks[1].ToPosition)
	assert.Equal(t, 21, chunks[2].FromPosition)
	assert.Equal(t, 25, chunks[2].ToPosition)
}

// A URL carries its last stop as the destination and the rest as waypoints —
// nine waypoints max, which is the actual Google limit.
func TestGoogleMapsNavChunkURLShape(t *testing.T) {
	chunks := domain.GoogleMapsNavChunks(stopsAt(10))
	require.Len(t, chunks, 1)
	u := chunks[0].URL

	assert.Contains(t, u, "https://www.google.com/maps/dir/?api=1")
	assert.Contains(t, u, "travelmode=driving")
	// Destination is the tenth stop.
	assert.Contains(t, u, "destination=46.010000,-119.010000")
	// Nine waypoints => eight separators.
	assert.Equal(t, 8, strings.Count(u, "%7C"))
	// The first stop leads the waypoint list, preserving OSRM's order.
	assert.Contains(t, u, "waypoints=46.001000,-119.001000%7C")
	// No origin: Google starts from wherever the driver is.
	assert.NotContains(t, u, "origin=")
}

func TestGoogleMapsNavSingleStopHasNoWaypoints(t *testing.T) {
	chunks := domain.GoogleMapsNavChunks(stopsAt(1))
	require.Len(t, chunks, 1)
	assert.NotContains(t, chunks[0].URL, "waypoints=")
	assert.Contains(t, chunks[0].URL, "destination=46.001000,-119.001000")
}

// A driver halfway through a run taps this to navigate what is left. Including
// delivered stops would route them back past houses they already visited.
func TestGoogleMapsNavChunksSkipResolvedStops(t *testing.T) {
	stops := stopsAt(5)
	stops[0].Status = domain.RouteStopDelivered
	stops[1].Status = domain.RouteStopSkipped
	stops[3].Status = domain.RouteStopDelivered

	chunks := domain.GoogleMapsNavChunks(stops)
	require.Len(t, chunks, 1)
	assert.Equal(t, 2, chunks[0].Count)
	// Positions 3 and 5 are what's left.
	assert.Equal(t, 3, chunks[0].FromPosition)
	assert.Equal(t, 5, chunks[0].ToPosition)
	assert.Contains(t, chunks[0].URL, "destination=46.005000,-119.005000")
	assert.NotContains(t, chunks[0].URL, "46.001000")
}

func TestGoogleMapsNavChunksEmptyWhenNothingPending(t *testing.T) {
	stops := stopsAt(3)
	for i := range stops {
		stops[i].Status = domain.RouteStopDelivered
	}
	assert.Empty(t, domain.GoogleMapsNavChunks(stops))
	assert.Empty(t, domain.GoogleMapsNavChunks(nil))
}

func TestNavChunkLabel(t *testing.T) {
	assert.Equal(t, "Navigate stops 1–10",
		domain.NavChunk{FromPosition: 1, ToPosition: 10, Count: 10}.Label())
	assert.Equal(t, "Navigate stops 11–18",
		domain.NavChunk{FromPosition: 11, ToPosition: 18, Count: 8}.Label())
	// One stop reads naturally rather than "stops 7–7".
	assert.Equal(t, "Navigate stop 7",
		domain.NavChunk{FromPosition: 7, ToPosition: 7, Count: 1}.Label())
}

func TestSingleStopURLs(t *testing.T) {
	s := domain.RouteStop{Lat: 46.2087, Lng: -119.1372}
	assert.Equal(t,
		"https://www.google.com/maps/dir/?api=1&travelmode=driving&destination=46.208700,-119.137200",
		domain.GoogleMapsStopURL(s))
	assert.Equal(t,
		"https://maps.apple.com/?dirflg=d&daddr=46.208700,-119.137200",
		domain.AppleMapsStopURL(s))
}

// Coordinates must never render in scientific notation — the maps apps don't
// parse it, and a stop near the prime meridian or equator is where that bites.
func TestCoordinateFormattingNeverScientific(t *testing.T) {
	s := domain.RouteStop{Lat: 0.0000123, Lng: -0.0000456}
	u := domain.GoogleMapsStopURL(s)
	assert.NotContains(t, u, "e-")
	assert.Contains(t, u, "destination=0.000012,-0.000046")
}
