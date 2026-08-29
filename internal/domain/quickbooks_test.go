package domain

import "testing"

func TestQBBillingMode(t *testing.T) {
	if !QBBillingModeLive.IsLive() {
		t.Error("live must read as live")
	}
	if QBBillingModeShadow.IsLive() {
		t.Error("shadow must not read as live")
	}
	// A value written by a newer binary, or corrupted, must not be taken as
	// permission to bill real customers.
	if QBBillingMode("something_else").IsLive() {
		t.Error("an unknown mode must never read as live")
	}
	if QBBillingMode("something_else").Valid() {
		t.Error("an unknown mode must not validate")
	}
	if DefaultQBBillingMode.IsLive() {
		t.Error("the default must not bill")
	}
}

func TestQBInvoicePreviewProblem(t *testing.T) {
	str := func(s string) *string { return &s }

	clean := QBInvoicePreview{DocNumber: "WO-1", BillEmail: "a@b.test", QBCustomerID: str("42")}
	if clean.Problem() != "" || clean.NeedsAttention() {
		t.Errorf("a matched, addressable preview should be clean, got %q", clean.Problem())
	}

	// Ordered by how much they should worry a human: a failed lookup means we
	// do not know anything, so it outranks conclusions drawn from a lookup
	// that did not happen.
	failed := clean
	failed.LookupError = str("boom")
	failed.WouldCreateCustomer = true
	if got := failed.Problem(); got != "QuickBooks lookup failed: boom" {
		t.Errorf("lookup failure should win, got %q", got)
	}

	unmatched := QBInvoicePreview{DocNumber: "WO-2", BillEmail: "a@b.test", WouldCreateCustomer: true}
	if !unmatched.NeedsAttention() {
		t.Error("an unmatched customer must need attention")
	}

	noEmail := QBInvoicePreview{DocNumber: "WO-3", QBCustomerID: str("42")}
	if !noEmail.NeedsAttention() {
		t.Error("a preview with no bill-to address must need attention")
	}

	existing := QBInvoicePreview{DocNumber: "WO-4", BillEmail: "a@b.test", QBCustomerID: str("42"), ExistingQBInvoiceID: str("9")}
	if !existing.NeedsAttention() {
		t.Error("an order QuickBooks already has an invoice for must need attention")
	}
}
