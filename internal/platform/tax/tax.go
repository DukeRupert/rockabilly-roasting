package tax

import (
	"context"

	"github.com/dukerupert/hiri/internal/domain"
)

// TaxCalculator computes tax for an order.
type TaxCalculator interface {
	Calculate(ctx context.Context, order TaxOrder) (domain.TaxResult, error)
}

// TaxOrder holds the inputs needed for tax calculation.
type TaxOrder struct {
	CustomerExempt bool
	LineItems      []domain.TaxLineItem
}

// FlatRateCalculator implements TaxCalculator for tax_mode = 'flat_rate'.
type FlatRateCalculator struct {
	Rate  float64
	Label string
}

func (c *FlatRateCalculator) Calculate(_ context.Context, order TaxOrder) (domain.TaxResult, error) {
	return domain.CalculateFlatRateTax(order.LineItems, c.Rate, order.CustomerExempt, c.Label), nil
}

// NoneCalculator implements TaxCalculator for tax_mode = 'none'.
// Also used for all B2B orders regardless of tenant tax_mode.
type NoneCalculator struct{}

func (c *NoneCalculator) Calculate(_ context.Context, _ TaxOrder) (domain.TaxResult, error) {
	return domain.TaxResult{}, nil
}
