package quickbooks

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The sandbox facts this whole file exists to encode, asserted against the
// request this package actually builds. Every claim below was observed against
// QBO on 2026-09-15 — see the header comment in taxcodes.go.

// An untaxed invoice must carry no tax vocabulary at all. A company that has
// never charged sales tax has no TAX or NON code for a line to point at, and
// every wholesale order in the shop's present state is untaxed.
func TestInvoiceOmitsTaxWhenThereIsNone(t *testing.T) {
	params := InvoiceParams{
		Lines:    []InvoiceLine{{Description: "Iron Horse Blend", Quantity: 4, UnitAmount: 5500, Amount: 22000}},
		Shipping: 1500,
	}

	lines := buildInvoiceLines(params, "17", "23")
	require.Len(t, lines, 2)
	for _, l := range lines {
		require.NotNil(t, l.SalesItemLineDetail)
		assert.Nil(t, l.SalesItemLineDetail.TaxCodeRef,
			"an untaxed invoice must not reference a tax code the company may not have")
	}

	body, err := json.Marshal(qbInvoiceRequest{Line: lines})
	require.NoError(t, err)
	assert.NotContains(t, string(body), "TxnTaxDetail")
	assert.NotContains(t, string(body), "TaxCodeRef")
}

// Taxability is per line, which is the whole point of the per-product rule:
// an invoice that bills a taxable grinder beside exempt coffee taxes only the
// grinder, and the base QBO applies the rate to says so.
func TestTaxableLinesDriveTheTaxableBase(t *testing.T) {
	params := InvoiceParams{
		Lines: []InvoiceLine{
			{Description: "Iron Horse Blend", Quantity: 4, UnitAmount: 5500, Amount: 22000, Taxable: false},
			{Description: "Grinder", Quantity: 1, UnitAmount: 40000, Amount: 40000, Taxable: true},
		},
		Shipping: 1500,
		Tax:      InvoiceTax{Amount: 3520, RatePercent: 8.8, TaxCodeID: "4", TaxRateID: "4"},
	}

	lines := buildInvoiceLines(params, "17", "23")
	require.Len(t, lines, 3)
	assert.Equal(t, "NON", lines[0].SalesItemLineDetail.TaxCodeRef.Value, "exempt coffee")
	assert.Equal(t, "TAX", lines[1].SalesItemLineDetail.TaxCodeRef.Value, "taxable grinder")

	// Shipping is never taxed: the shop's own calculator works from line items
	// only, so a taxable shipping line would have QBO add tax Hiri never
	// charged and the invoice would stop matching the order.
	assert.Equal(t, "NON", lines[2].SalesItemLineDetail.TaxCodeRef.Value, "shipping")

	// The base is the taxable lines alone — not the subtotal, not the total.
	assert.Equal(t, 40000, taxableBaseCents(params))
}

// Rates are matched in hundredths of a percent because a float that has been
// through JSON does not reliably compare equal to the one that was sent.
func TestRateBasisPoints(t *testing.T) {
	assert.Equal(t, 880, rateBasisPoints(8.8))
	assert.Equal(t, 880, rateBasisPoints(8.80))
	assert.Equal(t, 650, rateBasisPoints(6.5))
	assert.Equal(t, 1000, rateBasisPoints(10))

	// Two rates that differ by a hundredth of a percent are different rates,
	// and matching them together would bill one as the other.
	assert.NotEqual(t, rateBasisPoints(8.8), rateBasisPoints(8.81))
}

// The name is for humans only. Matching on it would let a bookkeeper who
// renames the code cause a duplicate rate, which splits the shop's sales-tax
// liability across two accounts in their books.
func TestTaxCodeName(t *testing.T) {
	assert.Equal(t, "WA Sales Tax 8.8%", taxCodeName("WA Sales Tax", 8.8))
	assert.Equal(t, "Sales Tax 6.5%", taxCodeName("", 6.5))
	assert.Equal(t, "Sales Tax 6.5%", taxCodeName("   ", 6.5))
}

// The request shape QBO actually honours. A bare TotalTax comes back as zero,
// so the TaxLine and its rate reference are what must be on the wire.
func TestTaxedInvoiceRequestCarriesRateReference(t *testing.T) {
	body, err := json.Marshal(qbInvoiceRequest{
		TxnTaxDetail: &qbTxnTaxDetail{
			TxnTaxCodeRef: qbRef{Value: "4"},
			TaxLine: []qbTaxLine{{
				DetailType: "TaxLineDetail",
				Amount:     35.20,
				TaxLineDetail: qbTaxLineDetail{
					TaxRateRef:       qbRef{Value: "4"},
					PercentBased:     true,
					TaxPercent:       8.8,
					NetAmountTaxable: 400.00,
				},
			}},
		},
	})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))
	detail, ok := decoded["TxnTaxDetail"].(map[string]any)
	require.True(t, ok, "TxnTaxDetail must be present on a taxed invoice")
	assert.Equal(t, map[string]any{"value": "4"}, detail["TxnTaxCodeRef"])

	taxLines, ok := detail["TaxLine"].([]any)
	require.True(t, ok, "a TaxLine is what produces an amount; TotalTax alone is ignored by QBO")
	require.Len(t, taxLines, 1)
	line := taxLines[0].(map[string]any)
	lineDetail := line["TaxLineDetail"].(map[string]any)
	assert.Equal(t, map[string]any{"value": "4"}, lineDetail["TaxRateRef"],
		"QBO computes from the referenced rate, so the reference is the load-bearing field")
	assert.Equal(t, 400.00, lineDetail["NetAmountTaxable"])
}
