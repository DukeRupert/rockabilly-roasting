package admin

import (
	"testing"
	"time"
)

// ExpiresAt is stored as the start of the day after the last valid one, so
// the list must step back a day: a code good through Dec 21 read "Dec 22".
func TestDiscountLastValidDay(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Skip("no tzdata")
	}
	stored := time.Date(2026, 12, 22, 0, 0, 0, 0, denver)
	if got := discountLastValidDay(stored, denver); got != "Dec 21" {
		t.Fatalf("got %q, want %q", got, "Dec 21")
	}
}
