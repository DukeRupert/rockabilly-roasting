package domain

import "math"

// TaxMode represents the tenant-level tax calculation strategy.
type TaxMode string

const (
	TaxModeStripeTax TaxMode = "stripe_tax"
	TaxModeFlatRate  TaxMode = "flat_rate"
	TaxModeNone      TaxMode = "none"
)

// TaxConfig holds the store-level tax configuration.
type TaxConfig struct {
	Mode  TaxMode
	Rate  float64 // decimal fraction, e.g. 0.0875 for 8.75%
	Label string  // e.g. "WA Sales Tax"
}

// RatePercent is the rate as a percentage (8.8), where Rate is the fraction
// (0.088) the column holds.
//
// It exists so the conversion has exactly one spelling. QuickBooks wants a
// percentage, and a caller that passes the fraction by mistake does not fail —
// it asks QuickBooks to create a 0.088% tax rate, and a tax agency to report
// it under, in the merchant's real books. That is a silent, hand-cleaned mess,
// so the multiplication lives here with a test on it rather than at each call
// site.
func (c TaxConfig) RatePercent() float64 { return c.Rate * 100 }

// TaxLineItem represents a single line item for tax calculation.
type TaxLineItem struct {
	LineIndex int
	Subtotal  int  // cents — the taxable amount for this line
	TaxExempt bool // from product.tax_exempt
}

// TaxResult holds the outcome of a tax calculation.
type TaxResult struct {
	TaxTotal  int    // cents
	Label     string // copied from TaxConfig.Label
	Breakdown []TaxLineBreakdown
}

// TaxLineBreakdown holds the tax amount for a single line item.
type TaxLineBreakdown struct {
	LineIndex int
	TaxAmount int // cents
}

// CalculateFlatRateTax is a pure function — no DB, no external calls.
// Skips tax-exempt line items and tax-exempt customers.
func CalculateFlatRateTax(items []TaxLineItem, rate float64, customerExempt bool, label string) TaxResult {
	if customerExempt || rate == 0 {
		return TaxResult{Label: label}
	}

	var total int
	breakdown := make([]TaxLineBreakdown, 0, len(items))

	for _, item := range items {
		if item.TaxExempt {
			breakdown = append(breakdown, TaxLineBreakdown{
				LineIndex: item.LineIndex,
				TaxAmount: 0,
			})
			continue
		}
		tax := int(math.Round(float64(item.Subtotal) * rate))
		total += tax
		breakdown = append(breakdown, TaxLineBreakdown{
			LineIndex: item.LineIndex,
			TaxAmount: tax,
		})
	}

	return TaxResult{
		TaxTotal:  total,
		Label:     label,
		Breakdown: breakdown,
	}
}
