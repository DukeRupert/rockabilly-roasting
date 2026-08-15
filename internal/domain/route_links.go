package domain

import (
	"fmt"
	"strings"
)

// maxGoogleMapsStops is how many stops fit in one Google Maps directions URL:
// nine waypoints plus a destination. Past that the link is silently truncated
// by the maps app, which on a delivery route means stops quietly disappearing
// from the driver's navigation — so routes longer than this are split into
// sequential links instead.
const maxGoogleMapsStops = 10

// NavChunk is one "navigate these next N stops" link. A route with more stops
// than a single maps URL can carry becomes several of these, worked through in
// order.
type NavChunk struct {
	// FromPosition and ToPosition are the 1-based stop positions this chunk
	// covers, inclusive — the numbers shown on the driver's list.
	FromPosition int
	ToPosition   int
	// Count is how many stops are in the chunk.
	Count int
	// URL is the Google Maps directions link.
	URL string
}

// Label is what the button says: "Navigate stops 1–10".
func (c NavChunk) Label() string {
	if c.Count == 1 {
		return fmt.Sprintf("Navigate stop %d", c.FromPosition)
	}
	return fmt.Sprintf("Navigate stops %d–%d", c.FromPosition, c.ToPosition)
}

// GoogleMapsNavChunks builds sequential multi-stop navigation links over the
// stops that still need visiting.
//
// Resolved stops are excluded: a driver halfway through a run taps this to
// navigate what is left, and re-including delivered stops would route them back
// past houses they have already visited.
//
// No origin is set on any chunk, so Google starts from the driver's current
// location. This is a deliberate departure from pinning the roastery as the
// origin: the button gets tapped both at the start of the run and midway
// through it, and Current Location is correct in both cases, whereas a pinned
// roastery would send a driver who is already out backwards across town.
//
// Coordinates only — never address text. An address handed to the maps app is
// re-geocoded there and can land on a different pin than the one the route was
// planned around.
func GoogleMapsNavChunks(stops []RouteStop) []NavChunk {
	pending := make([]RouteStop, 0, len(stops))
	for _, s := range stops {
		if !s.Status.Resolved() {
			pending = append(pending, s)
		}
	}
	if len(pending) == 0 {
		return nil
	}

	chunks := make([]NavChunk, 0, (len(pending)+maxGoogleMapsStops-1)/maxGoogleMapsStops)
	for start := 0; start < len(pending); start += maxGoogleMapsStops {
		end := start + maxGoogleMapsStops
		if end > len(pending) {
			end = len(pending)
		}
		group := pending[start:end]
		chunks = append(chunks, NavChunk{
			FromPosition: group[0].Position,
			ToPosition:   group[len(group)-1].Position,
			Count:        len(group),
			URL:          googleMapsDirectionsURL(group),
		})
	}
	return chunks
}

// googleMapsDirectionsURL renders one group of stops as a directions link.
// The last stop is the destination; everything before it rides as waypoints,
// pipe-separated, in order. Google preserves the given waypoint order — it does
// not re-optimize — which is the whole point: OSRM already decided the order.
func googleMapsDirectionsURL(stops []RouteStop) string {
	if len(stops) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("https://www.google.com/maps/dir/?api=1&travelmode=driving")

	dest := stops[len(stops)-1]
	b.WriteString("&destination=")
	b.WriteString(formatCoord(dest.Lat, dest.Lng))

	if len(stops) > 1 {
		b.WriteString("&waypoints=")
		for i, s := range stops[:len(stops)-1] {
			if i > 0 {
				// %7C is a pipe. Encoded rather than literal so the URL
				// survives being copied through anything that validates it.
				b.WriteString("%7C")
			}
			b.WriteString(formatCoord(s.Lat, s.Lng))
		}
	}
	return b.String()
}

// GoogleMapsStopURL is the single-stop navigation link for one delivery.
func GoogleMapsStopURL(s RouteStop) string {
	return "https://www.google.com/maps/dir/?api=1&travelmode=driving&destination=" + formatCoord(s.Lat, s.Lng)
}

// AppleMapsStopURL is the Apple Maps equivalent. Apple has no multi-stop URL
// scheme at all, so per-stop navigation is the most it can offer — which is why
// the "navigate everything left" affordance is Google-only.
func AppleMapsStopURL(s RouteStop) string {
	return "https://maps.apple.com/?dirflg=d&daddr=" + formatCoord(s.Lat, s.Lng)
}

// formatCoord renders lat,lng at six decimals — about 11cm, far finer than any
// doorstep needs. 'f' format matters: scientific notation is not something the
// maps apps parse.
func formatCoord(lat, lng float64) string {
	return fmt.Sprintf("%.6f,%.6f", lat, lng)
}
