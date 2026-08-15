package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

func TestNormalizeAddress(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"already canonical", "1234 w 4th ave kennewick wa 99336", "1234 w 4th ave kennewick wa 99336"},
		{"uppercase", "1234 W 4th Ave Kennewick WA 99336", "1234 w 4th ave kennewick wa 99336"},
		{"commas stripped", "1234 W 4th Ave, Kennewick, WA 99336", "1234 w 4th ave kennewick wa 99336"},
		{"ragged whitespace", "  1234   W  4th   Ave\tKennewick WA 99336 ", "1234 w 4th ave kennewick wa 99336"},
		{"long street suffix", "1234 West 4th Avenue, Kennewick, Washington 99336", "1234 w 4th ave kennewick wa 99336"},
		{"abbreviations with periods", "1234 W. 4th Ave., Kennewick, WA 99336", "1234 w 4th ave kennewick wa 99336"},
		{"zip plus four", "1234 W 4th Ave Kennewick WA 99336-1234", "1234 w 4th ave kennewick wa 99336"},
		{"country dropped", "1234 W 4th Ave, Kennewick, WA 99336, USA", "1234 w 4th ave kennewick wa 99336"},
		{"united states dropped", "1234 W 4th Ave, Kennewick, WA 99336, United States", "1234 w 4th ave kennewick wa 99336"},
		{"street suffix rd", "500 Road 68, Pasco, WA 99301", "500 rd 68 pasco wa 99301"},
		{"directional expanded", "77 Northeast Blvd, Richland, WA 99352", "77 ne blvd richland wa 99352"},
		{"boulevard expanded", "77 NE Boulevard, Richland, WA 99352", "77 ne blvd richland wa 99352"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, domain.NormalizeAddress(tt.in))
		})
	}
}

// Every spelling of a unit designator has to land on one key, or the same
// apartment is geocoded (and billed) once per spelling.
func TestNormalizeAddressUnitDesignators(t *testing.T) {
	want := "1234 w 4th ave # 2 kennewick wa 99336"
	for _, in := range []string{
		"1234 W 4th Ave Apt 2, Kennewick, WA 99336",
		"1234 W 4th Ave Apartment 2, Kennewick, WA 99336",
		"1234 W 4th Ave Unit 2, Kennewick, WA 99336",
		"1234 W 4th Ave Suite 2, Kennewick, WA 99336",
		"1234 W 4th Ave Ste 2, Kennewick, WA 99336",
		"1234 W 4th Ave #2, Kennewick, WA 99336",
		"1234 W 4th Ave # 2, Kennewick, WA 99336",
		"1234 W 4th Ave Apt #2, Kennewick, WA 99336",
	} {
		assert.Equal(t, want, domain.NormalizeAddress(in), "input: %s", in)
	}
}

// The failure that matters: two real delivery stops must never share a cache
// row. A collapsed key sends the driver to the wrong door — far worse than the
// few cents a duplicate lookup costs.
func TestNormalizeAddressKeepsDistinctAddressesDistinct(t *testing.T) {
	distinct := []string{
		"1234 W 4th Ave, Kennewick, WA 99336",
		"1234 W 4th Ave Apt 2, Kennewick, WA 99336",
		"1234 W 4th Ave Apt 3, Kennewick, WA 99336",
		"1234 E 4th Ave, Kennewick, WA 99336",
		"1235 W 4th Ave, Kennewick, WA 99336",
		"1234 W 4th St, Kennewick, WA 99336",
		"1234 W 4th Ave, Pasco, WA 99301",
	}
	seen := make(map[string]string, len(distinct))
	for _, addr := range distinct {
		key := domain.NormalizeAddress(addr)
		if prev, dup := seen[key]; dup {
			t.Errorf("distinct addresses collapsed to %q:\n  %s\n  %s", key, prev, addr)
		}
		seen[key] = addr
	}
}

// "St Andrews" must not become "Saint Andrews" or vice versa — the reason
// addressAbbreviations deliberately omits that expansion.
func TestNormalizeAddressLeavesAmbiguousTokensAlone(t *testing.T) {
	assert.Equal(t, "12 st andrews ct richland wa 99352",
		domain.NormalizeAddress("12 St Andrews Ct, Richland, WA 99352"))
}

func TestNormalizeAddressIsIdempotent(t *testing.T) {
	for _, in := range []string{
		"1234 West 4th Avenue, Apt 2, Kennewick, Washington 99336-1234",
		"500 Road 68, Pasco, WA 99301",
		"",
	} {
		once := domain.NormalizeAddress(in)
		assert.Equal(t, once, domain.NormalizeAddress(once), "input: %s", in)
	}
}

func TestGeocodeConfidencePrecise(t *testing.T) {
	assert.True(t, domain.GeocodeRooftop.Precise())
	assert.True(t, domain.GeocodeRangeInterpolated.Precise())
	assert.False(t, domain.GeocodeGeometricCenter.Precise())
	assert.False(t, domain.GeocodeApproximate.Precise())
	assert.False(t, domain.GeocodeConfidence("").Precise())
}

func TestFormatAddressForGeocoding(t *testing.T) {
	line2 := "Apt 2"
	blank := "   "
	tests := []struct {
		name string
		in   domain.Address
		want string
	}{
		{
			name: "full address",
			in:   domain.Address{Line1: "1234 W 4th Ave", City: "Kennewick", State: "WA", PostalCode: "99336"},
			want: "1234 W 4th Ave, Kennewick, WA 99336",
		},
		{
			name: "with unit",
			in:   domain.Address{Line1: "1234 W 4th Ave", Line2: &line2, City: "Kennewick", State: "WA", PostalCode: "99336"},
			want: "1234 W 4th Ave, Apt 2, Kennewick, WA 99336",
		},
		{
			name: "blank line2 omitted",
			in:   domain.Address{Line1: "1234 W 4th Ave", Line2: &blank, City: "Kennewick", State: "WA", PostalCode: "99336"},
			want: "1234 W 4th Ave, Kennewick, WA 99336",
		},
		{
			name: "missing zip",
			in:   domain.Address{Line1: "1234 W 4th Ave", City: "Kennewick", State: "WA"},
			want: "1234 W 4th Ave, Kennewick, WA",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, domain.FormatAddressForGeocoding(tt.in))
		})
	}
}
