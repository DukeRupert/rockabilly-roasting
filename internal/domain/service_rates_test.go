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

// Cost is a sum of what each hour was booked at, not a calculation against the
// current rate. The summary carries the figure; nothing recomputes it.
func TestServiceCostSummaryCosting(t *testing.T) {
	summary := domain.ServiceCostSummary{
		ServiceTotals: domain.ServiceTotals{
			PartsCostCents: 12690,
			LaborMinutes:   150,
			TravelMinutes:  60,
			LaborCostCents: 18000,
		},
	}

	assert.Equal(t, 30690, summary.TotalCostCents(), "parts plus the hours as they were booked")
	assert.True(t, summary.AnyCost())
	assert.True(t, summary.FullyCosted())
}

// A shop that never set a rate has hours and no money against them, and the
// difference between "free" and "never priced" has to survive to the surface.
func TestServiceCostSummaryUncosted(t *testing.T) {
	summary := domain.ServiceCostSummary{
		ServiceTotals: domain.ServiceTotals{
			PartsCostCents:  12690,
			LaborMinutes:    150,
			UncostedMinutes: 150,
		},
	}

	assert.Equal(t, 12690, summary.TotalCostCents(), "parts alone — nobody said what the hours were worth")
	assert.False(t, summary.AnyCost())
	assert.False(t, summary.FullyCosted(),
		"the money figure is a floor while any hour is unpriced, and the page says so")
}

// A partly-priced summary is the awkward middle: real money, and a total that
// is not the whole story.
func TestServiceCostSummaryPartlyCosted(t *testing.T) {
	summary := domain.ServiceCostSummary{
		ServiceTotals: domain.ServiceTotals{
			LaborMinutes:    300,
			LaborCostCents:  16250,
			UncostedMinutes: 150,
		},
	}

	assert.True(t, summary.AnyCost())
	assert.False(t, summary.FullyCosted())
}

// One entry's own cost, rounded to the nearest cent.
func TestServiceTimeEntryCost(t *testing.T) {
	priced := domain.ServiceTimeEntry{Minutes: 150, RateCents: rate(6500)}
	assert.True(t, priced.Costed())
	assert.Equal(t, 16250, priced.CostCents(), "2.5h at $65")

	// 7 minutes at $65/hour is 758.33 cents. Rounded, not truncated: these are
	// summed across hundreds of entries and a fraction lost on each would drift.
	odd := domain.ServiceTimeEntry{Minutes: 7, RateCents: rate(6500)}
	assert.Equal(t, 758, odd.CostCents())

	rounding := domain.ServiceTimeEntry{Minutes: 5, RateCents: rate(6500)}
	assert.Equal(t, 542, rounding.CostCents(), "541.67 rounds up, where truncation would say 541")

	unpriced := domain.ServiceTimeEntry{Minutes: 150}
	assert.False(t, unpriced.Costed())
	assert.Equal(t, 0, unpriced.CostCents(), "zero, and the surfaces say \"not priced\" rather than $0.00")

	// An explicit zero is a decision — the shop absorbs the hour — and is not
	// the same as never having priced it.
	absorbed := domain.ServiceTimeEntry{Minutes: 150, RateCents: rate(0)}
	assert.True(t, absorbed.Costed())
	assert.Equal(t, 0, absorbed.CostCents())
}

// The table's money column and its Cost ranking answer one question, so they
// read one field. Deciding it needs a database, so the service owns that — see
// TestCostByAccountShowsCostFromEitherSignal.
func TestServiceAccountReportShowCost(t *testing.T) {
	assert.False(t, domain.ServiceAccountReport{}.ShowCost())
	assert.True(t, domain.ServiceAccountReport{CanCost: true}.ShowCost())

	// Rates alone do not turn it on here: a report built without the check is
	// one whose ranking and column could disagree.
	rated := domain.ServiceAccountReport{
		Rates: domain.ServiceLaborRates{LaborCentsPerHour: rate(6500)},
	}
	assert.False(t, rated.ShowCost())
}

func TestServiceAccountCostSortValid(t *testing.T) {
	assert.True(t, domain.ServiceAccountCostByHours.Valid(), "the empty default is a valid sort")
	assert.True(t, domain.ServiceAccountCostByCost.Valid())
	assert.True(t, domain.ServiceAccountCostByParts.Valid())
	assert.True(t, domain.ServiceAccountCostByVisits.Valid())
	assert.False(t, domain.ServiceAccountCostSort("bogus").Valid())
}
