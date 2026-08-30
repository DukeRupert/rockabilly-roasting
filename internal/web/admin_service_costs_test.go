package web

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
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

// Blank and zero are different states: blank takes the money column off the
// reports, zero says the shop absorbs the drive.
func TestParseOptionalRate(t *testing.T) {
	got, err := parseOptionalRate("")
	assert.NoError(t, err)
	assert.Nil(t, got, "blank is unset, not zero")

	got, err = parseOptionalRate("  ")
	assert.NoError(t, err)
	assert.Nil(t, got)

	got, err = parseOptionalRate("0")
	assert.NoError(t, err)
	if assert.NotNil(t, got) {
		assert.Equal(t, 0, *got, "an explicit zero is a decision worth storing")
	}

	got, err = parseOptionalRate("65")
	assert.NoError(t, err)
	if assert.NotNil(t, got) {
		assert.Equal(t, 6500, *got)
	}

	got, err = parseOptionalRate("65.50")
	assert.NoError(t, err)
	if assert.NotNil(t, got) {
		assert.Equal(t, 6550, *got)
	}

	// Somebody pasting a figure off an invoice brings the dollar sign with it.
	got, err = parseOptionalRate("$72.25")
	assert.NoError(t, err)
	if assert.NotNil(t, got) {
		assert.Equal(t, 7225, *got)
	}

	_, err = parseOptionalRate("sixty five")
	assert.Error(t, err)

	_, err = parseOptionalRate("-10")
	assert.Error(t, err, "a negative hourly cost is not a thing")
}

// An unset rate renders as an empty field, never "0.00" — which would read as a
// decision nobody made.
func TestRateInput(t *testing.T) {
	assert.Equal(t, "", rateInput(nil))

	zero := 0
	assert.Equal(t, "0.00", rateInput(&zero))

	odd := 6505
	assert.Equal(t, "65.05", rateInput(&odd))

	round := 6500
	assert.Equal(t, "65.00", rateInput(&round))
}

// An absent sort has to be distinguishable from an explicit one: the default
// depends on whether a labour rate exists, which the parser cannot know.
func TestServiceCostSort(t *testing.T) {
	sort, explicit := serviceCostSort(url.Values{})
	assert.False(t, explicit)
	assert.Equal(t, domain.ServiceAccountCostByHours, sort)

	sort, explicit = serviceCostSort(url.Values{"sort": {"parts"}})
	assert.True(t, explicit)
	assert.Equal(t, domain.ServiceAccountCostByParts, sort)

	sort, explicit = serviceCostSort(url.Values{"sort": {"cost"}})
	assert.True(t, explicit)
	assert.Equal(t, domain.ServiceAccountCostByCost, sort)

	// An empty sort= is the reader explicitly asking for the hours ranking,
	// which is the value that option carries in the strip.
	_, explicit = serviceCostSort(url.Values{"sort": {""}})
	assert.True(t, explicit)

	sort, explicit = serviceCostSort(url.Values{"sort": {"bogus"}})
	assert.False(t, explicit, "a mistyped URL falls back to the best default")
	assert.Equal(t, domain.ServiceAccountCostByHours, sort)
}
