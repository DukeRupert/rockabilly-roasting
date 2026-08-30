package web

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ParseFloat's idea of a valid number is wider than a money field's. "Inf" and
// "NaN" both parse, both slip past a `< 0` check, and both convert to a large
// negative int — so a rate field would accept them and store nonsense.
func TestParseOptionalRateRejectsNonNumbers(t *testing.T) {
	for _, raw := range []string{"Inf", "+Inf", "-Inf", "inf", "NaN", "nan", "$Inf", "1e400"} {
		t.Run(raw, func(t *testing.T) {
			got, err := parseOptionalRate(raw)
			assert.Error(t, err, "parsed %q as a rate", raw)
			assert.Nil(t, got)
		})
	}
}

// The ordinary shapes still work, including the ones somebody pastes off an
// invoice.
func TestParseOptionalRateAcceptsMoney(t *testing.T) {
	cases := map[string]*int{
		"":       nil,
		"   ":    nil,
		"95":     ptrTo(9500),
		"95.50":  ptrTo(9550),
		"$95.50": ptrTo(9550),
		" 95.5 ": ptrTo(9550),
		"0":      ptrTo(0),
		"0.00":   ptrTo(0),
	}
	for raw, want := range cases {
		t.Run(raw, func(t *testing.T) {
			got, err := parseOptionalRate(raw)
			require.NoError(t, err)
			if want == nil {
				assert.Nil(t, got, "blank means unset, not zero")
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, *want, *got)
		})
	}
}
