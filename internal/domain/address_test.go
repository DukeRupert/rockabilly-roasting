package domain

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeState(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"full name", "Idaho", "ID"},
		{"full name lowercase", "idaho", "ID"},
		{"already a code", "ID", "ID"},
		{"code lowercase", "id", "ID"},
		{"code with space", " ID ", "ID"},
		{"multiword", "New York", "NY"},
		{"multiword collapsed space", "new  york", "NY"},
		{"district of columbia", "District of Columbia", "DC"},
		{"territory", "Puerto Rico", "PR"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},

		// Normalization is not validation: an unknown value is uppercased and
		// handed back, never rejected. A typo must not fail a checkout.
		{"unknown passes through", "Xanadu", "XANADU"},
		{"unknown multiword", "not a state", "NOT A STATE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NormalizeState(tt.in))
		})
	}
}

func TestNormalizeState_Idempotent(t *testing.T) {
	for in := range usStateCodes {
		once := NormalizeState(in)
		assert.Equal(t, once, NormalizeState(once), "input %q", in)
	}
}

func TestNormalizePostalCode(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		country string
		want    string
	}{
		{"five digit", "83201", "US", "83201"},
		{"zip+4 truncated", "83201-6529", "US", "83201"},
		{"zip+4 with space", " 83201-6529 ", "US", "83201"},
		{"lowercase country still truncates", "83201-6529", "us", "83201"},
		{"empty", "", "US", ""},

		// Strict truncation. Each of these has a hyphen but is not a
		// well-formed ZIP+4, so it survives intact rather than being cut at
		// the hyphen.
		{"too few base digits", "8320-6529", "US", "8320-6529"},
		{"too few plus digits", "83201-652", "US", "83201-652"},
		{"non-digit base", "8320A-6529", "US", "8320A-6529"},
		{"non-digit plus", "83201-65A9", "US", "83201-65A9"},
		{"double hyphen", "83201-6529-1", "US", "83201-6529-1"},

		// Non-US codes are folded but never truncated.
		{"uk postcode", "sw1a 1aa", "GB", "SW1A 1AA"},
		{"canadian with hyphen", "K1A-0B1", "CA", "K1A-0B1"},
		{"blank country is not US", "83201-6529", "", "83201-6529"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NormalizePostalCode(tt.in, tt.country))
		})
	}
}

// TestNormalizePostalCode_DivergesFromOldNormalizeZip pins the one behavioral
// difference between NormalizePostalCode and the normalizeZip helper it
// replaced in shipping.go. The old rule cut at the first hyphen
// unconditionally; the new one truncates only a well-formed ZIP+4. The
// existing shipping tests cover only well-formed input, so they pass either
// way and prove nothing about this case — hence this test.
//
// The divergence is safe for IsLocal because both sides of that comparison
// changed together, and because neither spelling of a malformed zip matches a
// configured local zone.
func TestNormalizePostalCode_DivergesFromOldNormalizeZip(t *testing.T) {
	oldNormalizeZip := func(zip string) string {
		z := strings.TrimSpace(zip)
		if i := strings.Index(z, "-"); i >= 0 {
			z = z[:i]
		}
		return z
	}

	assert.Equal(t, "8320", oldNormalizeZip("8320-6529"), "old rule cut at the hyphen")
	assert.Equal(t, "8320-6529", NormalizePostalCode("8320-6529", "US"), "new rule leaves it intact")

	// Well-formed input — where every existing caller actually lives — is
	// unchanged by the swap.
	for _, zip := range []string{"83201", "83201-6529", "99336-1234", "99352"} {
		assert.Equal(t, oldNormalizeZip(zip), NormalizePostalCode(zip, "US"), "zip %q", zip)
	}
}

func TestNormalizeCountryCode(t *testing.T) {
	assert.Equal(t, "US", NormalizeCountryCode("us"))
	assert.Equal(t, "US", NormalizeCountryCode(" US "))
	assert.Equal(t, "", NormalizeCountryCode(""))
	// No name-to-code guessing.
	assert.Equal(t, "UNITED STATES", NormalizeCountryCode("United States"))
}

// addr builds an Address for key comparisons. Only the fields AddressKey reads
// are set by callers; the rest stay zero.
func addr(first, last, line1, line2, city, state, postal, country string) Address {
	a := Address{
		FirstName:   first,
		LastName:    last,
		Line1:       line1,
		City:        city,
		State:       state,
		PostalCode:  postal,
		CountryCode: country,
	}
	if line2 != "" {
		a.Line2 = &line2
	}
	return a
}

// TestAddressKey_CollapsesReportedDuplicates uses the four rows that prompted
// this work: one customer, one house, four spellings.
func TestAddressKey_CollapsesReportedDuplicates(t *testing.T) {
	rows := []Address{
		addr("Jahnavi", "Lewis", "724 S 3rd Ave", "", "Pocatello", "ID", "83201", "US"),
		addr("Jahnavi", "Lewis", "724 S 3rd Ave", "", "Pocatello", "ID", "83201", "US"),
		addr("Jahnavi", "Lewis", "724 S 3rd Ave", "", "Pocatello", "Idaho", "83201", "US"),
		addr("Jahnavi", "Lewis", "724 S 3rd Ave", "", "Pocatello", "Idaho", "83201-6529", "US"),
	}
	want := AddressKey(rows[0])
	for i, r := range rows {
		assert.Equal(t, want, AddressKey(r), "row %d", i)
	}
}

