package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

func rate(cents int) *int { return &cents }

func TestServiceLaborRatesSet(t *testing.T) {
	assert.False(t, domain.ServiceLaborRates{}.Set(),
		"no rate means the reports say nothing about money")
	assert.False(t, domain.ServiceLaborRates{LaborCentsPerHour: rate(0)}.Set(),
		"a zero labour rate is not a rate — it would render as $0.00 of labour, which looks measured")
	assert.True(t, domain.ServiceLaborRates{LaborCentsPerHour: rate(6500)}.Set())
	assert.False(t, domain.ServiceLaborRates{TravelCentsPerHour: rate(4000)}.Set(),
		"travel alone has nothing to appear in")
}

// Travel falls back to the labour rate when it has none of its own.
func TestServiceLaborRatesTravelFallback(t *testing.T) {
	labourOnly := domain.ServiceLaborRates{LaborCentsPerHour: rate(6500)}
	assert.Equal(t, 6500, labourOnly.TravelRate(), "unset travel is costed as labour")

	split := domain.ServiceLaborRates{LaborCentsPerHour: rate(6500), TravelCentsPerHour: rate(4000)}
	assert.Equal(t, 4000, split.TravelRate())
	assert.Equal(t, 6500, split.LaborRate())

	absorbed := domain.ServiceLaborRates{LaborCentsPerHour: rate(6500), TravelCentsPerHour: rate(0)}
	assert.Equal(t, 0, absorbed.TravelRate(), "zero is a decision: the shop absorbs the drive")
}

func TestServiceCostSummaryCosting(t *testing.T) {
	summary := domain.ServiceCostSummary{
		ServiceTotals: domain.ServiceTotals{
			PartsCostCents: 12690,
			LaborMinutes:   150,
			TravelMinutes:  60,
		},
	}

	t.Run("no rate set", func(t *testing.T) {
		assert.Equal(t, 0, summary.LaborCostCents(domain.ServiceLaborRates{}))
		assert.Equal(t, 12690, summary.TotalCostCents(domain.ServiceLaborRates{}),
			"parts alone — the hours were spent, and the shop has not said what they were worth")
	})

	t.Run("labour rate only", func(t *testing.T) {
		rates := domain.ServiceLaborRates{LaborCentsPerHour: rate(6000)}
		// 150m at $60/h = $150, 60m of travel at the same = $60.
		assert.Equal(t, 21000, summary.LaborCostCents(rates))
		assert.Equal(t, 33690, summary.TotalCostCents(rates))
	})

	t.Run("split rates", func(t *testing.T) {
		rates := domain.ServiceLaborRates{LaborCentsPerHour: rate(6000), TravelCentsPerHour: rate(3000)}
		// 150m at $60/h = $150, 60m at $30/h = $30.
		assert.Equal(t, 18000, summary.LaborCostCents(rates))
	})

	t.Run("travel absorbed", func(t *testing.T) {
		rates := domain.ServiceLaborRates{LaborCentsPerHour: rate(6000), TravelCentsPerHour: rate(0)}
		assert.Equal(t, 15000, summary.LaborCostCents(rates))
	})
}

// Costing rounds to the nearest cent rather than truncating. These figures are
// summed across hundreds of entries, and a fraction lost on each would drift
// away from the same numbers computed any other way.
func TestServiceCostRounding(t *testing.T) {
	// 7 minutes at $65/hour is 758.33 cents.
	summary := domain.ServiceCostSummary{
		ServiceTotals: domain.ServiceTotals{LaborMinutes: 7},
	}
	rates := domain.ServiceLaborRates{LaborCentsPerHour: rate(6500)}

	assert.Equal(t, 758, summary.LaborCostCents(rates))

	// 5 minutes at $65/hour is 541.67 — rounds up, where truncation would say 541.
	summary.LaborMinutes = 5
	assert.Equal(t, 542, summary.LaborCostCents(rates))
}

func TestServiceAccountCostSortValid(t *testing.T) {
	assert.True(t, domain.ServiceAccountCostByHours.Valid(), "the empty default is a valid sort")
	assert.True(t, domain.ServiceAccountCostByCost.Valid())
	assert.True(t, domain.ServiceAccountCostByParts.Valid())
	assert.True(t, domain.ServiceAccountCostByVisits.Valid())
	assert.False(t, domain.ServiceAccountCostSort("bogus").Valid())
}
