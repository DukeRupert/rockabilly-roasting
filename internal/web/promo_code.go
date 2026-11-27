package web

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/logging"
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

// normalizePromo returns the code as checkout stores it, or "" when it could
// not be a code. See domain.NormalizePromoCode.
func normalizePromo(raw string) string { return domain.NormalizePromoCode(raw) }

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
		// This runs before ServeMux cleans the path, so the redirect is built
		// from what the client sent. "//evil.example/" would be a
		// protocol-relative Location, and browsers treat "/\" the same way:
		// collapse the leading slashes so it can only ever name this site.
		clean.Path = "/" + strings.TrimLeft(r.URL.Path, "/\\")
		clean.RawPath = ""
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

// clearPendingPromo forgets the promo link's code: a cart holds a code now,
// or checkout refused it in a way no checkout would accept (see
// app.CouponDeadEverywhere).
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

// pendingPromoFor is the promo link's code if this checkout should offer it:
// its discount works on this kind of order, or the code is unknown (offered,
// so checkout refuses it once and it is forgotten). A signup code is never
// put in the cart's code box, nor a cart code on the subscribe page — each
// would be refused there on every visit while it waits for the other.
// Best-effort: a failed lookup offers nothing.
func (d *Deps) pendingPromoFor(ctx context.Context, tx pgx.Tx, r *http.Request, kind domain.DiscountAppliesTo) string {
	code := pendingPromo(r)
	if code == "" {
		return ""
	}
	appliesTo, found, err := d.CheckoutService.CouponAppliesTo(ctx, tx, code)
	if err != nil {
		logging.FromContext(ctx).Warn("pending promo lookup", "error", err)
		return ""
	}
	if found && appliesTo != kind {
		return ""
	}
	return code
}
