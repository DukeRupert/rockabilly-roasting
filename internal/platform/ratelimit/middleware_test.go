package ratelimit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func postLogin(t *testing.T, h http.Handler, email string, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"email": {email}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.7:4242"
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func newTestLimiter(t *testing.T) *Limiter {
	t.Helper()
	store := NewMemoryStore(time.Hour)
	t.Cleanup(store.Stop)
	return NewLimiter(store)
}

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})

func formEmail(r *http.Request) string { return r.FormValue("email") }

// Reproduces the Village Bistro lockout: failed wholesale logins plus a
// forgot-password request used to fill the shared retail bucket, so every
// sign-in form answered 429. Each scope must count on its own.
func TestAuthLimit_ScopesDoNotShareCounters(t *testing.T) {
	l := newTestLimiter(t)
	wholesale := AuthLimit(l, AuthScopeWholesale, 10, 5, time.Minute, formEmail)(okHandler)
	retail := AuthLimit(l, AuthScopeRetail, 5, 5, time.Minute, formEmail)(okHandler)

	for i := 0; i < 4; i++ {
		if rec := postLogin(t, wholesale, "kara@example.com", false); rec.Code != http.StatusOK {
			t.Fatalf("wholesale attempt %d: status %d", i+1, rec.Code)
		}
	}
	if rec := postLogin(t, retail, "kara@example.com", false); rec.Code != http.StatusOK {
		t.Fatalf("forgot-password: status %d", rec.Code)
	}
	if rec := postLogin(t, retail, "kara@example.com", false); rec.Code != http.StatusOK {
		t.Fatalf("retail login after wholesale failures: status %d, want 200", rec.Code)
	}
	if rec := postLogin(t, wholesale, "kara@example.com", false); rec.Code != http.StatusOK {
		t.Fatalf("5th wholesale attempt: status %d, want 200", rec.Code)
	}
	if rec := postLogin(t, wholesale, "kara@example.com", false); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th wholesale attempt: status %d, want 429", rec.Code)
	}
}

func TestAuthLimit_ResetIdentifierReopensAccount(t *testing.T) {
	l := newTestLimiter(t)
	h := AuthLimit(l, AuthScopeWholesale, 10, 2, time.Minute, formEmail)(okHandler)
	for i := 0; i < 2; i++ {
		postLogin(t, h, "kara@example.com", false)
	}
	if rec := postLogin(t, h, "kara@example.com", false); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429", rec.Code)
	}
	_ = l.Reset(t.Context(), AuthIdentifierKey(AuthScopeWholesale, HashIdentifier("kara@example.com")))
	if rec := postLogin(t, h, "kara@example.com", false); rec.Code != http.StatusOK {
		t.Fatalf("after reset: status %d, want 200", rec.Code)
	}
}

// htmx swaps nothing on a 4xx, so the message has to arrive as an event or
// the visitor sees a form that does nothing.
func TestTooManyRequests_HTMXCarriesMessageInTrigger(t *testing.T) {
	l := newTestLimiter(t)
	h := AuthLimit(l, AuthScopeRetail, 1, 1, 15*time.Minute, formEmail)(okHandler)
	postLogin(t, h, "kara@example.com", true)
	rec := postLogin(t, h, "kara@example.com", true)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("HX-Reswap"); got != "none" {
		t.Errorf("HX-Reswap = %q, want none", got)
	}
	if got := rec.Header().Get("HX-Retarget"); got != "" {
		t.Errorf("HX-Retarget = %q, want unset", got)
	}
	var trigger map[string]map[string]string
	if err := json.Unmarshal([]byte(rec.Header().Get("HX-Trigger")), &trigger); err != nil {
		t.Fatalf("HX-Trigger not JSON: %v", err)
	}
	msg := trigger[RateLimitedEvent]["message"]
	if !strings.Contains(msg, "15 minutes") {
		t.Errorf("message %q does not say how long to wait", msg)
	}
}

func TestTooManyRequests_PlainRequestHasNoHTMXHeaders(t *testing.T) {
	l := newTestLimiter(t)
	h := AuthLimit(l, AuthScopeRetail, 1, 1, time.Minute, formEmail)(okHandler)
	postLogin(t, h, "kara@example.com", false)
	rec := postLogin(t, h, "kara@example.com", false)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429", rec.Code)
	}
	if rec.Header().Get("HX-Trigger") != "" {
		t.Error("HX-Trigger set on a non-htmx request")
	}
	if !strings.Contains(rec.Body.String(), "wait") {
		t.Errorf("body %q does not tell the visitor to wait", rec.Body.String())
	}
}

func TestLimitedMessage(t *testing.T) {
	for _, tc := range []struct {
		secs int
		want string
	}{
		{1, "wait a minute"},
		{60, "wait a minute"},
		{61, "about 2 minutes"},
		{900, "about 15 minutes"},
	} {
		if got := limitedMessage(tc.secs); !strings.Contains(got, tc.want) {
			t.Errorf("limitedMessage(%d) = %q, want it to contain %q", tc.secs, got, tc.want)
		}
	}
}
