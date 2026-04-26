package web

import (
	"net/http"
	"strings"
)

// legacyProductSlugMap maps WooCommerce product slugs (from the previous
// rockabillyroasting.com site) to current Hiri catalog slugs. Slugs not
// listed here fall back to /catalog. Used by handleLegacyProductRedirect.
var legacyProductSlugMap = map[string]string{
	// Coffee products that exist on the new site under a different slug.
	"white-coffee": "rev-it-up",

	// Coffee products with the same slug — listed for clarity so future
	// renames are caught at the redirect layer.
	"bike-blend": "bike-blend",
	"chop-top":   "chop-top",
}

// handleLegacyProductRedirect 301s old WooCommerce product URLs
// (/product/{slug}/) to the corresponding /catalog/{slug} on the new site.
// Unknown slugs redirect to /catalog so the visitor lands somewhere useful
// instead of a 404.
func (d *Deps) handleLegacyProductRedirect(w http.ResponseWriter, r *http.Request) {
	slug := strings.ToLower(r.PathValue("slug"))
	target := "/catalog"
	if newSlug, ok := legacyProductSlugMap[slug]; ok {
		target = "/catalog/" + newSlug
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

// handleLegacyCatalogRedirect 301s old WooCommerce category and shop pages
// (/product-category/{slug}/, /shop-merchandise/) to /catalog.
func (d *Deps) handleLegacyCatalogRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/catalog", http.StatusMovedPermanently)
}
