package web

import (
	"net/http"
	"regexp"
	"strings"
)

// A promo link: any storefront page with ?promo=CODE, from the offer strip or
// an email. The code is held in a cookie until a checkout can use it — the
// cart checkout applies it, the subscribe page fills it in — so it survives
// the browsing between the link and the checkout.
//
// "promo", not "code": OAuth callbacks (QuickBooks, Stripe) already put
// ?code= on their own URLs.
const (
	promoParam      = "promo"
	promoCookieName = "rr_promo"
	// promoCookieMaxAge outlasts a holiday campaign without keeping a stale
	// code around into the next one.
	promoCookieMaxAge = 30 * 24 * 60 * 60
)

// promoCodePattern is what a code may look like: codes are stored uppercase,
// and minted referral codes are NAME-XXXX. Anything else is not kept, so the
// cookie never carries markup or a value checkout would choke on.
var promoCodePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9-]{0,39}$`)

// normalizePromo returns the code as checkout stores it, or "" when it could
// not be a code.
func normalizePromo(raw string) string {
	code := strings.ToUpper(strings.TrimSpace(raw))
	if !promoCodePattern.MatchString(code) {
		return ""
	}
	return code
}

// capturePromoParam keeps a promo link's code and redirects to the same page
// without it, so the address bar, shared links and analytics see the clean
// URL. Only GETs on storefront pages: the admin, the APIs and webhooks never
// take a promo link.
func capturePromoParam(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || !q.Has(promoParam) || !promoLinkPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if code := normalizePromo(q.Get(promoParam)); code != "" {
			http.SetCookie(w, &http.Cookie{
				Name:     promoCookieName,
				Value:    code,
				Path:     "/",
				MaxAge:   promoCookieMaxAge,
				HttpOnly: true,
				// Lax still sends it on the top-level navigation from an email.
				SameSite: http.SameSiteLaxMode,
			})
		}
		q.Del(promoParam)
		clean := *r.URL
		clean.RawQuery = q.Encode()
		http.Redirect(w, r, clean.RequestURI(), http.StatusFound)
	})
}

func promoLinkPath(path string) bool {
	for _, prefix := range []string{"/admin", "/api/", "/webhooks/", "/static/"} {
		if strings.HasPrefix(path, prefix) {
			return false
		}
	}
	return true
}

// pendingPromo is the code a promo link left for checkout, or "".
func pendingPromo(r *http.Request) string {
	c, err := r.Cookie(promoCookieName)
	if err != nil {
		return ""
	}
	return normalizePromo(c.Value)
}

// clearPendingPromo forgets the promo link's code: it is on the cart now, or
// checkout refused it for good.
func clearPendingPromo(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     promoCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