// TestAddressKey_NormalizesItsInput is the guarantee that makes forward-only
// dedup work without a backfill: a legacy row stored before canonicalization
// shipped must key equal to a freshly normalized submission of the same place.
func TestAddressKey_NormalizesItsInput(t *testing.T) {
	legacy := addr("Jahnavi", "Lewis", "724 S 3rd Ave", "", "Pocatello", "Idaho", "83201-6529", "us")
	fresh := addr("Jahnavi", "Lewis", "724 S 3rd Ave", "", "Pocatello", "ID", "83201", "US")
	assert.Equal(t, AddressKey(legacy), AddressKey(fresh))
}

func TestAddressKey_IgnoresCaseAndSpacing(t *testing.T) {
	a := addr("Jahnavi", "Lewis", "724 S 3rd Ave", "Apt 2", "Pocatello", "ID", "83201", "US")
	b := addr("  JAHNAVI ", "lewis", "724  s  3rd   ave", "apt 2", " pocatello ", "id", " 83201 ", "us")
	assert.Equal(t, AddressKey(a), AddressKey(b))
}

func TestAddressKey_ExcludedFields(t *testing.T) {
	base := addr("Jahnavi", "Lewis", "724 S 3rd Ave", "", "Pocatello", "ID", "83201", "US")

	t.Run("IsDefault is excluded", func(t *testing.T) {
		other := base
		other.IsDefault = true
		assert.Equal(t, AddressKey(base), AddressKey(other),
			"siting equipment at an existing address must not turn on its default flag")
	})

	t.Run("ID and CustomerID are excluded", func(t *testing.T) {
		other := base
		id := uuid.New()
		cust := uuid.New()
		other.ID = id
		other.CustomerID = &cust
		assert.Equal(t, AddressKey(base), AddressKey(other))
	})

	t.Run("Company is excluded", func(t *testing.T) {
		other := base
		company := "Rockabilly Roasting Co."
		other.Company = &company
		assert.Equal(t, AddressKey(base), AddressKey(other))
	})
}

func TestAddressKey_Distinguishes(t *testing.T) {
	base := addr("Jahnavi", "Lewis", "724 S 3rd Ave", "", "Pocatello", "ID", "83201", "US")

	t.Run("different occupant at one address", func(t *testing.T) {
		other := addr("Marcus", "Lewis", "724 S 3rd Ave", "", "Pocatello", "ID", "83201", "US")
		assert.NotEqual(t, AddressKey(base), AddressKey(other),
			"a household must not collapse into one row with one occupant's name")
	})

	t.Run("different unit in one building", func(t *testing.T) {
		unit2 := addr("Jahnavi", "Lewis", "724 S 3rd Ave", "Apt 2", "Pocatello", "ID", "83201", "US")
		unit3 := addr("Jahnavi", "Lewis", "724 S 3rd Ave", "Apt 3", "Pocatello", "ID", "83201", "US")
		assert.NotEqual(t, AddressKey(unit2), AddressKey(unit3))
		assert.NotEqual(t, AddressKey(base), AddressKey(unit2))
	})

	t.Run("no abbreviation folding", func(t *testing.T) {
		// AddressKey deliberately does NOT know that "Avenue" is "Ave" — that
		// is NormalizeAddress's job in geocode.go, tuned for routing. Here the
		// two stay separate, which is visible to staff rather than silent.
		other := addr("Jahnavi", "Lewis", "724 S 3rd Avenue", "", "Pocatello", "ID", "83201", "US")
		assert.NotEqual(t, AddressKey(base), AddressKey(other))
	})

	t.Run("field boundaries cannot be forged", func(t *testing.T) {
		a := addr("Jahnavi", "Lewis", "724 S 3rd Ave", "", "Pocatello", "ID", "83201", "US")
		b := addr("Jahnavi Lewis", "", "724 S 3rd Ave", "", "Pocatello", "ID", "83201", "US")
		assert.NotEqual(t, AddressKey(a), AddressKey(b))
	})
}

func TestAddressKey_Idempotent(t *testing.T) {
	a := addr("Jahnavi", "Lewis", "724 S 3rd Ave", "Apt 2", "Pocatello", "Idaho", "83201-6529", "us")
	normalized := a
	normalized.State = NormalizeState(a.State)
	normalized.PostalCode = NormalizePostalCode(a.PostalCode, a.CountryCode)
	normalized.CountryCode = NormalizeCountryCode(a.CountryCode)
	assert.Equal(t, AddressKey(a), AddressKey(normalized),
		"keying a canonical address must equal keying its raw form")
}

// TestAddressKey_NilAndEmptyLine2 pins that an absent Line2 and an empty one
// are the same destination — checkout sends nil, some importers send "".
func TestAddressKey_NilAndEmptyLine2(t *testing.T) {
	nilLine2 := addr("Jahnavi", "Lewis", "724 S 3rd Ave", "", "Pocatello", "ID", "83201", "US")
	empty := ""
	emptyLine2 := nilLine2
	emptyLine2.Line2 = &empty
	blank := "   "
	blankLine2 := nilLine2
	blankLine2.Line2 = &blank

	assert.Equal(t, AddressKey(nilLine2), AddressKey(emptyLine2))
	assert.Equal(t, AddressKey(nilLine2), AddressKey(blankLine2))
}
