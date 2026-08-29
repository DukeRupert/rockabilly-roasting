package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

func TestEffectivePaymentTermsDays(t *testing.T) {
	days := func(n int) *int { return &n }

	tests := []struct {
		name     string
		customer *domain.Customer
		want     int
	}{
		{"nil customer defaults to net-7", nil, 7},
		{"unset terms default to net-7", &domain.Customer{}, 7},
		// Zero is "due on receipt", a selectable terms value since 2026-08-29,
		// and must survive rather than fall back to the house default.
		{"zero terms mean due on receipt", &domain.Customer{PaymentTermsDays: days(0)}, 0},
		{"negative terms default to net-7", &domain.Customer{PaymentTermsDays: days(-3)}, 7},
		{"explicit net-14 wins", &domain.Customer{PaymentTermsDays: days(14)}, 14},
		{"explicit net-30 wins", &domain.Customer{PaymentTermsDays: days(30)}, 30},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, EffectivePaymentTermsDays(tt.customer))
		})
	}
}

func TestOverdueReminderStageFor(t *testing.T) {
	tests := []struct {
		name        string
		daysPastDue int
		want        int
	}{
		{"not yet due", -1, 0},
		{"due day itself counts as first reminder", 0, 1},
		{"day 1 past due", 1, 1},
		{"day 6 still first stage", 6, 1},
		{"week 2", 7, 2},
		{"week 3", 14, 3},
		{"week 4", 21, 4},
		{"capped at four reminders", 28, 4},
		{"still capped months later", 90, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, overdueReminderStageFor(tt.daysPastDue))
		})
	}
}

func TestInvoicePastDue(t *testing.T) {
	// QB hands back a calendar date, which parses to midnight UTC.
	due := time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"day before", due.Add(-1 * time.Hour), false},
		// The case that matters for due-on-receipt: the invoice is issued and
		// reconciled on its own due date and must not be chased the same day.
		{"first instant of the due date", due, false},
		{"during the due date", due.Add(13 * time.Hour), false},
		{"last instant of the due date", due.Add(24*time.Hour - time.Nanosecond), false},
		{"start of the next day", due.AddDate(0, 0, 1), true},
		{"a week later", due.AddDate(0, 0, 7), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, invoicePastDue(due, tt.now))
		})
	}
}
