package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/tax"
)

// Wholesale used to be short-circuited to NoneCalculator here regardless of
// the store's configuration — a blanket exemption that made it impossible to
// bill a cafe for a taxable grinder. Both channels now run the same per-line
// rule, and taxability is a property of the product.
func TestTaxCalculator_WholesaleIsNotExemptByChannel(t *testing.T) {
	cfg := &domain.TaxConfig{Mode: domain.TaxModeFlatRate, Rate: 0.088, Label: "WA Sales Tax"}
	calculator := taxCalculatorForConfig(cfg)

	// A taxable line inside the nexus state is taxed, whoever is buying.
	taxable, err := calculator.Calculate(context.Background(), tax.TaxOrder{
		ShippingState: "WA",
		LineItems:     []domain.TaxLineItem{{LineIndex: 0, Subtotal: 40000, TaxExempt: false}},
	})
	require.NoError(t, err)
	assert.Equal(t, 3520, taxable.TaxTotal, "8.8% of $400")

	// The catalog's present state — every product exempt — still yields zero,
	// which is why this change bills no existing order differently.
	exempt, err := calculator.Calculate(context.Background(), tax.TaxOrder{
		ShippingState: "WA",
		LineItems:     []domain.TaxLineItem{{LineIndex: 0, Subtotal: 40000, TaxExempt: true}},
	})
	require.NoError(t, err)
	assert.Zero(t, exempt.TaxTotal)

	// A reseller permit on the account exempts the whole order regardless.
	permit, err := calculator.Calculate(context.Background(), tax.TaxOrder{
		ShippingState:  "WA",
		CustomerExempt: true,
		LineItems:      []domain.TaxLineItem{{LineIndex: 0, Subtotal: 40000, TaxExempt: false}},
	})
	require.NoError(t, err)
	assert.Zero(t, permit.TaxTotal)

	// Outside the nexus state nothing is taxed.
	away, err := calculator.Calculate(context.Background(), tax.TaxOrder{
		ShippingState: "OR",
		LineItems:     []domain.TaxLineItem{{LineIndex: 0, Subtotal: 40000, TaxExempt: false}},
	})
	require.NoError(t, err)
	assert.Zero(t, away.TaxTotal)
}
