package web

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The blank cost is the case that broke.
//
// handleAdminServicePartAdd called the shared parseDollarsCents directly, under
// a comment promising that a blank cost meant zero. That helper returns an error
// for the empty string — correct for the settings screens it was written for,
// wrong here — so every part added without a price was silently dropped and the
// operator was told "a part cannot cost less than nothing" about a box they had
// left empty.
//
// The app layer had this right all along and TestPartAddAllowsAZeroCost passed
// throughout: it calls the service directly and never reaches the handler's
// parsing. That is why the rule now lives in a named function with its own test
// — the layer that actually decides it is the layer that has to be covered.
func TestParsePartCost(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int
		err  bool
		why  string
	}{
		{
			name: "blank is zero", raw: "", want: 0,
			why: "the regression: the cost input is optional in the form, so blank is the ordinary case",
		},
		{
			name: "whitespace only is zero", raw: "   ", want: 0,
			why: "a space typed into an optional box is still an empty answer",
		},
		{
			name: "whole dollars", raw: "4", want: 400,
		},
		{
			name: "dollars and cents", raw: "4.25", want: 425,
		},
		{
			name: "one decimal place", raw: "6.5", want: 650,
		},
		{
			name: "surrounding whitespace is tolerated", raw: " 4.25 ", want: 425,
			why: "pasted values carry spaces",
		},
		{
			name: "zero is a real answer", raw: "0", want: 0,
			why: "a part off a shelf the shop already paid for is worth recording at zero",
		},
		{
			name: "negative is rejected", raw: "-5", err: true,
			why: "blank means unknown, but a typed negative is a mistake worth reporting",
		},
		{
			name: "non-numeric is rejected", raw: "abc", err: true,
			why: "a value that was typed and cannot be read must not be silently zeroed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePartCost(tc.raw)
			if tc.err {
				require.Error(t, err, tc.why)
				return
			}
			require.NoError(t, err, tc.why)
			assert.Equal(t, tc.want, got, tc.why)
		})
	}
}

// The fix must not change parseDollarsCents itself: the settings screens share
// it, and there a missing amount really is a mistake. Pinning this stops the
// next person "simplifying" the two back into one.
func TestParseDollarsCentsStillRejectsBlank(t *testing.T) {
	_, err := parseDollarsCents("")
	assert.Error(t, err,
		"settings forms rely on an empty amount being an error; only the part form treats blank as zero")
}
