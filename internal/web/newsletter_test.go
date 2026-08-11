package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/platform/newsletter"
)

// newsletterDeps returns Deps wired with a newsletter client pointed at a stub
// Broadwave, plus a pointer to the addresses that stub received. The handler
// touches no database, so a bare Deps is enough.
func newsletterDeps(t *testing.T, status int) (*Deps, *[]string) {
	t.Helper()

	var got []string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		got = append(got, r.FormValue("email"))
		// The key must arrive from the server's config, not the browser.
		assert.Equal(t, "test-key", r.FormValue("api_key"))
		assert.Equal(t, "test-list", r.FormValue("list"))
		w.WriteHeader(status)
	}))
	t.Cleanup(stub.Close)

	return &Deps{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Newsletter: newsletter.New(stub.URL, "test-key", "test-list"),
	}, &got
}

func postNewsletter(d *Deps, form url.Values, htmx bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/newsletter/subscribe", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	d.handleNewsletterSubscribe(rec, req)
	return rec
}

func TestNewsletterSubscribeSuccess(t *testing.T) {
	d, got := newsletterDeps(t, http.StatusOK)

	rec := postNewsletter(d, url.Values{"email": {"Fan@Example.COM"}}, true)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "on the list")
	// Address is normalized before it reaches Broadwave, matching how the rest
	// of the app keys on email.
	assert.Equal(t, []string{"fan@example.com"}, *got)
}

func TestNewsletterSubscribeNonHTMXRedirects(t *testing.T) {
	d, got := newsletterDeps(t, http.StatusOK)

	rec := postNewsletter(d, url.Values{"email": {"fan@example.com"}}, false)

	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/newsletter/thanks", rec.Header().Get("Location"))
	assert.Len(t, *got, 1)
}

// The honeypot must look exactly like success to the client while subscribing
// nobody — a bot that can tell a rejection from an acceptance just adapts.
func TestNewsletterSubscribeHoneypotIsSilent(t *testing.T) {
	d, got := newsletterDeps(t, http.StatusOK)

	rec := postNewsletter(d, url.Values{
		"email":   {"bot@example.com"},
		"website": {"http://spam.example"},
	}, true)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "on the list")
	assert.Empty(t, *got, "honeypot submission must not reach Broadwave")
}

func TestNewsletterSubscribeRejectsBadEmail(t *testing.T) {
	for _, addr := range []string{"", "   ", "not-an-email", "a@b@c.com", strings.Repeat("x", 250) + "@example.com"} {
		d, got := newsletterDeps(t, http.StatusOK)

		rec := postNewsletter(d, url.Values{"email": {addr}}, true)

		assert.Equal(t, http.StatusOK, rec.Code, addr)
		assert.Contains(t, rec.Body.String(), "look right", addr)
		assert.Empty(t, *got, "invalid address must not reach Broadwave: %q", addr)
	}
}

// A rejection from Broadwave is a user-facing "try another", not an outage.
func TestNewsletterSubscribeUpstreamRejection(t *testing.T) {
	d, _ := newsletterDeps(t, http.StatusBadRequest)

	rec := postNewsletter(d, url.Values{"email": {"fan@example.com"}}, true)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "add that address")
}

func TestNewsletterSubscribeUpstreamOutage(t *testing.T) {
	d, _ := newsletterDeps(t, http.StatusInternalServerError)

	rec := postNewsletter(d, url.Values{"email": {"fan@example.com"}}, true)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "went sideways")
}

// A disabled client (no key configured, e.g. local dev) must not error the
// form — it accepts and subscribes nobody.
func TestNewsletterSubscribeDisabledClient(t *testing.T) {
	d := &Deps{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Newsletter: newsletter.New("", "", ""),
	}

	rec := postNewsletter(d, url.Values{"email": {"fan@example.com"}}, true)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "on the list")
}

// The email is reflected into an HTML fragment, so it must be escaped.
func TestNewsletterStatusEscapes(t *testing.T) {
	rec := httptest.NewRecorder()
	newsletterStatus(rec, `<script>alert(1)</script>`, false)
	assert.NotContains(t, rec.Body.String(), "<script>")
	assert.Contains(t, rec.Body.String(), "&lt;script&gt;")
}
