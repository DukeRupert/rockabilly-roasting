package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// GeocodeConfidence is a geocoding provider's own assessment of how precisely
// it located an address. Values are stored provider-native (Google's
// location_type) rather than collapsed to a boolean, so a later provider swap
// can reinterpret history without re-geocoding it.
type GeocodeConfidence string

const (
	// GeocodeRooftop — the provider has the actual building. Trustworthy.
	GeocodeRooftop GeocodeConfidence = "ROOFTOP"
	// GeocodeRangeInterpolated — interpolated between two known house numbers
	// on the block. Off by a house at worst; fine for a driver.
	GeocodeRangeInterpolated GeocodeConfidence = "RANGE_INTERPOLATED"
	// GeocodeGeometricCenter — the center of a street or parcel. A driver sent
	// here lands on the right road, wrong end of it.
	GeocodeGeometricCenter GeocodeConfidence = "GEOMETRIC_CENTER"
	// GeocodeApproximate — a city or zip centroid. Useless as a delivery stop.
	GeocodeApproximate GeocodeConfidence = "APPROXIMATE"
)

// Precise reports whether a result is good enough to route a driver to without
// staff review. The line sits below RANGE_INTERPOLATED: a house-number
// interpolation puts the van within a door or two, while a geometric center can
// be a quarter mile off — that's a delivery attempt at the wrong building.
//
// Anything not Precise is surfaced to admin at route-plan time rather than
// silently routed (see the low-confidence report).
func (c GeocodeConfidence) Precise() bool {
	return c == GeocodeRooftop || c == GeocodeRangeInterpolated
}

// GeocodedAddress is one cached address→coordinate resolution.
type GeocodedAddress struct {
	ID uuid.UUID
	// NormalizedAddress is the cache key — the output of NormalizeAddress.
	NormalizedAddress string
	// RawAddress is the address as last seen on an order, kept legible for
	// staff reviewing low-confidence results.
	RawAddress string
	Lat        float64
	Lng        float64
	Provider   string
	Confidence GeocodeConfidence
	GeocodedAt time.Time
}

// Point returns the coordinate pair. Route code passes these to OSRM and into
// maps deep links; note that OSRM takes lng,lat while every URL scheme takes
// lat,lng, so callers should convert at the boundary rather than storing
// either order as "the" order.
func (g GeocodedAddress) Point() (lat, lng float64) {
	return g.Lat, g.Lng
}

// addressAbbreviations maps the long forms of street suffixes, directionals,
// and unit designators onto one canonical short form each.
//
// Only unambiguous expansions are listed. Notably absent: "st" → "saint".
// "St Andrews Ave" and "Street Andrews Ave" would collapse together, which is
// the kind of clever normalization that merges two real addresses into one
// cache row and sends a driver to the wrong house.
var addressAbbreviations = map[string]string{
	// Street suffixes (USPS Publication 28 canonical forms).
	"street": "st", "str": "st",
	"avenue": "ave", "av": "ave", "aven": "ave",
	"road":      "rd",
	"drive":     "dr",
	"lane":      "ln",
	"court":     "ct",
	"circle":    "cir",
	"boulevard": "blvd", "boul": "blvd",
	"place":    "pl",
	"terrace":  "ter",
	"parkway":  "pkwy",
	"highway":  "hwy",
	"way":      "way",
	"loop":     "loop",
	"trail":    "trl",
	"square":   "sq",
	"crossing": "xing",

	// Directionals. Bare "n"/"s"/"e"/"w" already canonical.
	"north": "n", "south": "s", "east": "e", "west": "w",
	"northeast": "ne", "northwest": "nw",
	"southeast": "se", "southwest": "sw",

	// Unit designators all collapse to "#", so "Apt 2", "Unit 2", "#2" and
	// "Suite 2" are one address. The unit NUMBER is always preserved — that is
	// the part that distinguishes two real delivery stops in one building.
	"apartment": "#", "apt": "#", "unit": "#",
	"suite": "#", "ste": "#",
	"building": "#", "bldg": "#",

	// State name → USPS code. Only the states the delivery radius can reach;
	// a national list here would be dead weight.
	"washington": "wa",
	"oregon":     "or",
	"idaho":      "id",
}

