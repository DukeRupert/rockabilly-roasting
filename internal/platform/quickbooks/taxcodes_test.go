package quickbooks

import (
	"encoding/json"
	"testing"
	"time"

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

// The request shape QBO actually honours, asserted against the builder that
// actually produces it. An earlier version of this test hand-built the struct
// literal and checked its JSON tags, which cannot fail however wrong
// buildInvoiceRequest goes — and buildInvoiceRequest is where the money is.
func TestTaxedInvoiceRequestCarriesRateReference(t *testing.T) {
	params := InvoiceParams{
		CustomerID: "1",
		DocNumber:  "WO-1234",
		DueDate:    time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC),
		Lines: []InvoiceLine{
			{Description: "Iron Horse Blend", Quantity: 4, UnitAmount: 5500, Amount: 22000, Taxable: false},
			{Description: "Grinder", Quantity: 1, UnitAmount: 40000, Amount: 40000, Taxable: true},
		},
		Shipping: 1500,
		Tax:      InvoiceTax{Amount: 3520, RatePercent: 8.8, TaxCodeID: "4", TaxRateID: "9"},
	}

	body, err := buildInvoiceRequest(params, ClientConfig{SalesItemID: "19"})
	require.NoError(t, err)
	require.NotNil(t, body.TxnTaxDetail)

	assert.Equal(t, "4", body.TxnTaxDetail.TxnTaxCodeRef.Value)
	require.Len(t, body.TxnTaxDetail.TaxLine, 1)
	line := body.TxnTaxDetail.TaxLine[0]
	assert.Equal(t, "TaxLineDetail", line.DetailType)
	assert.InDelta(t, 35.20, line.Amount, 0.001)

	// The rate reference is the load-bearing field: QBO computes from the rate
	// it points at and ignores the percent in the request.
	assert.Equal(t, "9", line.TaxLineDetail.TaxRateRef.Value)
	assert.True(t, line.TaxLineDetail.PercentBased)
	assert.InDelta(t, 8.8, line.TaxLineDetail.TaxPercent, 0.001)

	// The taxable base is the grinder alone — not the subtotal, not the total,
	// and not including shipping.
	assert.InDelta(t, 400.00, line.TaxLineDetail.NetAmountTaxable, 0.001)
}

// Tax with nowhere to put it must be refused before the request is sent.
// Billing it anyway creates an invoice short by the tax, silently — the exact
// failure a TotalTax-only request produces against QBO.
func TestInvoiceRefusesTaxWithNoTaxCode(t *testing.T) {
	params := InvoiceParams{
		CustomerID: "1",
		Lines:      []InvoiceLine{{Description: "Grinder", Quantity: 1, UnitAmount: 40000, Amount: 40000, Taxable: true}},
		Tax:        InvoiceTax{Amount: 3520, RatePercent: 8.8},
	}

	for _, tc := range []struct {
		name string
		tax  InvoiceTax
	}{
		{"neither ref", InvoiceTax{Amount: 3520, RatePercent: 8.8}},
		{"code but no rate", InvoiceTax{Amount: 3520, RatePercent: 8.8, TaxCodeID: "4"}},
		{"rate but no code", InvoiceTax{Amount: 3520, RatePercent: 8.8, TaxRateID: "9"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := params
			p.Tax = tc.tax
			_, err := buildInvoiceRequest(p, ClientConfig{SalesItemID: "19"})
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrBadRequest, "must be permanent — a missing rate never fixes itself on retry")
		})
	}

	// An untaxed invoice needs no refs and must still build.
	p := params
	p.Tax = InvoiceTax{}
	p.Lines[0].Taxable = false
	body, err := buildInvoiceRequest(p, ClientConfig{SalesItemID: "19"})
	require.NoError(t, err)
	assert.Nil(t, body.TxnTaxDetail)
}

