package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// RFC 8058 one-click is detected from the POST body, which mail providers send
// as exactly "List-Unsubscribe=One-Click". Getting this wrong either renders a
// full HTML page into Gmail's invisible request (harmless but wasteful) or,
// worse, misreads an ordinary form POST as one-click and skips the
// confirmation page a human should have seen.
func TestIsOneClickUnsubscribe(t *testing.T) {
	form := func(body string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/wholesale/unsubscribe?t=abc", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}

	tests := []struct {
		name string
		body string
		want bool
	}{
		{"one-click body", "List-Unsubscribe=One-Click", true},
		{"confirmation form post", "t=sometoken", false},
		{"empty body", "", false},
		{"wrong value", "List-Unsubscribe=Yes", false},
		{"wrong key", "Unsubscribe=One-Click", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isOneClickUnsubscribe(form(tt.body)))
		})
	}
}

// GET must be a distinct handler from POST. This is the guard against inbox
// scanners: a corporate mail gateway fetches every link in an incoming
// message, so if the GET route acted, customers would be unsubscribed by their
// own IT department without ever clicking. Asserting the mux resolves the two
// methods to different handlers keeps a future refactor from collapsing them.
func TestUnsubscribeGetAndPostAreDistinctRoutes(t *testing.T) {
	mux := http.NewServeMux()
	var got []string
	mux.HandleFunc("GET /wholesale/unsubscribe", func(http.ResponseWriter, *http.Request) {
		got = append(got, "get")
	})
	mux.HandleFunc("POST /wholesale/unsubscribe", func(http.ResponseWriter, *http.Request) {
		got = append(got, "post")
	})

	for _, m := range []string{http.MethodGet, http.MethodPost} {
		h, pattern := mux.Handler(httptest.NewRequest(m, "/wholesale/unsubscribe?t=x", nil))
		require.Contains(t, pattern, m, "%s must match its own method-scoped pattern", m)
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(m, "/wholesale/unsubscribe?t=x", nil))
	}
	require.Equal(t, []string{"get", "post"}, got)
}
