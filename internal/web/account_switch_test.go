package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// setCookieFor returns the Set-Cookie header written for name, or "".
func setCookieFor(t *testing.T, rec *httptest.ResponseRecorder, name string) string {
	t.Helper()
	for _, c := range rec.Header().Values("Set-Cookie") {
		if strings.HasPrefix(c, name+"=") {
			return c
		}
	}
	return ""
}

// A buyer who runs two companies signs in to the second one straight from the
// switch-account form. The wholesale cart cookie is device-scoped, so unless
// authenticating drops it, company A's half-built order follows the new session
// into company B's portal — priced for a customer who is no longer signed in,
// and never re-authorized at checkout.
func TestNewCustomerSessionCookieDropsWholesaleCart(t *testing.T) {
	rec := httptest.NewRecorder()

	newCustomerSessionCookie(rec, true, "session-token", time.Hour, http.SameSiteStrictMode)

	session := setCookieFor(t, rec, customerCookieName)
	require.Contains(t, session, "session-token", "the new session cookie is written")
	require.Contains(t, session, "HttpOnly")
	require.Contains(t, session, "Secure")
	require.Contains(t, session, "SameSite=Strict")

	cart := setCookieFor(t, rec, wholesaleCartCookieName)
	require.NotEmpty(t, cart, "authenticating must expire any wholesale cart left on the device")
	require.Contains(t, cart, "Max-Age=0", "the cart cookie is expired, not carried over")
}

// Everything that ends a session routes through clearCustomerCookie: both
// logouts, the team self-revoke, and the three stale-cookie clears in the
// middleware. The cart has to leave with it, or it outlives whoever built it.
func TestClearCustomerCookieDropsWholesaleCart(t *testing.T) {
	rec := httptest.NewRecorder()

	clearCustomerCookie(rec)

	session := setCookieFor(t, rec, customerCookieName)
	require.Contains(t, session, "Max-Age=0", "session cookie is expired")

	cart := setCookieFor(t, rec, wholesaleCartCookieName)
	require.Contains(t, cart, "Max-Age=0", "wholesale cart cookie is expired alongside it")
}

// clearWholesaleCartCookie has to mirror setWholesaleCartCookie on the
// attributes browsers match a deletion against. A Path or SameSite that drifts
// leaves the original cookie in place and the expiry silently does nothing.
func TestClearWholesaleCartCookieMirrorsSetAttributes(t *testing.T) {
	set := httptest.NewRecorder()
	setWholesaleCartCookie(set, uuid.New())
	clear := httptest.NewRecorder()
	clearWholesaleCartCookie(clear)

	written := setCookieFor(t, set, wholesaleCartCookieName)
	expired := setCookieFor(t, clear, wholesaleCartCookieName)

	for _, attr := range []string{"Path=/", "HttpOnly", "SameSite=Lax"} {
		require.Contains(t, written, attr)
		require.Contains(t, expired, attr, "deletion must match the attributes the cookie was set with")
	}
}

// Signing out mid-switch should land back on the sign-in form rather than the
// home page, but the destination arrives in a form body — so it goes through
// safeNextOr like every other caller-supplied redirect.
func TestWholesaleLogoutRedirect(t *testing.T) {
	tests := []struct {
		name     string
		redirect string
		want     string
	}{
		{
			name:     "switch-account flow returns to the sign-in form",
			redirect: "/wholesale/login",
			want:     "/wholesale/login",
		},
		{
			name:     "a plain sign-out ends the visit at home",
			redirect: "",
			want:     "/",
		},
		{
			name:     "protocol-relative redirect is refused",
			redirect: "//evil.example.com",
			want:     "/",
		},
		{
			name:     "absolute redirect is refused",
			redirect: "https://evil.example.com/pwn",
			want:     "/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{}
			if tt.redirect != "" {
				form.Set("redirect", tt.redirect)
			}
			// No session cookie: the handler skips the database entirely and
			// still has to clear cookies and redirect.
			r := httptest.NewRequest(http.MethodPost, "/wholesale/logout", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()

			(&Deps{}).handleWholesaleLogout(rec, r)

			require.Equal(t, http.StatusSeeOther, rec.Code)
			require.Equal(t, tt.want, rec.Header().Get("Location"))
			require.Contains(t, setCookieFor(t, rec, wholesaleCartCookieName), "Max-Age=0")
		})
	}
}

// The label names the account a buyer is currently in, so it has to survive the
// accounts that carry no company name at all.
func TestWholesaleAccountLabel(t *testing.T) {
	company := func(s string) *string { return &s }

	tests := []struct {
		name     string
		customer *domain.Customer
		want     string
	}{
		{
			name:     "company name wins",
			customer: &domain.Customer{CompanyName: company("Ruby Diner Coffee"), FirstName: "Kara", LastName: "Vogt", Email: "kara@example.com"},
			want:     "Ruby Diner Coffee",
		},
		{
			name:     "company name is trimmed",
			customer: &domain.Customer{CompanyName: company("  Ruby Diner Coffee  ")},
			want:     "Ruby Diner Coffee",
		},
		{
			name:     "whitespace-only company falls back to the contact",
			customer: &domain.Customer{CompanyName: company("   "), FirstName: "Kara", LastName: "Vogt", Email: "kara@example.com"},
			want:     "Kara Vogt",
		},
		{
			name:     "no company falls back to the contact",
			customer: &domain.Customer{FirstName: "Kara", LastName: "Vogt", Email: "kara@example.com"},
			want:     "Kara Vogt",
		},
		{
			name:     "no name falls back to the email",
			customer: &domain.Customer{Email: "kara@example.com"},
			want:     "kara@example.com",
		},
		{
			name:     "nil customer yields nothing to render",
			customer: nil,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, wholesaleAccountLabel(tt.customer))
		})
	}
}
