package jobs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The tolerance decides whether a tax difference pages an engineer or is
// filed as arithmetic, so its shape matters more than its exact value.
//
// A flat cent was the first attempt and it was wrong: Hiri rounds tax per
// line, QBO rounds once over the summed base, each line contributes up to half
// a cent, and the errors do not cancel. Twelve taxable lines of $6.31 at 8.8%
// diverge by six cents — which a flat cent would have logged at Error, the
// false-alarm outcome the tolerance exists to prevent.
func TestTaxRoundingTolerance(t *testing.T) {
	// Half a cent per line, rounded up.
	assert.Equal(t, 1, taxRoundingTolerance(1))
	assert.Equal(t, 1, taxRoundingTolerance(2))
	assert.Equal(t, 2, taxRoundingTolerance(4))
	assert.Equal(t, 4, taxRoundingTolerance(8))
	assert.Equal(t, 6, taxRoundingTolerance(12), "the case that falsified the flat cent")

	// It has to keep growing, or a large order re-acquires the false alarm.
	assert.Greater(t, taxRoundingTolerance(20), taxRoundingTolerance(8))

	// No taxable line and yet a divergence: nothing about that is rounding,
	// and it must not be swallowed.
	assert.Equal(t, 0, taxRoundingTolerance(0))
	assert.Equal(t, 0, taxRoundingTolerance(-1))
}
