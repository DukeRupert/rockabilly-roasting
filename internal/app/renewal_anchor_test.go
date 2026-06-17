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
