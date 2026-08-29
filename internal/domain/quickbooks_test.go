package domain

import (
	"strings"
	"testing"
)

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

	clean := QBInvoicePreview{DocNumber: "WO-1", BillEmail: "a@b.test", QBCustomerID: str("42"), AutoBilled: true}
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

	unmatched := QBInvoicePreview{DocNumber: "WO-2", BillEmail: "a@b.test", WouldCreateCustomer: true, AutoBilled: true}
	if !unmatched.NeedsAttention() {
		t.Error("an unmatched customer must need attention")
	}

	noEmail := QBInvoicePreview{DocNumber: "WO-3", QBCustomerID: str("42"), AutoBilled: true}
	if !noEmail.NeedsAttention() {
		t.Error("a preview with no bill-to address must need attention")
	}

	existing := QBInvoicePreview{DocNumber: "WO-4", BillEmail: "a@b.test", QBCustomerID: str("42"), ExistingQBInvoiceID: str("9"), AutoBilled: true}
	if !existing.NeedsAttention() {
		t.Error("an order QuickBooks already has an invoice for must need attention")
	}
}

func TestManualAccountIsNotAProblem(t *testing.T) {
	// An account nobody bills automatically has no billing problem — flagging
	// every one of them would bury the rows that genuinely need someone.
	manual := QBInvoicePreview{DocNumber: "WO-5", BillingMethod: BillingMethodManual}
	if manual.Problem() != "" {
		t.Errorf("manual billing is a decision, not a fault: got %q", manual.Problem())
	}
	if manual.NeedsAttention() {
		t.Error("a manual account must not be flagged as needing attention")
	}
	if !manual.AwaitingManualInvoice() {
		t.Error("but it is waiting for a person to invoice it")
	}

	// And the checks that ask "would this invoice succeed" must stay quiet
	// about an invoice nothing is going to attempt.
	manual.WouldCreateCustomer = true
	manual.BillEmail = ""
	if manual.Problem() != "" {
		t.Errorf("no question about a never-attempted invoice: got %q", manual.Problem())
	}

	billed := QBInvoicePreview{DocNumber: "WO-6", AutoBilled: true, BillEmail: "a@b.test", QBCustomerID: func() *string { s := "1"; return &s }()}
	if billed.AwaitingManualInvoice() {
		t.Error("an automatically billed order is not waiting for anyone")
	}
}

func TestBillingObstacleAnswersADifferentQuestionFromProblem(t *testing.T) {
	str := func(s string) *string { return &s }

	// A manual account with no bill-to address. Leaving it alone is fine, so
	// it is not a problem — but Bill now targets exactly this row, and
	// invoicing it would create the invoice and then fail the send. Before
	// these were separate methods, the operator was told nothing.
	manual := QBInvoicePreview{
		DocNumber:     "WO-1",
		BillingMethod: BillingMethodManual,
		AutoBilled:    false,
	}
	if manual.Problem() != "" {
		t.Errorf("leaving a manual account alone is not a problem: got %q", manual.Problem())
	}
	if manual.NeedsAttention() {
		t.Error("a manual account must not be counted as needing attention")
	}
	obstacle := manual.BillingObstacle()
	if obstacle == "" {
		t.Fatal("but billing it would fail, and the operator has to be told")
	}
	if !strings.Contains(obstacle, "bill-to address") {
		t.Errorf("the obstacle should name the missing address: got %q", obstacle)
	}

	// The customer-match fact is recorded for manual rows too, because
	// invoicing one really would create a customer in the merchant's books.
	unmatched := QBInvoicePreview{DocNumber: "WO-2", BillEmail: "a@b.test", WouldCreateCustomer: true}
	if unmatched.Problem() != "" {
		t.Error("still not flagged on the page")
	}
	if !strings.Contains(unmatched.BillingObstacle(), "new one would be created") {
		t.Errorf("but billing it creates a customer: got %q", unmatched.BillingObstacle())
	}

	// On an automatically billed row the two agree — the obstacle IS the
	// problem, and suppressing one must not have changed the other.
	billed := QBInvoicePreview{DocNumber: "WO-3", AutoBilled: true, QBCustomerID: str("42")}
	if billed.Problem() != billed.BillingObstacle() {
		t.Errorf("on a billed row they must agree: %q vs %q", billed.Problem(), billed.BillingObstacle())
	}

	// A clean row obstructs nothing.
	clean := QBInvoicePreview{DocNumber: "WO-4", AutoBilled: true, BillEmail: "a@b.test", QBCustomerID: str("42")}
	if clean.BillingObstacle() != "" || clean.Problem() != "" {
		t.Error("a clean row has neither")
	}
}
