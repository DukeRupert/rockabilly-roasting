package web

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// The note is clipped by runes, not bytes, and the difference is not cosmetic:
// a byte slice can land mid-character, and Postgres refuses invalid UTF-8
// outright. The staffer would then get a generic "failed to move that run" on a
// note the form had just accepted, with nothing saved.
func TestClipNote_KeepsValidUTF8(t *testing.T) {
	// Every character is two bytes, so 250 of them clear maxlength in the
	// browser and still overshoot a 200-byte cut — landing inside a character.
	long := strings.Repeat("é", 250)
	require.Greater(t, len(long), postponementNoteMaxRunes, "fixture must exceed the byte cut")

	got := clipNote(long)

	assert.True(t, utf8.ValidString(got), "clipped note must stay valid UTF-8")
	assert.Equal(t, postponementNoteMaxRunes, utf8.RuneCountInString(got))
	assert.Equal(t, strings.Repeat("é", postponementNoteMaxRunes), got)
}

func TestClipNote_LeavesShortNotesAlone(t *testing.T) {
	assert.Equal(t, "Labor Day", clipNote("Labor Day"))
	assert.Equal(t, "", clipNote(""))

	exact := strings.Repeat("a", postponementNoteMaxRunes)
	assert.Equal(t, exact, clipNote(exact))
}

// mustLoad is the merchant zone the panel renders in.
func mustLoad(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	return loc
}

// utcDate is how a postponement arrives from Postgres: a bare `date`, handed
// back as midnight UTC whatever zone the shop keeps.
func utcDate(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// A postponement passes through three states, not two, and the middle one is
// the ordinary case: a Monday run moved to Thursday, seen on the Wednesday.
// Its scheduled day has gone but the run has not, so it carries no Restore
// button — and without a word for it the row would simply go quiet.
func TestPostponementRows_ThreeStates(t *testing.T) {
	loc := mustLoad(t)
	// A Wednesday, with the Monday behind it and the Thursday ahead.
	now := time.Date(2026, time.September, 9, 10, 0, 0, 0, loc)

	rows := postponementRows([]domain.DeliveryPostponement{
		{OriginalDate: utcDate(2026, time.September, 3), MovedTo: utcDate(2026, time.September, 4), Note: "over and done"},
		{OriginalDate: utcDate(2026, time.September, 7), MovedTo: utcDate(2026, time.September, 10), Note: "Labor Day"},
		{OriginalDate: utcDate(2026, time.September, 14), MovedTo: utcDate(2026, time.September, 15), Note: "ahead"},
	}, loc, now)

	require.Len(t, rows, 3)

	// Both days behind us: the run happened, the row is history.
	assert.False(t, rows[0].Restorable)
	assert.Equal(t, "Already run — kept for the record.", rows[0].StatusNote)

	// The middle state — scheduled day passed, run still ahead. No button, but
	// it says why and says when the van actually goes.
	assert.False(t, rows[1].Restorable, "restoring would put orders back on a day that has gone")
	assert.Contains(t, rows[1].StatusNote, "scheduled day has passed")
	assert.Contains(t, rows[1].StatusNote, "Thursday, September 10")

	// Wholly ahead: restorable, and the button is its own explanation.
	assert.True(t, rows[2].Restorable)
	assert.Empty(t, rows[2].StatusNote, "a row with a live button needs no note")
}

// Restorable has to ask exactly what RestoreDeliveryRun's guard asks — is the
// scheduled day still ahead? A button offered on a row the service will refuse
// is a dead click; a button withheld from one it would accept strands the run.
// Today itself is still restorable: the guard refuses only a day behind us.
func TestPostponementRows_RestorableMatchesTheServiceGuard(t *testing.T) {
	loc := mustLoad(t)
	now := time.Date(2026, time.September, 9, 10, 0, 0, 0, loc)

	for _, tc := range []struct {
		name       string
		original   time.Time
		restorable bool
	}{
		{"yesterday", utcDate(2026, time.September, 8), false},
		{"today", utcDate(2026, time.September, 9), true},
		{"tomorrow", utcDate(2026, time.September, 10), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := postponementRows([]domain.DeliveryPostponement{
				{OriginalDate: tc.original, MovedTo: tc.original.AddDate(0, 0, 3)},
			}, loc, now)
			require.Len(t, rows, 1)
			assert.Equal(t, tc.restorable, rows[0].Restorable)
		})
	}
}

// Every date on the panel is the merchant's day, not UTC's. The rows arrive as
// midnight UTC, and "today" is judged from an instant that is already tomorrow
// in UTC — read either in the wrong zone and the panel shifts a run by a day,
// or withholds Restore from a run that is still ahead.
func TestPostponementRows_RenderInTheMerchantZone(t *testing.T) {
	loc := mustLoad(t)
	// 11:30pm on the 9th in Helena; already the 10th in UTC.
	lateOnTheNinth := time.Date(2026, time.September, 9, 23, 30, 0, 0, loc)
	require.Equal(t, 10, lateOnTheNinth.UTC().Day(), "fixture must straddle the date line")

	rows := postponementRows([]domain.DeliveryPostponement{
		{OriginalDate: utcDate(2026, time.September, 10), MovedTo: utcDate(2026, time.September, 11)},
	}, loc, lateOnTheNinth)

	require.Len(t, rows, 1)
	assert.Equal(t, "Thursday, September 10", rows[0].OriginalLabel)
	assert.Equal(t, "Friday, September 11", rows[0].MovedToLabel)
	assert.Equal(t, "2026-09-10", rows[0].OriginalValue, "the Restore form posts the day staff can see")
	assert.True(t, rows[0].Restorable, "tomorrow in Helena is not yesterday because UTC rolled over")
}

// A nil zone falls back to UTC rather than panicking — the same fallback the
// rest of the delivery code makes when MERCHANT_TIMEZONE is unset in dev.
func TestPostponementRows_NilZoneAndEmptyInput(t *testing.T) {
	assert.Empty(t, postponementRows(nil, nil, time.Now()))

	rows := postponementRows([]domain.DeliveryPostponement{
		{OriginalDate: utcDate(2026, time.September, 7), MovedTo: utcDate(2026, time.September, 8)},
	}, nil, utcDate(2026, time.September, 1))
	require.Len(t, rows, 1)
	assert.Equal(t, "Monday, September 7", rows[0].OriginalLabel)
	assert.True(t, rows[0].Restorable)
}
