package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// anchorRenewalTime is a pure helper, so these tests need no database.
func TestAnchorRenewalTime(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	cases := []struct {
		name string
		in   time.Time
		want time.Time
	}{
		{
			// The common case: a sub signed up mid-afternoon renews the next 2am.
			name: "afternoon rolls forward to next-day 2am",
			in:   time.Date(2026, 7, 17, 14, 23, 0, 0, la),
			want: time.Date(2026, 7, 18, 2, 0, 0, 0, la),
		},
		{
			name: "pre-2am snaps up to same-day 2am",
			in:   time.Date(2026, 7, 17, 1, 0, 0, 0, la),
			want: time.Date(2026, 7, 17, 2, 0, 0, 0, la),
		},
		{
			name: "exactly 2am is unchanged",
			in:   time.Date(2026, 7, 17, 2, 0, 0, 0, la),
			want: time.Date(2026, 7, 17, 2, 0, 0, 0, la),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := anchorRenewalTime(tc.in, la, 2)
			assert.True(t, got.Equal(tc.want), "got %s want %s", got, tc.want)
			// Forward-only: never charge a subscriber before their period elapses.
			assert.False(t, got.Before(tc.in), "anchor must not move the renewal earlier")
		})
	}
}

// The anchor is a wall-clock time in a zone that does not always have 24-hour
// days, and both transitions bend it. Pinned here because the resume path now
// leans on the anchor for a promise about *how soon* the next order lands, and
// the honest version of that promise is "the next renewal run", not "within 24
// hours" — the copy and the resume test both say a calendar day for the reason
// this test records.
//
// Neither case is a defect to fix: a renewal an hour early on one spring night,
// or 45 minutes past the day mark on one autumn night, is invisible to a
// subscriber. It is only a defect to *claim* otherwise.
func TestAnchorRenewalTimeAcrossDST(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	t.Run("spring forward has no 2am, so the anchor falls back to 1am", func(t *testing.T) {
		// 2027-03-14 02:00 PST does not exist — the clocks jump 02:00 to 03:00.
		// time.Date normalizes into the gap downwards, giving 01:00 PST.
		got := anchorRenewalTime(time.Date(2027, 3, 13, 23, 0, 0, 0, la), la, 2)
		want := time.Date(2027, 3, 14, 1, 0, 0, 0, la)
		assert.True(t, got.Equal(want), "got %s want %s", got, want)
		assert.Equal(t, 1, got.In(la).Hour(), "the one day a year the renewal window is not 2am")
	})

	t.Run("fall back stretches the wait past 24 hours", func(t *testing.T) {
		// 02:15 on the day before the clocks go back is past that day's anchor,
		// so it rolls to the next — across a 25-hour day.
		in := time.Date(2026, 10, 31, 2, 15, 0, 0, la)
		got := anchorRenewalTime(in, la, 2)
		want := time.Date(2026, 11, 1, 2, 0, 0, 0, la)
		assert.True(t, got.Equal(want), "got %s want %s", got, want)
		assert.Equal(t, 24*time.Hour+45*time.Minute, got.Sub(in),
			"the widest gap a resume can produce — a calendar day, not 24 hours")
	})

	t.Run("never moves the renewal earlier, on any day of the year", func(t *testing.T) {
		// The invariant that does hold everywhere. Swept at 15-minute steps
		// across a year containing both transitions.
		start := time.Date(2026, 9, 1, 0, 0, 0, 0, la)
		for at := start; at.Before(start.AddDate(1, 0, 0)); at = at.Add(15 * time.Minute) {
			got := anchorRenewalTime(at, la, 2)
			require.False(t, got.Before(at), "anchor moved %s earlier, to %s", at, got)
			require.True(t, got.Sub(at) <= 25*time.Hour,
				"anchor put %s more than a calendar day out, at %s", at, got)
		}
	})
}

func TestAnchorRenewalTimeNilLocIsNoop(t *testing.T) {
	in := time.Date(2026, 7, 17, 14, 23, 0, 0, time.UTC)
	// No anchor wired (e.g. tests, or unset config) → raw timestamp preserved.
	assert.Equal(t, in, anchorRenewalTime(in, nil, 2))
}

func TestAnchorRenewalTimeNormalizesAcrossZones(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	// 2026-07-17 21:23 UTC == 14:23 PDT, so the next 2am PDT is 2026-07-18.
	in := time.Date(2026, 7, 17, 21, 23, 0, 0, time.UTC)
	got := anchorRenewalTime(in, la, 2)
	want := time.Date(2026, 7, 18, 2, 0, 0, 0, la)
	assert.True(t, got.Equal(want), "got %s want %s", got, want)
}
