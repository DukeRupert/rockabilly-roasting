package quickbooks

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCentsToFloat(t *testing.T) {
	tests := []struct {
		name  string
		cents int
		want  float64
	}{
		{"zero", 0, 0.0},
		{"one dollar", 100, 1.0},
		{"ten cents", 10, 0.10},
		{"one cent", 1, 0.01},
		{"thirty-three cents", 33, 0.33},
		{"typical order", 4999, 49.99},
		{"large amount", 99999999, 999999.99},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := centsToFloat(tt.cents)
			// QB rounds to 2 decimal places, so verify within half-cent tolerance.
			assert.True(t, math.Abs(got-tt.want) < 0.005,
				"centsToFloat(%d) = %f, want %f (within 0.005)", tt.cents, got, tt.want)
		})
	}
}
