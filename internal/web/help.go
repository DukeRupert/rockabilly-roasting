package web

import (
	"net/http"

	"github.com/dukerupert/hiri/internal/ui/admin"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// --- Storefront help ---

func (d *Deps) handleHelpIndex(w http.ResponseWriter, r *http.Request) {
	props := storefront.HelpIndexProps{
		CartCount:    d.cartItemCountFromCookie(r),
		Articles:     d.HelpRegistry.TOC("storefront"),
		CanonicalURL: d.BaseURL + r.URL.Path,
	}
	if IsHTMX(r) {
		storefront.HelpIndexContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.HelpIndexPage(props).Render(r.Context(), w) //nolint:errcheck
}

func (d *Deps) handleHelpArticle(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	article, err := d.HelpRegistry.Article("storefront", slug)
	if err != nil {
		d.handleNotFoundPage(w, r)
		return
	}
	props := storefront.HelpArticleProps{
		CartCount:    d.cartItemCountFromCookie(r),
		Article:      article,
		Articles:     d.HelpRegistry.TOC("storefront"),
		CurrentSlug:  slug,
		CanonicalURL: d.BaseURL + r.URL.Path,
	}
	if IsHTMX(r) {
		storefront.HelpArticleContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.HelpArticlePage(props).Render(r.Context(), w) //nolint:errcheck
}

// --- Wholesale help ---

func (d *Deps) handleWholesaleHelpIndex(w http.ResponseWriter, r *http.Request) {
	props := storefront.WholesaleHelpIndexProps{
		CartCount: d.cartItemCountFromCookie(r),
		Articles:  d.HelpRegistry.TOC("wholesale"),
	}
	if IsHTMX(r) {
		storefront.WholesaleHelpIndexContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.WholesaleHelpIndexPage(props).Render(r.Context(), w) //nolint:errcheck
}

func (d *Deps) handleWholesaleHelpArticle(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	article, err := d.HelpRegistry.Article("wholesale", slug)
	if err != nil {
		d.handleNotFoundPage(w, r)
		return
	}
	props := storefront.WholesaleHelpArticleProps{
		CartCount:   d.cartItemCountFromCookie(r),
		Article:     article,
		Articles:    d.HelpRegistry.TOC("wholesale"),
		CurrentSlug: slug,
	}
	if IsHTMX(r) {
		storefront.WholesaleHelpArticleContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.WholesaleHelpArticlePage(props).Render(r.Context(), w) //nolint:errcheck
}

// --- Admin help ---

func (d *Deps) handleAdminHelpIndex(w http.ResponseWriter, r *http.Request) {
	name, _ := staffNameRole(r)
	props := admin.AdminHelpIndexProps{
		StaffName: name,
		Articles:  d.HelpRegistry.TOC("admin"),
	}
	if IsHTMX(r) {
		admin.AdminHelpIndexContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	admin.AdminHelpIndex(props).Render(r.Context(), w) //nolint:errcheck
}

func (d *Deps) handleAdminHelpArticle(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	name, _ := staffNameRole(r)
	article, err := d.HelpRegistry.Article("admin", slug)
	if err != nil {
		d.handleNotFoundPage(w, r)
		return
	}
	props := admin.AdminHelpArticleProps{
		StaffName:   name,
		Article:     article,
		Articles:    d.HelpRegistry.TOC("admin"),
		CurrentSlug: slug,
	}
	if IsHTMX(r) {
		admin.AdminHelpArticleContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	admin.AdminHelpArticle(props).Render(r.Context(), w) //nolint:errcheck
}
