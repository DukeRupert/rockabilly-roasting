package web

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
