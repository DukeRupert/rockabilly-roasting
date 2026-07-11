package quickbooks

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildInvoiceLines(t *testing.T) {
	// buildInvoiceLines reads only Lines and Shipping from the params.
	params := InvoiceParams{
		Lines: []InvoiceLine{
			{Description: "Iron Horse Blend (WH-5LB)", Quantity: 4, UnitAmount: 5500, Amount: 22000},
			{Description: "Rebel Roast (WH-5LB)", Quantity: 2, UnitAmount: 6000, Amount: 12000},
		},
		Shipping: 1500,
	}

	t.Run("every line carries the sales ItemRef", func(t *testing.T) {
		lines := buildInvoiceLines(params, "17", "23")
		require.Len(t, lines, 3) // 2 products + shipping

		for _, l := range lines[:2] {
			assert.Equal(t, "SalesItemLineDetail", l.DetailType)
			require.NotNil(t, l.SalesItemLineDetail)
			assert.Equal(t, "17", l.SalesItemLineDetail.ItemRef.Value)
		}

		ship := lines[2]
		assert.Equal(t, "Shipping", ship.Description)
		require.NotNil(t, ship.SalesItemLineDetail)
		assert.Equal(t, "23", ship.SalesItemLineDetail.ItemRef.Value)
		assert.Equal(t, 15.0, ship.Amount)
	})

	t.Run("shipping falls back to the sales item", func(t *testing.T) {
		lines := buildInvoiceLines(params, "17", "")
		require.Len(t, lines, 3)
		assert.Equal(t, "17", lines[2].SalesItemLineDetail.ItemRef.Value)
	})

	t.Run("no shipping line when shipping is zero", func(t *testing.T) {
		p := params
		p.Shipping = 0
		lines := buildInvoiceLines(p, "17", "")
		require.Len(t, lines, 2)
	})
}

func TestCentsToFloat(t *testing.T) {
	tests := []struct {
		name  string
		cents int
		want  float64
	}{
		{"zero", 0, 0.0},
		{"one dollar", 100, 1.0},
		{"ten cents", 10, 0.10},
		{"one cent", 1, 0.01},
		{"thirty-three cents", 33, 0.33},
		{"typical order", 4999, 49.99},
		{"large amount", 99999999, 999999.99},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := centsToFloat(tt.cents)
			// QB rounds to 2 decimal places, so verify within half-cent tolerance.
			assert.True(t, math.Abs(got-tt.want) < 0.005,
				"centsToFloat(%d) = %f, want %f (within 0.005)", tt.cents, got, tt.want)
		})
	}
}

// The payment flags must reach QBO explicitly — an omitempty would drop
// `false` and let the company default re-enable a pay button the caller
// meant to turn off.
func TestInvoiceRequestPaymentFlagsAlwaysSent(t *testing.T) {
	b, err := json.Marshal(qbInvoiceRequest{
		CustomerRef:                  qbRef{Value: "42"},
		DueDate:                      "2026-07-17",
		AllowOnlineACHPayment:        true,
		AllowOnlineCreditCardPayment: false,
	})
	require.NoError(t, err)
	assert.Contains(t, string(b), `"AllowOnlineACHPayment":true`)
	assert.Contains(t, string(b), `"AllowOnlineCreditCardPayment":false`)
}
