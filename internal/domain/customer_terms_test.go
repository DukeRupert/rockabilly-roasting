package domain

import "testing"

func TestPaymentTermsOptions(t *testing.T) {
	// The set mirrors QBO's stock Terms plus the house net-7 default.
	want := []int{0, 7, 10, 15, 30, 60}
	if len(PaymentTermsOptions) != len(want) {
		t.Fatalf("got %v, want %v", PaymentTermsOptions, want)
	}
	for i, w := range want {
		if PaymentTermsOptions[i] != w {
			t.Errorf("option %d: got %d, want %d", i, PaymentTermsOptions[i], w)
		}
	}
	// Net 21 was retired; nothing should accept it any more.
	if ValidPaymentTermsDays(21) {
		t.Error("net-21 was retired but still validates")
	}
	if !ValidPaymentTermsDays(0) || !ValidPaymentTermsDays(7) {
		t.Error("due-on-receipt and net-7 must both be valid")
	}
	if ValidPaymentTermsDays(-1) || ValidPaymentTermsDays(45) {
		t.Error("out-of-set terms must not validate")
	}
}

func TestPaymentTermsLabel(t *testing.T) {
	if got := PaymentTermsLabel(0); got != "Due on receipt" {
		t.Errorf("zero days: got %q, want %q", got, "Due on receipt")
	}
	if got := PaymentTermsLabel(15); got != "Net 15" {
		t.Errorf("15 days: got %q, want %q", got, "Net 15")
	}
}
