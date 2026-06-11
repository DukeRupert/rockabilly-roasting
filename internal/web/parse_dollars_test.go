package web

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseDollars(t *testing.T) {
	cases := []struct {
		in    string
		cents int
		ok    bool
	}{
		// Empty defaults to zero.
		{"", 0, true},
		{"   ", 0, true},
		// Whole dollars.
		{"18", 1800, true},
		{"0", 0, true},
		{"1800", 180000, true},
		// Decimals.
		{"18.00", 1800, true},
		{"18.5", 1850, true},
		{"18.05", 1805, true},
		{"0.99", 99, true},
		{".50", 50, true},
		{"18.", 1800, true},
		// Adornments staff might paste or type.
		{"$18.00", 1800, true},
		{"$1,800.00", 180000, true},
		{" $18.00 ", 1800, true},
		// Rejected: too many fractional digits, non-numeric, negative.
		{"18.005", 0, false},
		{"abc", 0, false},
		{"18.0a", 0, false},
		{"-5", 0, false},
		{"-5.00", 0, false},
		{".", 0, false},
	}
	for _, c := range cases {
		cents, ok := parseDollars(c.in)
		assert.Equal(t, c.ok, ok, "ok for %q", c.in)
		if c.ok {
			assert.Equal(t, c.cents, cents, "cents for %q", c.in)
		}
	}
}
