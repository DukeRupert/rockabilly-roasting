package quickbooks

import (
	"testing"
	"time"
)

func TestDollarsToCents_Rounds(t *testing.T) {
	cases := []struct {
		dollars float64
		want    int
	}{
		{0, 0},
		{0.33, 33}, // 0.33*100 = 32.999... must round to 33, not truncate to 32
		{0.01, 1},
		{99.99, 9999},
		{100.00, 10000},
		{-5.00, -500}, // overpayment / credit
	}
	for _, c := range cases {
		if got := dollarsToCents(c.dollars); got != c.want {
			t.Errorf("dollarsToCents(%v) = %d, want %d", c.dollars, got, c.want)
		}
	}
}

func TestInvoiceFromResponse_ParsesTotalAndDueDate(t *testing.T) {
	var resp qbInvoiceResponse
	resp.Invoice.ID = "42"
	resp.Invoice.DocNumber = "1001"
	resp.Invoice.Balance = 40.00
	resp.Invoice.TotalAmt = 100.00
	resp.Invoice.DueDate = "2026-06-10"

	inv := invoiceFromResponse(resp)

	if inv.ID != "42" || inv.DocNumber != "1001" {
		t.Fatalf("unexpected id/doc: %+v", inv)
	}
	if inv.BalanceCents() != 4000 {
		t.Errorf("BalanceCents = %d, want 4000", inv.BalanceCents())
	}
	if inv.TotalCents() != 10000 {
		t.Errorf("TotalCents = %d, want 10000", inv.TotalCents())
	}
	want := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	if !inv.DueDate.Equal(want) {
		t.Errorf("DueDate = %v, want %v", inv.DueDate, want)
	}
}

func TestInvoiceFromResponse_MissingDueDateIsZero(t *testing.T) {
	var resp qbInvoiceResponse
	resp.Invoice.ID = "7"
	resp.Invoice.TotalAmt = 0 // voided invoice zeroes amounts
	inv := invoiceFromResponse(resp)
	if !inv.DueDate.IsZero() {
		t.Errorf("expected zero DueDate, got %v", inv.DueDate)
	}
	if inv.TotalCents() != 0 {
		t.Errorf("TotalCents = %d, want 0 (voided)", inv.TotalCents())
	}
}
