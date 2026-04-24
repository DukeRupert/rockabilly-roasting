package web

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/email"
	mediapkg "github.com/dukerupert/hiri/internal/platform/media"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// handleStorefrontHome renders the landing page with featured products.
func (d *Deps) handleStorefrontHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Fetch up to 5 active products: 1 for the featured banner + 4 for the grid.
	activeStatus := domain.ProductStatusActive
	filter := store.ProductFilter{
		Limit:  5,
		Offset: 0,
		Status: &activeStatus,
	}

	var products []domain.Product
	var cards []storefront.ProductCard
	var featuredHeroURL string
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		products, txErr = d.CatalogService.ListProducts(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}

		cards = make([]storefront.ProductCard, len(products))
		for i, p := range products {
			cards[i] = storefront.ProductCard{
				Product:      p,
				CurrencyCode: "USD",
			}

			media, mediaErr := d.CatalogService.ListProductMedia(ctx, tx, p.ID)
			if mediaErr != nil {
				return mediaErr
			}
			if len(media) > 0 {
				cards[i].ThumbnailURL = d.MediaConfig.ProductImageURL(media[0].R2Key, mediapkg.VariantCard)
				if i == 0 {
					featuredHeroURL = d.MediaConfig.ProductImageURL(media[0].R2Key, mediapkg.VariantHero)
				}
			}

			variants, varErr := d.CatalogService.ListVariants(ctx, tx, p.ID)
			if varErr != nil {
				return varErr
			}
			for _, v := range variants {
				if v.IsDefault {
					price, priceErr := d.PricingService.GetBasePrice(ctx, tx, v.ID, "USD")
					if priceErr == nil {
						cards[i].BasePrice = &price.Amount
					}
					break
				}
			}

			attrVals, attrErr := d.AttributeService.ListProductAttributeValues(ctx, tx, p.ID)
			if attrErr != nil {
				return attrErr
			}
			cards[i].Coffee = buildCoffeeAttrs(attrVals)
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props := storefront.HomePageProps{
		CartCount: d.cartItemCountFromCookie(r),
	}
	if len(cards) > 0 {
		props.FeaturedProduct = &cards[0]
		props.HeroImageURL = featuredHeroURL
		props.FeaturedProducts = cards // show all products in the grid, including the featured one
	}

	if IsHTMX(r) {
		storefront.HomeContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.HomePage(props).Render(ctx, w) //nolint:errcheck
}

const catalogPageSize = 12

// handleStorefrontCatalog renders the product catalog page.
func (d *Deps) handleStorefrontCatalog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	categorySlug := r.URL.Query().Get("category")
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	filter := store.ProductFilter{
		Limit:  catalogPageSize,
		Offset: (page - 1) * catalogPageSize,
		Search: search,
	}
	// Only show active products on the storefront.
	activeStatus := domain.ProductStatusActive
	filter.Status = &activeStatus

	// Load filterable attribute keys for filter UI and param validation.
	var filterableKeys []domain.AttributeKey
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		filterableKeys, txErr = d.AttributeService.ListFilterableKeys(ctx, tx)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	// Parse attribute filter params from URL.
	activeFilters := make(map[string][]string)
	filterableBySlug := make(map[string]domain.AttributeKey, len(filterableKeys))
	for _, key := range filterableKeys {
		filterableBySlug[key.Slug] = key
	}
	for slug, key := range filterableBySlug {
		raw := r.URL.Query().Get(slug)
		if raw == "" {
			continue
		}
		values := strings.Split(raw, ",")
		// Validate values against allowed list (for enum types).
		var valid []string
		for _, v := range values {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			if key.IsEnumType() && len(key.AllowedValues) > 0 {
				for _, allowed := range key.AllowedValues {
					if v == allowed {
						valid = append(valid, v)
						break
					}
				}
			} else {
				// boolean or text types — accept as-is
				valid = append(valid, v)
			}
		}
		if len(valid) == 0 {
			continue
		}
		activeFilters[slug] = valid
		af := store.AttributeFilter{KeySlug: slug}
		if key.IsMultiType() {
			af.Values = valid
		} else {
			af.Value = valid[0]
		}
		filter.Attributes = append(filter.Attributes, af)
	}

	// If filtering by category, resolve the taxon.
	if categorySlug != "" {
		var taxon *domain.Taxon
		err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			var txErr error
			taxon, txErr = d.CatalogService.GetTaxonBySlug(ctx, tx, categorySlug)
			return txErr
		})
		if err != nil {
			if errors.Is(err, app.ErrTaxonNotFound) {
				// Unknown category — show all products.
			} else {
				Error(w, r, err)
				return
			}
		}
		if taxon != nil {
			filter.TaxonID = &taxon.ID
		}
	}

	var products []domain.Product
	var taxons []domain.Taxon
	var totalCount int

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		products, txErr = d.CatalogService.ListProducts(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}
		totalCount, txErr = d.CatalogService.CountProducts(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}
		taxons, txErr = d.CatalogService.ListRootTaxons(ctx, tx)
		if txErr != nil {
			return txErr
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	totalPages := (totalCount + catalogPageSize - 1) / catalogPageSize
	if totalPages < 1 {
		totalPages = 1
	}

	// Build product cards with thumbnails, prices, and coffee attributes.
	cards := make([]storefront.ProductCard, len(products))
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		for i, p := range products {
			cards[i] = storefront.ProductCard{
				Product:      p,
				CurrencyCode: "USD",
			}

			// Get thumbnail (first media item).
			media, mediaErr := d.CatalogService.ListProductMedia(ctx, tx, p.ID)
			if mediaErr != nil {
				return mediaErr
			}
			if len(media) > 0 {
				cards[i].ThumbnailURL = d.MediaConfig.ProductImageURL(media[0].R2Key, mediapkg.VariantCard)
			}

			// Get price from default variant.
			variants, varErr := d.CatalogService.ListVariants(ctx, tx, p.ID)
			if varErr != nil {
				return varErr
			}
			for _, v := range variants {
				if v.IsDefault {
					price, priceErr := d.PricingService.GetBasePrice(ctx, tx, v.ID, "USD")
					if priceErr != nil {
						if errors.Is(priceErr, app.ErrPriceNotFound) {
							break
						}
						return priceErr
					}
					cards[i].BasePrice = &price.Amount
					break
				}
			}

			// Get coffee attributes.
			attrVals, attrErr := d.AttributeService.ListProductAttributeValues(ctx, tx, p.ID)
			if attrErr != nil {
				return attrErr
			}
			cards[i].Coffee = buildCoffeeAttrs(attrVals)
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	// Build catalog filter list for the sidebar UI (top 3 only).
	catalogFilterSlugs := []string{"roast-level", "regions", "is-decaf"}
	catalogFilters := make([]storefront.CatalogFilter, 0, len(catalogFilterSlugs))
	for _, slug := range catalogFilterSlugs {
		if key, ok := filterableBySlug[slug]; ok {
			catalogFilters = append(catalogFilters, storefront.CatalogFilter{
				Key:          key,
				ActiveValues: activeFilters[key.Slug],
			})
		}
	}

	props := storefront.CatalogPageProps{
		Products:      cards,
		Taxons:        taxons,
		ActiveTaxon:   categorySlug,
		Search:        search,
		Filters:       catalogFilters,
		ActiveFilters: activeFilters,
		Page:          page,
		TotalPages:    totalPages,
		CartCount:     d.cartItemCountFromCookie(r),
	}

	if IsHTMX(r) {
		if r.Header.Get("HX-Target") == "catalog-results" {
			// Filter click: swap just the sidebar + grid area.
			storefront.CatalogResults(props).Render(ctx, w) //nolint:errcheck
			return
		}
		storefront.CatalogContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.CatalogPage(props).Render(ctx, w) //nolint:errcheck
}

// handleSubscriptionsPage renders the subscriptions landing page.
func (d *Deps) handleSubscriptionsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	subscribable := true
	activeStatus := domain.ProductStatusActive
	filter := store.ProductFilter{
		Status:       &activeStatus,
		Subscribable: &subscribable,
		Limit:        50,
	}

	var products []domain.Product
	var plans []domain.SubscriptionPlan

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		products, txErr = d.CatalogService.ListProducts(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}
		plans, txErr = d.SubscriptionService.ListActivePlans(ctx, tx)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	// Compute max discount across plans
	maxDiscount := 0
	for _, p := range plans {
		if p.DiscountPct > maxDiscount {
			maxDiscount = p.DiscountPct
		}
	}

	cards := make([]storefront.SubscriptionProductCard, len(products))
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		for i, p := range products {
			cards[i] = storefront.SubscriptionProductCard{
				Product:     p,
				MaxDiscount: maxDiscount,
			}

			media, mediaErr := d.CatalogService.ListProductMedia(ctx, tx, p.ID)
			if mediaErr != nil {
				return mediaErr
			}
			if len(media) > 0 {
				cards[i].ThumbnailURL = d.MediaConfig.ProductImageURL(media[0].R2Key, mediapkg.VariantCard)
			}

			variants, varErr := d.CatalogService.ListVariants(ctx, tx, p.ID)
			if varErr != nil {
				return varErr
			}
			for _, v := range variants {
				if v.IsDefault {
					price, priceErr := d.PricingService.GetBasePrice(ctx, tx, v.ID, "USD")
					if priceErr != nil {
						if errors.Is(priceErr, app.ErrPriceNotFound) {
							break
						}
						return priceErr
					}
					cards[i].BasePrice = &price.Amount
					break
				}
			}

			attrVals, attrErr := d.AttributeService.ListProductAttributeValues(ctx, tx, p.ID)
			if attrErr != nil {
				return attrErr
			}
			cards[i].Coffee = buildCoffeeAttrs(attrVals)
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props := storefront.SubscriptionsPageProps{
		Products:  cards,
		Plans:     plans,
		CartCount: d.cartItemCountFromCookie(r),
	}

	if IsHTMX(r) {
		storefront.SubscriptionsContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.SubscriptionsPage(props).Render(ctx, w) //nolint:errcheck
}

// handlePrivacyPage renders the privacy policy page.
func (d *Deps) handlePrivacyPage(w http.ResponseWriter, r *http.Request) {
	props := storefront.PrivacyProps{
		CartCount: d.cartItemCountFromCookie(r),
	}
	if IsHTMX(r) {
		storefront.PrivacyContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.PrivacyPage(props).Render(r.Context(), w) //nolint:errcheck
}

// handleTermsPage renders the terms of service page.
func (d *Deps) handleTermsPage(w http.ResponseWriter, r *http.Request) {
	props := storefront.TermsProps{
		CartCount: d.cartItemCountFromCookie(r),
	}
	if IsHTMX(r) {
		storefront.TermsContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.TermsPage(props).Render(r.Context(), w) //nolint:errcheck
}

// handleAboutPage renders the about page with optional contact form success.
func (d *Deps) handleAboutPage(w http.ResponseWriter, r *http.Request) {
	props := storefront.AboutProps{
		CartCount:      d.cartItemCountFromCookie(r),
		ContactSuccess: r.URL.Query().Get("sent") == "1",
	}
	if IsHTMX(r) {
		storefront.AboutContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.AboutPage(props).Render(r.Context(), w) //nolint:errcheck
}

// handleContactSubmit processes the contact form submission and sends the email via Postmark.
func (d *Deps) handleContactSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	// Honeypot — if the hidden "website" field is filled, silently accept (bot).
	if r.FormValue("website") != "" {
		http.Redirect(w, r, "/about?sent=1", http.StatusSeeOther)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	fromEmail := strings.TrimSpace(r.FormValue("email"))
	subject := strings.TrimSpace(r.FormValue("subject"))
	message := strings.TrimSpace(r.FormValue("message"))

	// Basic validation
	if name == "" || fromEmail == "" || subject == "" || message == "" {
		props := storefront.AboutProps{
			CartCount:    d.cartItemCountFromCookie(r),
			ContactError: "Please fill in all fields.",
		}
		if IsHTMX(r) {
			storefront.AboutContent(props).Render(ctx, w) //nolint:errcheck
			return
		}
		storefront.AboutPage(props).Render(ctx, w) //nolint:errcheck
		return
	}

	// Send email to staff
	htmlBody := fmt.Sprintf(
		"<h2>Contact Form Submission</h2>"+
			"<p><strong>Name:</strong> %s</p>"+
			"<p><strong>Email:</strong> %s</p>"+
			"<p><strong>Subject:</strong> %s</p>"+
			"<hr/>"+
			"<p>%s</p>",
		html.EscapeString(name),
		html.EscapeString(fromEmail),
		html.EscapeString(subject),
		strings.ReplaceAll(html.EscapeString(message), "\n", "<br/>"),
	)
	textBody := fmt.Sprintf(
		"Contact Form Submission\n\nName: %s\nEmail: %s\nSubject: %s\n\n%s",
		name, fromEmail, subject, message,
	)

	_, err := d.Mailer.Send(ctx, email.Message{
		From:    d.EmailFrom,
		To:      d.StaffEmail,
		Subject: "Contact: " + subject,
		HTML:    htmlBody,
		Text:    textBody,
		Tag:     "contact-form",
	})
	if err != nil {
		d.Logger.Error("contact form email failed", "error", err)
		props := storefront.AboutProps{
			CartCount:    d.cartItemCountFromCookie(r),
			ContactError: "Something went wrong sending your message. Please try again.",
		}
		if IsHTMX(r) {
			storefront.AboutContent(props).Render(ctx, w) //nolint:errcheck
			return
		}
		storefront.AboutPage(props).Render(ctx, w) //nolint:errcheck
		return
	}

	// Redirect to avoid form resubmission on refresh
	http.Redirect(w, r, "/about?sent=1", http.StatusSeeOther)
}

// handleWholesaleLandingPage renders the wholesale & white label info page.
func (d *Deps) handleWholesaleLandingPage(w http.ResponseWriter, r *http.Request) {
	props := storefront.WholesaleProps{
		CartCount: d.cartItemCountFromCookie(r),
	}
	if IsHTMX(r) {
		storefront.WholesaleContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.WholesalePage(props).Render(r.Context(), w) //nolint:errcheck
}

// handleShippingPage renders the shipping & returns policy page.
func (d *Deps) handleShippingPage(w http.ResponseWriter, r *http.Request) {
	props := storefront.ShippingProps{
		CartCount: d.cartItemCountFromCookie(r),
	}
	if IsHTMX(r) {
		storefront.ShippingContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.ShippingPage(props).Render(r.Context(), w) //nolint:errcheck
}

// handleNotFoundPage renders the branded 404 page.
func (d *Deps) handleNotFoundPage(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	props := storefront.NotFoundProps{
		CartCount: d.cartItemCountFromCookie(r),
	}
	if IsHTMX(r) {
		storefront.NotFoundContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.NotFoundPage(props).Render(r.Context(), w) //nolint:errcheck
}

// handleStorefrontProduct renders a single product detail page.
func (d *Deps) handleStorefrontProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slug := r.PathValue("slug")

	var product *domain.Product
	var media []domain.ProductMedia
	var variants []domain.Variant
	var options []storefront.OptionWithValues
	var variantsWithPrices []storefront.VariantWithPrice
	var variantMap map[string]storefront.VariantOptionEntry
	var defaultPrice *int
	var plans []domain.SubscriptionPlan
	var coffeeAttrs *storefront.CoffeeAttrs

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error

		// Get product by slug.
		product, txErr = d.CatalogService.GetProductBySlug(ctx, tx, slug)
		if txErr != nil {
			return txErr
		}

		// Get media.
		media, txErr = d.CatalogService.ListProductMedia(ctx, tx, product.ID)
		if txErr != nil {
			return txErr
		}

		// Get variants with prices.
		variants, txErr = d.CatalogService.ListVariants(ctx, tx, product.ID)
		if txErr != nil {
			return txErr
		}

		variantsWithPrices = make([]storefront.VariantWithPrice, len(variants))
		for i, v := range variants {
			vwp := storefront.VariantWithPrice{
				Variant:      v,
				CurrencyCode: "USD",
			}
			price, priceErr := d.PricingService.GetBasePrice(ctx, tx, v.ID, "USD")
			if priceErr != nil {
				if !errors.Is(priceErr, app.ErrPriceNotFound) {
					return priceErr
				}
			} else {
				vwp.BasePrice = &price.Amount
				if v.IsDefault {
					defaultPrice = &price.Amount
				}
			}
			variantsWithPrices[i] = vwp
		}

		// Build variant → option value mapping for JS variant resolution.
		variantMap = make(map[string]storefront.VariantOptionEntry, len(variantsWithPrices))
		for _, vwp := range variantsWithPrices {
			vovs, vovErr := d.CatalogService.ListVariantOptionValues(ctx, tx, vwp.Variant.ID)
			if vovErr != nil {
				return vovErr
			}
			ids := make([]string, len(vovs))
			for j, vov := range vovs {
				ids[j] = vov.ProductOptionValueID.String()
			}
			variantMap[vwp.Variant.ID.String()] = storefront.VariantOptionEntry{
				OptionValueIDs: ids,
				Price:          vwp.BasePrice,
			}
		}

		// Only load subscription plans for subscribable products.
		if product.Subscribable {
			plans, txErr = d.SubscriptionService.ListActivePlans(ctx, tx)
			if txErr != nil {
				return txErr
			}
		}

		// Get options with values.
		opts, optErr := d.CatalogService.ListProductOptions(ctx, tx, product.ID)
		if optErr != nil {
			return optErr
		}

		options = make([]storefront.OptionWithValues, len(opts))
		for i, opt := range opts {
			values, valErr := d.CatalogService.ListProductOptionValues(ctx, tx, opt.ID)
			if valErr != nil {
				return valErr
			}
			options[i] = storefront.OptionWithValues{
				Option: opt,
				Values: values,
			}
		}

		// Get coffee attributes.
		attrVals, attrErr := d.AttributeService.ListProductAttributeValues(ctx, tx, product.ID)
		if attrErr != nil {
			return attrErr
		}
		coffeeAttrs = buildCoffeeAttrs(attrVals)

		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props := storefront.ProductDetailProps{
		Product:           product,
		Media:             media,
		MediaConfig:       d.MediaConfig,
		Variants:          variantsWithPrices,
		Options:           options,
		VariantMap:        variantMap,
		DefaultPrice:      defaultPrice,
		CurrencyCode:      "USD",
		CartCount:         d.cartItemCountFromCookie(r),
		SubscriptionPlans: plans,
		Coffee:            coffeeAttrs,
	}

	if IsHTMX(r) {
		storefront.ProductContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.ProductPage(props).Render(ctx, w) //nolint:errcheck
}

// handleRobotsTxt serves the robots.txt file.
func (d *Deps) handleRobotsTxt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	sitemapURL := d.BaseURL + "/sitemap.xml"
	body := "User-agent: *\nAllow: /\n\n" +
		"Disallow: /admin/\n" +
		"Disallow: /account/\n" +
		"Disallow: /cart\n" +
		"Disallow: /checkout\n" +
		"Disallow: /api/\n" +
		"Disallow: /auth/\n" +
		"Disallow: /webhooks/\n" +
		"Disallow: /wholesale/portal\n" +
		"Disallow: /wholesale/checkout\n\n" +
		"Sitemap: " + sitemapURL + "\n"
	w.Write([]byte(body)) //nolint:errcheck
}

// handleSitemapXML serves a sitemap with static pages and product URLs.
func (d *Deps) handleSitemapXML(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	base := strings.TrimRight(d.BaseURL, "/")

	type sitemapURL struct {
		Loc        string
		ChangeFreq string
		Priority   string
	}

	// Static pages
	urls := []sitemapURL{
		{Loc: base + "/", ChangeFreq: "weekly", Priority: "1.0"},
		{Loc: base + "/catalog", ChangeFreq: "daily", Priority: "0.9"},
		{Loc: base + "/subscriptions", ChangeFreq: "weekly", Priority: "0.8"},
		{Loc: base + "/wholesale", ChangeFreq: "monthly", Priority: "0.6"},
		{Loc: base + "/about", ChangeFreq: "monthly", Priority: "0.5"},
		{Loc: base + "/shipping", ChangeFreq: "monthly", Priority: "0.4"},
		{Loc: base + "/privacy", ChangeFreq: "yearly", Priority: "0.2"},
		{Loc: base + "/terms", ChangeFreq: "yearly", Priority: "0.2"},
	}

	// Add active product pages
	activeStatus := domain.ProductStatusActive
	filter := store.ProductFilter{
		Status: &activeStatus,
		Limit:  500,
	}
	var products []domain.Product
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		products, txErr = d.CatalogService.ListProducts(ctx, tx, filter)
		return txErr
	})
	if err != nil {
		d.Logger.Error("sitemap: failed to list products", "error", err)
		// Continue with static pages only
	}
	for _, p := range products {
		urls = append(urls, sitemapURL{
			Loc:        base + "/catalog/" + p.Slug,
			ChangeFreq: "weekly",
			Priority:   "0.8",
		})
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Fprint(w, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for _, u := range urls {
		fmt.Fprintf(w, `<url><loc>%s</loc><changefreq>%s</changefreq><priority>%s</priority></url>`, u.Loc, u.ChangeFreq, u.Priority)
	}
	fmt.Fprint(w, `</urlset>`)
}

// buildCoffeeAttrs converts product attribute values into a CoffeeAttrs for template rendering.
// Returns nil if no coffee-related attributes are found.
func buildCoffeeAttrs(vals []domain.ProductAttributeValue) *storefront.CoffeeAttrs {
	if len(vals) == 0 {
		return nil
	}
	attrs := &storefront.CoffeeAttrs{}
	found := false
	for _, v := range vals {
		switch v.KeySlug {
		case "roast-level":
			if v.Value != nil {
				attrs.RoastLevel = *v.Value
				found = true
			}
		case "process":
			if v.Value != nil {
				attrs.Process = *v.Value
				found = true
			}
		case "origin-type":
			if v.Value != nil {
				attrs.OriginType = *v.Value
				found = true
			}
		case "regions":
			if len(v.Values) > 0 {
				attrs.Regions = v.Values
				found = true
			}
		case "tasting-notes":
			if len(v.Values) > 0 {
				attrs.TastingNotes = v.Values
				found = true
			}
		case "body":
			if v.Value != nil {
				attrs.Body = *v.Value
				found = true
			}
		case "acidity":
			if v.Value != nil {
				attrs.Acidity = *v.Value
				found = true
			}
		case "sweetness":
			if v.Value != nil {
				attrs.Sweetness = *v.Value
				found = true
			}
		case "finish":
			if v.Value != nil {
				attrs.Finish = *v.Value
				found = true
			}
		case "brew-methods":
			if len(v.Values) > 0 {
				attrs.BrewMethods = v.Values
				found = true
			}
		case "caffeine-level":
			if v.Value != nil && *v.Value == "decaf" {
				attrs.IsDecaf = true
				found = true
			}
		case "seasonal":
			if v.BoolValue() {
				attrs.IsSeasonal = true
				found = true
			}
		case "certifications":
			if len(v.Values) > 0 {
				attrs.Certifications = v.Values
				found = true
			}
		}
	}
	if !found {
		return nil
	}
	return attrs
}
