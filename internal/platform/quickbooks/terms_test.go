package quickbooks

import (
	"fmt"
	"testing"
)

func TestQBTermIsStandard(t *testing.T) {
	tests := []struct {
		name string
		term qbTerm
		want bool
	}{
		{"stock standard term", qbTerm{Type: "STANDARD", DueDays: 15}, true},
		{"due on receipt", qbTerm{Type: "STANDARD", DueDays: 0}, true},
		// A DATE_DRIVEN term carries DayOfMonthDue and no DueDays, so it
		// unmarshals with DueDays 0 and would otherwise be matched as "due on
		// receipt" — stamping the wrong terms on every such invoice.
		{"date-driven term must not pass", qbTerm{Type: "DATE_DRIVEN", DayOfMonthDue: 5}, false},
		{"date-driven with a due day and no type", qbTerm{DayOfMonthDue: 5}, false},
		// QBO populates Type on the stock terms, but an absent Type should not
		// disqualify an otherwise ordinary term.
		{"missing type is treated as standard", qbTerm{DueDays: 30}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.term.isStandard(); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsStaleTermRefError(t *testing.T) {
	// Message is http.StatusText(400) in production; only Detail carries the
	// QBO fault, so the fixtures put the distinguishing text there.
	termFault := &APIError{StatusCode: 400, Message: "Bad Request", Detail: "Invalid Reference Id: SalesTermRef id 42 not found"}
	dupFault := &APIError{StatusCode: 400, Message: "Bad Request", Detail: "Duplicate Document Number Exists: DocNumber=WO-1"}
	// The reason the match is on whole words: QBO says "determined" in
	// unrelated faults, and treating that as a term problem would evict a good
	// cached Term and bill without the label.
	determinedFault := &APIError{StatusCode: 400, Message: "Bad Request", Detail: "The transaction amount could not be determined"}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"term reference rejected", fmt.Errorf("%w: %w", ErrBadRequest, termFault), true},
		// The whole point of narrowing: a duplicate DocNumber is also a 400,
		// and retrying it without the term evicts a good cached Term and
		// repeats a request that fails identically.
		{"duplicate doc number is not a term problem", fmt.Errorf("%w: %w", ErrBadRequest, dupFault), false},
		{"non-bad-request error", fmt.Errorf("network unreachable"), false},
		{"bad request with no api error", ErrBadRequest, false},
		{"'determined' is not a term fault", fmt.Errorf("%w: %w", ErrBadRequest, determinedFault), false},
		{"plural terms", fmt.Errorf("%w: %w", ErrBadRequest, &APIError{StatusCode: 400, Message: "Bad Request", Detail: "Payment terms are invalid"}), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isStaleTermRefError(tt.err); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTermName(t *testing.T) {
	if got := termName(0); got != "Due on receipt" {
		t.Errorf("zero days: got %q", got)
	}
	if got := termName(7); got != "Net 7" {
		t.Errorf("seven days: got %q", got)
	}
}
