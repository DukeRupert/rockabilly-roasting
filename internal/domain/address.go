package domain

import "strings"

// usStateCodes maps the unambiguous long form of every US state, the District
// of Columbia, and the inhabited territories onto its USPS two-letter code.
//
// Deliberately national, unlike addressAbbreviations in geocode.go, which
// carries three states on purpose. The two tables must never be merged: this
// one keys a customer's stored address book, which can hold any US address,
// while that one keys the delivery-route geocode cache, which only ever sees
// stops inside the delivery radius.
//
// Only full names are listed. Nicknames and abbreviations beyond the USPS code
// ("Cali", "Mass", "Wash") are absent because the ones that look safe are not:
// "Wash" is Washington to a Seattle customer and a street name elsewhere.
var usStateCodes = map[string]string{
	"alabama": "AL", "alaska": "AK", "arizona": "AZ", "arkansas": "AR",
	"california": "CA", "colorado": "CO", "connecticut": "CT", "delaware": "DE",
	"florida": "FL", "georgia": "GA", "hawaii": "HI", "idaho": "ID",
	"illinois": "IL", "indiana": "IN", "iowa": "IA", "kansas": "KS",
	"kentucky": "KY", "louisiana": "LA", "maine": "ME", "maryland": "MD",
	"massachusetts": "MA", "michigan": "MI", "minnesota": "MN",
	"mississippi": "MS", "missouri": "MO", "montana": "MT", "nebraska": "NE",
	"nevada": "NV", "new hampshire": "NH", "new jersey": "NJ",
	"new mexico": "NM", "new york": "NY", "north carolina": "NC",
	"north dakota": "ND", "ohio": "OH", "oklahoma": "OK", "oregon": "OR",
	"pennsylvania": "PA", "rhode island": "RI", "south carolina": "SC",
	"south dakota": "SD", "tennessee": "TN", "texas": "TX", "utah": "UT",
	"vermont": "VT", "virginia": "VA", "washington": "WA",
	"west virginia": "WV", "wisconsin": "WI", "wyoming": "WY",

	"district of columbia": "DC",

	"american samoa": "AS", "guam": "GU", "northern mariana islands": "MP",
	"puerto rico": "PR", "virgin islands": "VI",
	"u.s. virgin islands": "VI", "us virgin islands": "VI",
}

// NormalizeState canonicalizes a US state to its USPS two-letter code.
//
// "Idaho", "idaho", " ID ", and "id" all yield "ID". Matching is case- and
// space-insensitive, and internal runs of whitespace collapse so "new  york"
// resolves like "new york".
//
// An unrecognized value is returned trimmed and uppercased, NOT rejected:
// normalization is not validation. Turning a customer's typo into an error
// here would fail a checkout at the last step over a field the carrier would
// have accepted. Callers that need validation do it themselves.
func NormalizeState(s string) string {
	folded := foldSpace(strings.ToLower(s))
	if folded == "" {
		return ""
	}
	if code, ok := usStateCodes[folded]; ok {
		return code
	}
	return strings.ToUpper(folded)
}

// NormalizePostalCode canonicalizes a postal code for the given country.
//
// For the US it reduces a well-formed ZIP+4 to its five-digit base: the +4
// varies by carrier route and is frequently absent, so keying on it splits one
// house across two rows. "83201-6529" and "83201" both yield "83201".
//
// Truncation is strict — exactly five digits, a hyphen, exactly four digits.
// Anything else is returned trimmed and uppercased but structurally intact, so
// a non-US code ("SW1A 1AA") or a malformed entry survives unmangled. This is
// the same rule truncateZIP applies in geocode.go, which is a deliberate
// duplicate rather than a shared call — see that function's comment. The
// allDigits predicate IS shared: it is a character test with no tuning, not
// one of the tuned normalizers the two families keep apart.
//
// countryCode may be given in any case; only the US branch truncates.
func NormalizePostalCode(s, countryCode string) string {
	folded := foldSpace(s)
	if folded == "" {
		return ""
	}
	folded = strings.ToUpper(folded)
	if NormalizeCountryCode(countryCode) != "US" {
		return folded
	}
	base, plus, found := strings.Cut(folded, "-")
	if !found || len(base) != 5 || len(plus) != 4 {
		return folded
	}
	if !allDigits(base) || !allDigits(plus) {
		return folded
	}
	return base
}

// NormalizeCountryCode canonicalizes an ISO country code by trimming and
// uppercasing. It does not map country names onto codes: this is a
// single-country shop whose forms submit a code directly, and guessing at
// "United States" vs "United States of America" would be inventing a rule no
// caller has asked for.
func NormalizeCountryCode(s string) string {
	return strings.ToUpper(foldSpace(s))
}

// addressKeySep separates fields inside an address key. The ASCII unit
// separator cannot be typed into a web form, so no field value can forge a
// boundary and make two different addresses collide.
const addressKeySep = "\x1f"

// AddressKey returns the canonical identity of a destination: two addresses
// with equal keys are the same place, addressed to the same person.
//
// The key covers first name, last name, line1, line2, city, state, postal code,
// and country. It excludes ID, CustomerID, Company, and IsDefault — IsDefault
// especially, because siting a machine at an address a customer already has
// must never promote or demote that address.
//
// Names are included so a household or shared office does not collapse into one
// row with one occupant's name on the other's label. The cost is that
// "Jahnavi Lewis" and "J Lewis" stay separate, which is visible to staff rather
// than silent.
//
// AddressKey normalizes its argument; it does NOT assume the address is already
// canonical. That is load-bearing: addresses written before canonicalization
// shipped are still in the table, and matching a new order against a customer's
// legacy "Idaho / 83201-6529" row is the only reason dedup works at all without
// a backfill.
//
// This is a STORAGE identity, not a routing identity. It answers "is this a
// duplicate row in one customer's address book", never "is this the same
// delivery stop" — NormalizeAddress in geocode.go answers the second question
// and is the only function that may be used for routing or geocoding.
func AddressKey(a Address) string {
	line2 := ""
	if a.Line2 != nil {
		line2 = *a.Line2
	}
	country := NormalizeCountryCode(a.CountryCode)
	return strings.Join([]string{
		foldField(a.FirstName),
		foldField(a.LastName),
		foldField(a.Line1),
		foldField(line2),
		foldField(a.City),
		NormalizeState(a.State),
		NormalizePostalCode(a.PostalCode, country),
		country,
	}, addressKeySep)
}

// foldField reduces a free-text address field to its comparison form:
// lowercased, trimmed, with internal whitespace runs collapsed. Deliberately
// no punctuation stripping or suffix abbreviation — that is NormalizeAddress's
// job in geocode.go, tuned for a different risk, and pulling it in here would
// be the merge those two functions exist apart to prevent.
func foldField(s string) string {
	return foldSpace(strings.ToLower(s))
}

// foldSpace trims the ends and collapses every internal run of whitespace to a
// single space, so " 724  S  3rd " and "724 S 3rd" compare equal.
func foldSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
