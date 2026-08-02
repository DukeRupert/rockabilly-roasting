package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// These two headers are what make Gmail and Apple Mail show a native
// unsubscribe control. Without List-Unsubscribe-Post the client renders a
// mailto/link instead of the one-click button; without List-Unsubscribe there
// is no control at all and the recipient's only exit is the spam button.
func TestReminderUnsubscribeHeaders(t *testing.T) {
	h := reminderUnsubscribeHeaders("https://example.test/wholesale/unsubscribe?t=abc.def")

	require.Equal(t, "<https://example.test/wholesale/unsubscribe?t=abc.def>", h["List-Unsubscribe"],
		"the URL must be angle-bracketed per RFC 2369")
	require.Equal(t, "List-Unsubscribe=One-Click", h["List-Unsubscribe-Post"])
}

// No signing secret means no verifiable link, so no headers either — a
// List-Unsubscribe pointing at a URL that always 400s is worse than none.
func TestReminderUnsubscribeHeadersOmittedWhenUnconfigured(t *testing.T) {
	require.Nil(t, reminderUnsubscribeHeaders(""))
}
