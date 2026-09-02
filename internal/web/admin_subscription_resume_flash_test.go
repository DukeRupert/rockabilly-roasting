package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The flash exists because the confirmation dialog can be wrong. It names the
// window as of the moment the page was rendered, so a tab left open across the
// renewal hour offers a date the resume will not book — and the staffer who
// clicked it has no other way to learn that. The flash is what corrects them,
// which only works if it reports the booking rather than re-deriving a date.
func TestResumeFlashNamesTheBookedDate(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	// The case the review demonstrated: rendered late on Wednesday promising
	// Thursday's run, confirmed after 2am, so Friday is what got booked.
	booked := time.Date(2027, 3, 12, 2, 0, 0, 0, la)
	assert.Equal(t, "Subscription resumed — next order Friday, March 12", resumeFlash(booked, la))

	// Same instant, and it must still read as the merchant's day rather than
	// UTC's — 2am in Los Angeles is already the next date in UTC.
	assert.Equal(t, "Subscription resumed — next order Friday, March 12",
		resumeFlash(booked.UTC(), la),
		"the booked instant must be rendered in the merchant's zone, whatever zone it arrives in")

	// No zone configured is not a reason to print nothing useful — it falls back
	// to UTC. Checked at an hour where UTC has already rolled over, so this
	// pins the fallback rather than passing on a coincidence.
	lateEvening := time.Date(2027, 3, 12, 22, 0, 0, 0, la)
	assert.Equal(t, "Subscription resumed — next order Friday, March 12", resumeFlash(lateEvening, la))
	assert.Equal(t, "Subscription resumed — next order Saturday, March 13", resumeFlash(lateEvening, nil))
}
