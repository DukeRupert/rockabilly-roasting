package tax

import (
	"context"
	"strings"

	"github.com/dukerupert/hiri/internal/domain"
)

// TaxCalculator computes tax for an order.
type TaxCalculator interface {
	Calculate(ctx context.Context, order TaxOrder) (domain.TaxResult, error)
}

// TaxOrder holds the inputs needed for tax calculation.
type TaxOrder struct {
	CustomerExempt bool
	ShippingState  string // 2-letter US state code; empty when not yet known
	LineItems      []domain.TaxLineItem
}

// FlatRateCalculator implements TaxCalculator for tax_mode = 'flat_rate'.
// If Jurisdiction is non-empty, tax is only applied when the order's
// ShippingState matches (case-insensitive). Leave Jurisdiction empty to
// apply the rate regardless of destination.
type FlatRateCalculator struct {
	Rate         float64
	Label        string
	Jurisdiction string
}

func (c *FlatRateCalculator) Calculate(_ context.Context, order TaxOrder) (domain.TaxResult, error) {
	if c.Jurisdiction != "" && !strings.EqualFold(strings.TrimSpace(order.ShippingState), c.Jurisdiction) {
		return domain.TaxResult{Label: c.Label}, nil
	}
	return domain.CalculateFlatRateTax(order.LineItems, c.Rate, order.CustomerExempt, c.Label), nil
}

// NoneCalculator implements TaxCalculator for tax_mode = 'none'.
//
// It used to be returned for every B2B order as well, whatever the store's
// mode. That ended on 2026-09-15: taxability is a property of the product, not
// of the channel, so wholesale now runs the same per-line rule as retail. See
// app.taxCalculatorForConfig.
type NoneCalculator struct{}

func (c *NoneCalculator) Calculate(_ context.Context, _ TaxOrder) (domain.TaxResult, error) {
	return domain.TaxResult{}, nil
}
