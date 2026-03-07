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
