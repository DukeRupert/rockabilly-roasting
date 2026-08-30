package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
)

func gridDay(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// The off-by-one in a calendar grid is the classic bug, and much cheaper to
// catch here than by counting cells in a screenshot.
func TestStartOfWeek(t *testing.T) {
	tests := []struct {
		name string
		day  time.Time
		want time.Time
	}{
		// September 2026 starts on a Tuesday, so the grid opens on 31 August.
		{"a Tuesday walks back one day", gridDay(2026, time.September, 1), gridDay(2026, time.August, 31)},
		{"a Monday is already the start", gridDay(2026, time.August, 31), gridDay(2026, time.August, 31)},
		{"a Sunday walks back six", gridDay(2026, time.September, 6), gridDay(2026, time.August, 31)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, startOfWeek(tc.day))
		})
	}
}

func TestBuildCalendarDays(t *testing.T) {
	month := gridDay(2026, time.September, 1)
	gridStart := startOfWeek(month)

	rows := []domain.MaintenanceDueRow{
		{MaintenanceDue: domain.MaintenanceDue{DueOn: gridDay(2026, time.September, 10)}},
		{MaintenanceDue: domain.MaintenanceDue{DueOn: gridDay(2026, time.September, 10)}},
		// A trailing day from the previous month. It still shows: maintenance
		// due on the 31st does not stop mattering because you paged forward.
		{MaintenanceDue: domain.MaintenanceDue{DueOn: gridDay(2026, time.August, 31)}},
	}

	days := buildCalendarDays(gridStart, month, rows)

	require.Len(t, days, 42, "six weeks covers every month layout")
	assert.Equal(t, gridStart, days[0].Date)
	assert.False(t, days[0].InMonth, "31 August is filler in a September grid")
	assert.Len(t, days[0].Rows, 1, "filler days still carry their work")

	// 10 September is the eleventh day of the month, and the grid opened on the
	// 31st of August, so it lands at index 10.
	tenth := days[10]
	assert.Equal(t, gridDay(2026, time.September, 10), tenth.Date)
	assert.True(t, tenth.InMonth)
	assert.Len(t, tenth.Rows, 2, "two jobs on one day share a cell")
}

func TestParseCalendarMonth(t *testing.T) {
	today := gridDay(2026, time.September, 17)

	assert.Equal(t, gridDay(2026, time.November, 1), parseCalendarMonth("2026-11", today))
	assert.Equal(t, gridDay(2026, time.September, 1), parseCalendarMonth("", today),
		"no month means the one we are in")
	assert.Equal(t, gridDay(2026, time.September, 1), parseCalendarMonth("nonsense", today),
		"a mistyped URL should still show a calendar, not a 500")
}

// A hand-edited scope must show the whole list rather than erroring.
func TestMaintenanceScopeRejectsUnknown(t *testing.T) {
	assert.Equal(t, store.MaintenanceScopeOverdue, maintenanceScope("overdue"))
	assert.Equal(t, store.MaintenanceScopeWarranty, maintenanceScope("warranty"))
	assert.Equal(t, store.MaintenanceScopeAll, maintenanceScope("bogus"))
	assert.Equal(t, store.MaintenanceScopeAll, maintenanceScope(""))
	assert.Equal(t, store.MaintenanceScopeAll, maintenanceScope("bookable"),
		"bookable is the sweep's scope, not a tab somebody can browse to")
}

// Row actions come back to the tab the staffer was on — losing the filter every
// time you log a job makes a list of twenty unbearable.
func TestMaintenancePath(t *testing.T) {
	assert.Equal(t, "/admin/service/maintenance", maintenancePath(""))
	assert.Equal(t, "/admin/service/maintenance", maintenancePath("bogus"))
	assert.Equal(t, "/admin/service/maintenance?scope=overdue", maintenancePath("overdue"))
}

func TestParseMaintenanceDay(t *testing.T) {
	fallback := gridDay(2026, time.September, 1)

	got, err := parseMaintenanceDay("2026-06-13", fallback)
	require.NoError(t, err)
	assert.Equal(t, gridDay(2026, time.June, 13), got)

	got, err = parseMaintenanceDay("  ", fallback)
	require.NoError(t, err)
	assert.Equal(t, fallback, got, "a blank date is today, not a rejection")

	_, err = parseMaintenanceDay("13/06/2026", fallback)
	assert.Error(t, err)
}
