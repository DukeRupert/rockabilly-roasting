package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	mediapkg "github.com/dukerupert/hiri/internal/platform/media"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// handleStorefrontHome redirects / to /catalog.
func (d *Deps) handleStorefrontHome(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/catalog", http.StatusFound)
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

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
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

	// Build product cards with thumbnails and prices.
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
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props := storefront.CatalogPageProps{
		Products:    cards,
		Taxons:      taxons,
		ActiveTaxon: categorySlug,
		Search:      search,
		Page:        page,
		TotalPages:  totalPages,
		CartCount:   d.cartItemCountFromCookie(r),
	}

	if IsHTMX(r) {
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
	}

	if IsHTMX(r) {
		storefront.ProductContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.ProductPage(props).Render(ctx, w) //nolint:errcheck
}
