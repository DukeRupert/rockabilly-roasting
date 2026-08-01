package jobs

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWeeklyScheduleNext(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	require.NoError(t, err)

	s := NewWeeklySchedule(time.Friday, 10, 0, denver)

	tests := []struct {
		name string
		from time.Time
		want time.Time
	}{
		{
			name: "midweek advances to Friday",
			from: time.Date(2026, 8, 5, 14, 0, 0, 0, denver), // Wednesday
			want: time.Date(2026, 8, 7, 10, 0, 0, 0, denver),
		},
		{
			name: "earlier on send day fires the same day",
			from: time.Date(2026, 8, 7, 9, 59, 0, 0, denver),
			want: time.Date(2026, 8, 7, 10, 0, 0, 0, denver),
		},
		{
			// A restart after the send must not fire a second batch — it rolls
			// a full week rather than scheduling for "now".
			name: "after the send time rolls a full week",
			from: time.Date(2026, 8, 7, 10, 1, 0, 0, denver),
			want: time.Date(2026, 8, 14, 10, 0, 0, 0, denver),
		},
		{
			name: "exactly at the send time rolls a full week",
			from: time.Date(2026, 8, 7, 10, 0, 0, 0, denver),
			want: time.Date(2026, 8, 14, 10, 0, 0, 0, denver),
		},
		{
			name: "day after the send waits nearly a week",
			from: time.Date(2026, 8, 8, 6, 0, 0, 0, denver), // Saturday
			want: time.Date(2026, 8, 14, 10, 0, 0, 0, denver),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.Next(tt.from)
			require.True(t, got.Equal(tt.want), "got %s, want %s", got, tt.want)
			require.Equal(t, time.Friday, got.In(denver).Weekday())
		})
	}
}

// The send must stay at 10:00 merchant-local across a DST transition. An
// interval-based schedule would drift by an hour here.
func TestWeeklyScheduleHoldsLocalHourAcrossDST(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	require.NoError(t, err)

	s := NewWeeklySchedule(time.Friday, 10, 0, denver)

	// US DST ends Sunday 2026-11-01; this spans that boundary.
	from := time.Date(2026, 10, 30, 11, 0, 0, 0, denver) // Friday, after the send
	next := s.Next(from)

	local := next.In(denver)
	require.Equal(t, time.Friday, local.Weekday())
	require.Equal(t, 2026, local.Year())
	require.Equal(t, time.November, local.Month())
	require.Equal(t, 6, local.Day())
	require.Equal(t, 10, local.Hour(), "send must stay at 10:00 local through the DST change")
	require.Equal(t, 0, local.Minute())
}

func TestWeeklyScheduleNilLocationDefaultsUTC(t *testing.T) {
	s := NewWeeklySchedule(time.Monday, 8, 30, nil)
	next := s.Next(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	require.Equal(t, time.Monday, next.Weekday())
	require.Equal(t, 8, next.Hour())
	require.Equal(t, 30, next.Minute())
	require.Equal(t, time.UTC, next.Location())
}