// matchTaxCode is what stands between the shop and an invoice billed at a rate
// nobody configured. Each rejection below is a wrong amount of money collected
// from a real customer if it stops happening.
func TestMatchTaxCode(t *testing.T) {
	active, inactive := true, false
	rate := func(id string, v float64, act *bool) qbTaxRate {
		return qbTaxRate{ID: id, RateValue: v, Active: act}
	}
	code := func(id string, taxable bool, act *bool, rateIDs ...string) qbTaxCode {
		c := qbTaxCode{ID: id, Taxable: taxable, Active: act}
		for _, rid := range rateIDs {
			c.SalesTaxRateList.TaxRateDetail = append(c.SalesTaxRateList.TaxRateDetail, struct {
				TaxRateRef qbRef `json:"TaxRateRef"`
			}{TaxRateRef: qbRef{Value: rid}})
		}
		return c
	}

	t.Run("matches the code carrying exactly the rate asked for", func(t *testing.T) {
		got := matchTaxCode(
			[]qbTaxRate{rate("9", 8.8, nil), rate("3", 8.0, nil)},
			[]qbTaxCode{code("2", true, nil, "3"), code("4", true, nil, "9")},
			8.8)
		assert.Equal(t, TaxCodeRef{TaxCodeID: "4", TaxRateID: "9"}, got)
	})

	t.Run("no match when the company has no such rate", func(t *testing.T) {
		got := matchTaxCode([]qbTaxRate{rate("3", 8.0, nil)}, []qbTaxCode{code("2", true, nil, "3")}, 8.8)
		assert.Equal(t, TaxCodeRef{}, got, "a near miss must create the right rate, not bill the wrong one")
	})

	t.Run("skips a deactivated rate", func(t *testing.T) {
		got := matchTaxCode([]qbTaxRate{rate("9", 8.8, &inactive)}, []qbTaxCode{code("4", true, nil, "9")}, 8.8)
		assert.Equal(t, TaxCodeRef{}, got, "referencing a dead rate fails every invoice until the cache is dropped")
	})

	t.Run("skips a deactivated code", func(t *testing.T) {
		got := matchTaxCode([]qbTaxRate{rate("9", 8.8, &active)}, []qbTaxCode{code("4", true, &inactive, "9")}, 8.8)
		assert.Equal(t, TaxCodeRef{}, got)
	})

	t.Run("absent Active means active — the built-in codes carry no flag", func(t *testing.T) {
		got := matchTaxCode([]qbTaxRate{rate("9", 8.8, nil)}, []qbTaxCode{code("4", true, nil, "9")}, 8.8)
		assert.Equal(t, TaxCodeRef{TaxCodeID: "4", TaxRateID: "9"}, got)
	})

	t.Run("skips a non-taxable code", func(t *testing.T) {
		got := matchTaxCode([]qbTaxRate{rate("9", 8.8, nil)}, []qbTaxCode{code("NON", false, nil, "9")}, 8.8)
		assert.Equal(t, TaxCodeRef{}, got)
	})

	t.Run("skips a group code carrying more than one rate", func(t *testing.T) {
		// Two rates on one code charge their sum. Matching on a member would
		// bill the combined rate while reporting the member's.
		got := matchTaxCode(
			[]qbTaxRate{rate("9", 8.8, nil), rate("7", 2.0, nil)},
			[]qbTaxCode{code("5", true, nil, "9", "7")},
			8.8)
		assert.Equal(t, TaxCodeRef{}, got)
	})
}

// The cache has to distinguish rates, or every invoice after the first bills
// at the first one's rate.
func TestTaxCodeCacheKeysOnTheRate(t *testing.T) {
	c := newTaxCodeCache()
	c.put(rateBasisPoints(8.8), TaxCodeRef{TaxCodeID: "4", TaxRateID: "9"})
	c.put(rateBasisPoints(6.5), TaxCodeRef{TaxCodeID: "2", TaxRateID: "3"})

	got, ok := c.get(rateBasisPoints(8.8))
	require.True(t, ok)
	assert.Equal(t, "4", got.TaxCodeID)

	got, ok = c.get(rateBasisPoints(6.5))
	require.True(t, ok)
	assert.Equal(t, "2", got.TaxCodeID)

	_, ok = c.get(rateBasisPoints(10))
	assert.False(t, ok)

	// forget drops by code id, which is how a rejected invoice recovers.
	c.forget("4")
	_, ok = c.get(rateBasisPoints(8.8))
	assert.False(t, ok)
	_, ok = c.get(rateBasisPoints(6.5))
	assert.True(t, ok, "forgetting one rate must not empty the cache")
}

// Truncation instead of rounding would put rates a hundredth of a percent
// below their true value into a different bucket, so a company's existing
// 8.75% rate would not be matched and a duplicate created beside it.
func TestRateBasisPointsRoundsRatherThanTruncates(t *testing.T) {
	// The cases that actually separate the two: in float64, 4.10*100 is
	// 409.99999999999994 and 4.27*100 is 426.99999999999994, so truncation
	// puts each a hundredth of a percent low. Picked by enumerating every
	// two-decimal rate from 4% to 11% and keeping ones where trunc and round
	// disagree — 55 of 700 do, so a plausible local rate hits this.
	assert.Equal(t, 410, rateBasisPoints(4.10))
	assert.Equal(t, 431, rateBasisPoints(4.31))
	assert.Equal(t, 427, rateBasisPoints(4.27))

	// And the shop's own rate, which does not.
	assert.Equal(t, 880, rateBasisPoints(8.8))
}
