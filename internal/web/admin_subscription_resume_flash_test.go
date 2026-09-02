package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// The flash exists because the confirmation dialog can be wrong: it names the
// renewal window as of the moment the page was rendered, so a tab left open
// across that hour offers a date the resume will not book, and the staffer who
// clicked has no other way to learn it.
//
// What this test covers is the flash's own contract — it follows the
// subscription's booked date, in the merchant's zone, in the dialog's format.
// That the handler hands it the *resumed* subscription is carried by the type
// (there is no date parameter to pass the wrong value to) and by
// TestResumeSubscriptionBillsAtNextAnchor, which pins what a resume books.
func TestResumeFlashNamesTheBookedDate(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	// The case the review demonstrated: rendered late on Thursday promising
	// Thursday's run, confirmed after 2am, so Friday is what got booked.
	booked := &domain.Subscription{NextOrderAt: time.Date(2027, 3, 12, 2, 0, 0, 0, la)}
	assert.Equal(t, "Subscription resumed — next order Friday, March 12", resumeFlash(booked, la))

	// It reports whatever the subscription carries, rather than anything
	// derived from the current time — a different booking, a different date.
	dayLater := &domain.Subscription{NextOrderAt: booked.NextOrderAt.AddDate(0, 0, 1)}
	assert.Equal(t, "Subscription resumed — next order Saturday, March 13", resumeFlash(dayLater, la))

	// The merchant's day, not the stored value's zone. Deliberately an evening
	// booking, where Los Angeles and UTC disagree about the date: at the 2am
	// anchor they agree, so an assertion there passes whether the conversion
	// happens or not — which is exactly how a dropped .In(loc) survived a
	// round of review.
	evening := time.Date(2027, 3, 12, 22, 0, 0, 0, la)
	assert.Equal(t, "Subscription resumed — next order Friday, March 12",
		resumeFlash(&domain.Subscription{NextOrderAt: evening.UTC()}, la),
		"the booked instant must render as the merchant's day, whatever zone it arrives in")
}