// countryTokens are dropped entirely — a domestic-only delivery operation
// gains nothing by keying the cache on "usa" being present or absent.
var countryTokens = map[string]bool{
	"usa": true, "us": true, "united": true, "states": true, "america": true,
}

// NormalizeAddress canonicalizes a street address for use as a cache key.
//
// The cache is keyed on exact string equality, so every spelling of one address
// must reduce to one form or the same house gets geocoded (and billed) again on
// every variation a customer types. "1234 West 4th Avenue, Kennewick, WA 99336"
// and "1234 W 4th Ave Kennewick WA 99336" are the same delivery stop.
//
// The rules, in order: lowercase, strip punctuation, collapse whitespace, drop
// country tokens, truncate ZIP+4 to five digits, and map each remaining token
// through addressAbbreviations.
//
// This is deliberately conservative. Over-normalizing is the dangerous
// direction — two distinct addresses collapsing into one cache row sends a
// driver to the wrong house, while under-normalizing merely costs a few cents
// in duplicate lookups. When in doubt, leave the token alone.
func NormalizeAddress(addr string) string {
	s := strings.ToLower(strings.TrimSpace(addr))

	// Punctuation carries no address meaning, but "#" does — it is the unit
	// marker every other designator collapses onto — so it is spaced out into
	// its own token rather than stripped, turning "#2" into "# 2".
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch {
		case r == '#':
			b.WriteString(" # ")
		case r == ',' || r == '.' || r == ';' || r == ':' || r == '"' || r == '\'':
			b.WriteRune(' ')
		case r == '-':
			// Hyphens join things that should stay joined (ZIP+4, hyphenated
			// street names), so keep them for now; the ZIP pass below is what
			// deals with "99336-1234".
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}

	fields := strings.Fields(b.String())
	out := make([]string, 0, len(fields))
	for _, tok := range fields {
		if countryTokens[tok] {
			continue
		}
		tok = truncateZIP(tok)
		if mapped, ok := addressAbbreviations[tok]; ok {
			tok = mapped
		}
		out = append(out, tok)
	}

	// Two unit markers in a row ("apt #2" → "# # 2") collapse to one.
	deduped := make([]string, 0, len(out))
	for i, tok := range out {
		if tok == "#" && i > 0 && out[i-1] == "#" {
			continue
		}
		deduped = append(deduped, tok)
	}

	return strings.Join(deduped, " ")
}

// truncateZIP reduces a ZIP+4 to its five-digit base. The +4 varies by carrier
// route and is frequently absent, so keying on it would split one address
// across two cache rows.
func truncateZIP(tok string) string {
	base, plus, found := strings.Cut(tok, "-")
	if !found || len(base) != 5 || len(plus) != 4 {
		return tok
	}
	if !allDigits(base) || !allDigits(plus) {
		return tok
	}
	return base
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// FormatAddressForGeocoding renders a stored Address as the single-line string
// a geocoding provider expects. Line2 is included — an apartment number is
// exactly the detail that separates two stops in one building — and the country
// is omitted, since the provider call is already constrained to the US.
func FormatAddressForGeocoding(a Address) string {
	parts := make([]string, 0, 5)
	if a.Line1 != "" {
		parts = append(parts, a.Line1)
	}
	if a.Line2 != nil && strings.TrimSpace(*a.Line2) != "" {
		parts = append(parts, strings.TrimSpace(*a.Line2))
	}
	if a.City != "" {
		parts = append(parts, a.City)
	}
	stateZip := strings.TrimSpace(a.State + " " + a.PostalCode)
	if stateZip != "" {
		parts = append(parts, stateZip)
	}
	return strings.Join(parts, ", ")
}
