package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCalculateFlatRateTax(t *testing.T) {
	rate := 0.0875 // 8.75%
	label := "WA Sales Tax"

	t.Run("standard cart no exemptions", func(t *testing.T) {
		items := []TaxLineItem{
			{LineIndex: 0, Subtotal: 1000},
			{LineIndex: 1, Subtotal: 2000},
		}
		result := CalculateFlatRateTax(items, rate, false, label)

		assert.Equal(t, 88+175, result.TaxTotal) // round(1000*0.0875)=88, round(2000*0.0875)=175
		assert.Equal(t, label, result.Label)
		assert.Len(t, result.Breakdown, 2)
		assert.Equal(t, 88, result.Breakdown[0].TaxAmount)
		assert.Equal(t, 175, result.Breakdown[1].TaxAmount)
	})

	t.Run("customer exempt", func(t *testing.T) {
		items := []TaxLineItem{
			{LineIndex: 0, Subtotal: 5000},
		}
		result := CalculateFlatRateTax(items, rate, true, label)

		assert.Equal(t, 0, result.TaxTotal)
		assert.Empty(t, result.Breakdown)
	})

	t.Run("one product exempt", func(t *testing.T) {
		items := []TaxLineItem{
			{LineIndex: 0, Subtotal: 1000, TaxExempt: true},
			{LineIndex: 1, Subtotal: 2000, TaxExempt: false},
		}
		result := CalculateFlatRateTax(items, rate, false, label)

		assert.Equal(t, 175, result.TaxTotal)
		assert.Len(t, result.Breakdown, 2)
		assert.Equal(t, 0, result.Breakdown[0].TaxAmount)
		assert.Equal(t, 175, result.Breakdown[1].TaxAmount)
	})

	t.Run("all products exempt", func(t *testing.T) {
		items := []TaxLineItem{
			{LineIndex: 0, Subtotal: 1000, TaxExempt: true},
			{LineIndex: 1, Subtotal: 2000, TaxExempt: true},
		}
		result := CalculateFlatRateTax(items, rate, false, label)

		assert.Equal(t, 0, result.TaxTotal)
	})

	t.Run("zero rate", func(t *testing.T) {
		items := []TaxLineItem{
			{LineIndex: 0, Subtotal: 5000},
		}
		result := CalculateFlatRateTax(items, 0.0, false, label)

		assert.Equal(t, 0, result.TaxTotal)
	})

	t.Run("rounding fractional cents", func(t *testing.T) {
		// 1001 * 0.0875 = 87.5875 → should round to 88
		items := []TaxLineItem{
			{LineIndex: 0, Subtotal: 1001},
		}
		result := CalculateFlatRateTax(items, rate, false, label)

		assert.Equal(t, 88, result.TaxTotal)
	})

	t.Run("rounding down", func(t *testing.T) {
		// 999 * 0.0875 = 87.4125 → should round to 87
		items := []TaxLineItem{
			{LineIndex: 0, Subtotal: 999},
		}
		result := CalculateFlatRateTax(items, rate, false, label)

		assert.Equal(t, 87, result.TaxTotal)
	})

	t.Run("empty cart", func(t *testing.T) {
		result := CalculateFlatRateTax(nil, rate, false, label)

		assert.Equal(t, 0, result.TaxTotal)
		assert.Empty(t, result.Breakdown)
	})
}

// store_settings holds the rate as a fraction and QuickBooks wants a
// percentage. Passing the fraction by mistake does not fail — it asks
// QuickBooks to create a 0.088% tax rate, and a tax agency to report it
// under, in the merchant's real books. One spelling, tested.
func TestTaxConfigRatePercent(t *testing.T) {
	assert.InDelta(t, 8.8, TaxConfig{Rate: 0.088}.RatePercent().Float64(), 0.0001)
	assert.InDelta(t, 6.5, TaxConfig{Rate: 0.065}.RatePercent().Float64(), 0.0001)
	assert.InDelta(t, 0, TaxConfig{}.RatePercent().Float64(), 0.0001)

	// The value that actually reaches QuickBooks must read as a percentage,
	// not as a fraction: anything under 1 for a US sales tax rate is the
	// fraction leaking through.
	assert.Greater(t, TaxConfig{Rate: 0.088}.RatePercent().Float64(), 1.0)

	// The real guard is the type, not this arithmetic: RatePercent returns
	// TaxRatePercent, so a call site that reaches for the raw fraction does
	// not compile. Centralising the multiplication shrank the class from every
	// call site to four; the named type closed it.
	var _ TaxRatePercent = TaxConfig{Rate: 0.088}.RatePercent()
}
