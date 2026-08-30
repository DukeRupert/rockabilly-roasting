package web

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A hand-edited window must land on one the control strip can highlight —
// otherwise the reader cannot tell what period they are looking at.
func TestServiceCostDays(t *testing.T) {
	assert.Equal(t, 90, serviceCostDays(""), "the quarter is the period people argue about")
	assert.Equal(t, 90, serviceCostDays("90"))
	assert.Equal(t, 365, serviceCostDays("365"))
	assert.Equal(t, 0, serviceCostDays("0"), "zero is all time")
	assert.Equal(t, 90, serviceCostDays("42"), "an arbitrary window falls back to the default")
	assert.Equal(t, 90, serviceCostDays("nonsense"))
}
