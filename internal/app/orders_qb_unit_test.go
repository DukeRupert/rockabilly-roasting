package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
			assert.Equal(t, tt.want, invoicePastDue(due, tt.now, time.UTC))
		})
	}
}

// NET terms are counted on the shop's calendar, not on UTC's.
//
// PlacedAt arrives from pgx in UTC, where an evening order in Los Angeles is
// already tomorrow. Adding 24-hour spans to that instant gave a NET 7 invoice a
// due date of day eight — but only for orders placed after about 4pm local,
// which is why nothing looked systematically wrong. Both QuickBooks and our own
// preview row were fed the same expression, so the two systems agreed with each
// other while both disagreed with the cafe's calendar.
func TestInvoiceDueDateCountsTheMerchantsDays(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	day := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}

	cases := []struct {
		name   string
		placed time.Time
		terms  int
		want   time.Time
		wasBug bool // the old expression disagreed here
	}{
		{
			name:   "evening order is still that day for the shop",
			placed: time.Date(2026, 9, 1, 19, 0, 0, 0, la),
			terms:  7,
			want:   day(2026, 9, 8),
			wasBug: true,
		},
		{
			name:   "late evening, minutes before the UTC rollover was already tomorrow",
			placed: time.Date(2026, 9, 1, 23, 30, 0, 0, la),
			terms:  7,
			want:   day(2026, 9, 8),
			wasBug: true,
		},
		{
			name:   "midday order was never affected",
			placed: time.Date(2026, 9, 1, 12, 0, 0, 0, la),
			terms:  7,
			want:   day(2026, 9, 8),
		},
		{
			name:   "due on receipt means the day it was ordered",
			placed: time.Date(2026, 9, 1, 19, 0, 0, 0, la),
			terms:  0,
			want:   day(2026, 9, 1),
			wasBug: true,
		},
		{
			name:   "NET 30 rolls the month over on the calendar",
			placed: time.Date(2026, 8, 31, 20, 0, 0, 0, la),
			terms:  30,
			want:   day(2026, 9, 30),
			wasBug: true,
		},
		{
			name:   "a span containing the spring DST change is still seven days",
			placed: time.Date(2027, 3, 10, 23, 30, 0, 0, la),
			terms:  7,
			want:   day(2027, 3, 17),
			wasBug: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// As pgx hands it over, which is where the fault lived.
			got := InvoiceDueDate(tc.placed.UTC(), tc.terms, la)
			assert.True(t, got.Equal(tc.want), "got %s want %s", got, tc.want)

			// A calendar date carries no time of day and no zone to be
			// re-interpreted in: it is stored in a date column and formatted
			// straight into QuickBooks' YYYY-MM-DD.
			assert.Equal(t, time.UTC, got.Location())
			assert.Zero(t, got.Hour()+got.Minute()+got.Second()+got.Nanosecond())

			if tc.wasBug {
				old := tc.placed.UTC().Add(time.Duration(tc.terms) * 24 * time.Hour)
				assert.NotEqual(t, tc.want.Format("2006-01-02"), old.Format("2006-01-02"),
					"this case is here because the old expression got it wrong; if they now agree the case has stopped guarding anything")
			}
		})
	}

	// No zone configured must not silently shift the answer to some third
	// thing; UTC is the documented fallback.
	placed := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	assert.True(t, InvoiceDueDate(placed, 7, nil).Equal(day(2026, 9, 8)))
}

func TestInvoicePastDueUsesTheMerchantsDay(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	due := time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)

	// 17:00 UTC on the due date is 10:00 in Los Angeles — still the morning of
	// the day the invoice falls due. Comparing against the next UTC day would
	// call that overdue and fire the first past-due chase, which for
	// due-on-receipt terms means billing and chasing on the same morning.
	sameMorningLocal := time.Date(2026, 8, 29, 17, 0, 0, 0, time.UTC)
	assert.False(t, invoicePastDue(due, sameMorningLocal, la),
		"an invoice must not be overdue while it is still its due date where the shop is")

	// Late evening local, still the due date.
	assert.False(t, invoicePastDue(due, time.Date(2026, 8, 30, 6, 59, 0, 0, time.UTC), la))

	// Midnight in Los Angeles is 07:00 UTC the following day.
	assert.True(t, invoicePastDue(due, time.Date(2026, 8, 30, 7, 0, 0, 0, time.UTC), la),
		"once the shop's day rolls over, the due date has passed")
}
