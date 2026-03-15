package web

import (
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/a-h/templ"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
	"github.com/dukerupert/hiri/internal/ui/components/toast"
	"github.com/jackc/pgx/v5"
)

var nonAlphanumDash = regexp.MustCompile(`[^\w-]+`)
var multiDash = regexp.MustCompile(`-{2,}`)

// slugify converts a string to a URL-friendly slug.
func slugify(s string) string {
	slug := strings.ToLower(strings.TrimSpace(s))
	slug = nonAlphanumDash.ReplaceAllString(slug, "-")
	slug = multiDash.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	return slug
}

// --- Product List ---

func (d *Deps) handleAdminProductList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	statusFilter := r.URL.Query().Get("status")
	taxonFilter := r.URL.Query().Get("taxon")
	search := r.URL.Query().Get("q")
	pageStr := r.URL.Query().Get("page")

	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}

	perPage := 25
	filter := store.ProductFilter{
		Search: search,
		Limit:  perPage + 1, // fetch one extra to detect "has more"
		Offset: (page - 1) * perPage,
	}

	if statusFilter != "" {
		s := domain.ProductStatus(statusFilter)
		filter.Status = &s
	}
	if taxonFilter != "" {
		if id, err := uuid.Parse(taxonFilter); err == nil {
			filter.TaxonID = &id
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
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	hasMore := len(products) > perPage
	if hasMore {
		products = products[:perPage]
	}

	taxonMap := make(map[uuid.UUID]string, len(taxons))
	for _, t := range taxons {
		taxonMap[t.ID] = t.Name
	}

	name, role := staffNameRole(r)
	props := admin.ProductListProps{
		Products:     products,
		Taxons:       taxons,
		TaxonMap:     taxonMap,
		StatusFilter: statusFilter,
		TaxonFilter:  taxonFilter,
		Search:       search,
		TotalCount:   totalCount,
		Page:         page,
		PerPage:      perPage,
		HasMore:      hasMore,
		StaffName:    name,
		StaffRole:    role,
	}
	if IsHTMX(r) {
		admin.ProductListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.ProductList(props).Render(ctx, w) //nolint:errcheck
}

// --- Create Product ---

func (d *Deps) handleAdminProductNew(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var taxons []domain.Taxon
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		taxons, txErr = d.CatalogService.ListRootTaxons(ctx, tx)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.ProductNewProps{
		Taxons:    taxons,
		StaffName: name,
		StaffRole: role,
	}
	if IsHTMX(r) {
		admin.ProductNewContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.ProductNew(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAdminProductCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	slug := r.FormValue("slug")
	if slug == "" {
		slug = slugify(r.FormValue("title"))
	}

	params := store.CreateProductParams{
		Title:       r.FormValue("title"),
		Slug:        slug,
		Description: r.FormValue("description"),
		Status:      domain.ProductStatusDraft,
	}

	if taxonID := r.FormValue("taxon_id"); taxonID != "" {
		if id, err := uuid.Parse(taxonID); err == nil {
			params.TaxonID = id
		}
	}

	if v := r.FormValue("available_on"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			params.AvailableOn = &t
		}
	}
	if v := r.FormValue("discontinue_on"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			params.DiscontinueOn = &t
		}
	}

	// Validate
	errs := map[string]string{}
	if params.Title == "" {
		errs["title"] = "Title is required"
	}
	if params.Slug == "" {
		errs["slug"] = "Slug is required"
	}

	if len(errs) > 0 {
		var taxons []domain.Taxon
		_ = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			var txErr error
			taxons, txErr = d.CatalogService.ListRootTaxons(ctx, tx)
			return txErr
		})
		p := domain.Product{
			Title:       params.Title,
			Slug:        params.Slug,
			Description: params.Description,
			TaxonID:     params.TaxonID,
		}
		n, ro := staffNameRole(r)
		props := admin.ProductNewProps{
			Product:   &p,
			Taxons:    taxons,
			Errors:    errs,
			StaffName: n,
			StaffRole: ro,
		}
		if IsHTMX(r) {
			admin.ProductNewContent(props).Render(ctx, w) //nolint:errcheck
			return
		}
		admin.ProductNew(props).Render(ctx, w) //nolint:errcheck
		return
	}

	var product *domain.Product
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		product, txErr = d.CatalogService.CreateProduct(ctx, tx, params, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Product+created", product.ID), http.StatusSeeOther)
}

// --- Edit Product ---

func (d *Deps) handleAdminProductEdit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var product *domain.Product
	var taxons []domain.Taxon
	var variants []admin.VariantWithOptions
	var options []admin.OptionWithValues
	var groups []domain.CustomerGroup
	var mediaList []domain.ProductMedia
	var assignedSets []domain.AttributeSet
	var allSets []domain.AttributeSet
	var attrValues []domain.ProductAttributeValue

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		product, txErr = d.CatalogService.GetProduct(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		taxons, txErr = d.CatalogService.ListRootTaxons(ctx, tx)
		if txErr != nil {
			return txErr
		}
		mediaList, txErr = d.CatalogService.ListProductMedia(ctx, tx, id)
		if txErr != nil {
			return txErr
		}

		// Load options + values
		opts, txErr := d.CatalogService.ListProductOptions(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		for _, opt := range opts {
			vals, vErr := d.CatalogService.ListProductOptionValues(ctx, tx, opt.ID)
			if vErr != nil {
				return vErr
			}
			options = append(options, admin.OptionWithValues{
				Option: opt,
				Values: vals,
			})
		}

		// Load variants with their option value names
		rawVariants, txErr := d.CatalogService.ListVariants(ctx, tx, id)
		if txErr != nil {
			return txErr
		}

		// Load prices for all variants at once
		priceMap, txErr := d.PricingService.ListBasePricesByProduct(ctx, tx, id, "USD")
		if txErr != nil {
			return txErr
		}

		// Load group prices
		groupPriceMap, txErr := d.PricingService.ListGroupPricesByProduct(ctx, tx, id, "USD")
		if txErr != nil {
			return txErr
		}

		for _, v := range rawVariants {
			vov, vErr := d.CatalogService.ListVariantOptionValues(ctx, tx, v.ID)
			if vErr != nil {
				return vErr
			}
			vwo := admin.VariantWithOptions{
				Variant:      v,
				OptionValues: sortedOptionValueNames(vov, options),
				GroupPrices:  groupPriceMap[v.ID],
			}
			if cents, ok := priceMap[v.ID]; ok {
				vwo.PriceCents = &cents
			}
			variants = append(variants, vwo)
		}

		// Load customer groups
		groups, txErr = d.CustomerGroupStore.List(ctx, tx)
		if txErr != nil {
			return txErr
		}

		// Load product attributes
		assignedSets, txErr = d.AttributeService.ListProductAttributeSets(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		for i := range assignedSets {
			keys, kErr := d.AttributeService.ListAttributeKeys(ctx, tx, assignedSets[i].ID)
			if kErr != nil {
				return kErr
			}
			assignedSets[i].Keys = keys
		}
		allSets, txErr = d.AttributeService.ListAttributeSets(ctx, tx)
		if txErr != nil {
			return txErr
		}
		attrValues, txErr = d.AttributeService.ListProductAttributeValues(ctx, tx, id)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	var taxonName string
	for _, t := range taxons {
		if t.ID == product.TaxonID {
			taxonName = t.Name
			break
		}
	}

	name, role := staffNameRole(r)
	props := admin.ProductEditProps{
		Product:         product,
		Taxons:          taxons,
		Variants:        variants,
		Options:         options,
		Groups:          groups,
		Media:           mediaList,
		MediaConfig:     d.MediaConfig,
		TaxonName:       taxonName,
		Flash:           r.URL.Query().Get("flash"),
		StaffName:       name,
		StaffRole:       role,
		AssignedSets:    assignedSets,
		AllSets:         allSets,
		AttributeValues: attrValues,
	}
	if IsHTMX(r) {
		admin.ProductEditContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.ProductEdit(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAdminProductUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	params := store.UpdateProductParams{
		ID:          id,
		Title:       r.FormValue("title"),
		Slug:        r.FormValue("slug"),
		Description: r.FormValue("description"),
	}

	if taxonID := r.FormValue("taxon_id"); taxonID != "" {
		if tid, err := uuid.Parse(taxonID); err == nil {
			params.TaxonID = tid
		}
	}

	if v := r.FormValue("available_on"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			params.AvailableOn = &t
		}
	}
	if v := r.FormValue("discontinue_on"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			params.DiscontinueOn = &t
		}
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CatalogService.UpdateProduct(ctx, tx, id, params, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Product+updated", id), http.StatusSeeOther)
}

func (d *Deps) handleAdminProductStatusUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	status := domain.ProductStatus(r.FormValue("status"))
	// Toggle switch sends no value when unchecked — default to draft.
	if status == "" {
		status = domain.ProductStatusDraft
	}

	var product *domain.Product
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		product, txErr = d.CatalogService.UpdateProductStatus(ctx, tx, id, status, staffActor(r))
		return txErr
	})
	if err != nil {
		if IsHTMX(r) {
			var current *domain.Product
			_ = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
				current, _ = d.CatalogService.GetProduct(ctx, tx, id)
				return nil
			})
			if current != nil {
				admin.StatusToggle(current).Render(ctx, w) //nolint:errcheck
			}
			toast.Toast(toast.VariantError, "Failed to update product status.").Render(ctx, w) //nolint:errcheck
			return
		}
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		admin.StatusToggle(product).Render(ctx, w) //nolint:errcheck
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Status+updated", id), http.StatusSeeOther)
}

func (d *Deps) handleAdminProductSubscribableUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Checkbox sends "true" when checked, nothing when unchecked.
	subscribable := r.FormValue("subscribable") == "true"

	var product *domain.Product
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		product, txErr = d.CatalogService.UpdateProductSubscribable(ctx, tx, id, subscribable, staffActor(r))
		return txErr
	})
	if err != nil {
		if IsHTMX(r) {
			// Re-fetch product to render panel with unchanged state, plus error toast.
			var current *domain.Product
			_ = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
				current, _ = d.CatalogService.GetProduct(ctx, tx, id)
				return nil
			})
			if current != nil {
				admin.SubscribablePanel(current).Render(ctx, w) //nolint:errcheck
			}
			toast.Toast(toast.VariantError, "Failed to update subscription setting.").Render(ctx, w) //nolint:errcheck
			return
		}
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		admin.SubscribablePanel(product).Render(ctx, w) //nolint:errcheck
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Subscription+setting+updated", id), http.StatusSeeOther)
}

func (d *Deps) handleAdminProductDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CatalogService.DeleteProduct(ctx, tx, id, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/catalog?flash=Product+deleted", http.StatusSeeOther)
}

// sortedOptionValueNames returns option value display names for a variant,
// ordered by the parent option's position (matching the options slice order).
func sortedOptionValueNames(vov []domain.VariantOptionValue, options []admin.OptionWithValues) []string {
	// Build a set of this variant's option value IDs
	selected := make(map[uuid.UUID]struct{}, len(vov))
	for _, link := range vov {
		selected[link.ProductOptionValueID] = struct{}{}
	}
	// Walk options in definition order, pick matching values
	var names []string
	for _, opt := range options {
		for _, val := range opt.Values {
			if _, ok := selected[val.ID]; ok {
				names = append(names, val.Value)
			}
		}
	}
	return names
}

// --- Panel helpers ---

// renderOptionsPanel re-fetches options for a product and renders the partial.
func (d *Deps) renderOptionsPanel(w http.ResponseWriter, r *http.Request, productID uuid.UUID) {
	ctx := r.Context()
	var product *domain.Product
	var options []admin.OptionWithValues
	var variants []admin.VariantWithOptions
	var groups []domain.CustomerGroup
	var taxonName string

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		product, txErr = d.CatalogService.GetProduct(ctx, tx, productID)
		if txErr != nil {
			return txErr
		}

		if product.TaxonID != uuid.Nil {
			if taxon, tErr := d.CatalogService.GetTaxon(ctx, tx, product.TaxonID); tErr == nil {
				taxonName = taxon.Name
			}
		}

		// Load options + values
		opts, txErr := d.CatalogService.ListProductOptions(ctx, tx, productID)
		if txErr != nil {
			return txErr
		}
		for _, opt := range opts {
			vals, vErr := d.CatalogService.ListProductOptionValues(ctx, tx, opt.ID)
			if vErr != nil {
				return vErr
			}
			options = append(options, admin.OptionWithValues{
				Option: opt,
				Values: vals,
			})
		}

		// Load variants for OOB swap
		rawVariants, txErr := d.CatalogService.ListVariants(ctx, tx, productID)
		if txErr != nil {
			return txErr
		}

		// Load prices for all variants at once
		priceMap, txErr := d.PricingService.ListBasePricesByProduct(ctx, tx, productID, "USD")
		if txErr != nil {
			return txErr
		}

		// Load group prices
		groupPriceMap, txErr := d.PricingService.ListGroupPricesByProduct(ctx, tx, productID, "USD")
		if txErr != nil {
			return txErr
		}

		for _, v := range rawVariants {
			vov, vErr := d.CatalogService.ListVariantOptionValues(ctx, tx, v.ID)
			if vErr != nil {
				return vErr
			}
			vwo := admin.VariantWithOptions{
				Variant:      v,
				OptionValues: sortedOptionValueNames(vov, options),
				GroupPrices:  groupPriceMap[v.ID],
			}
			if cents, ok := priceMap[v.ID]; ok {
				vwo.PriceCents = &cents
			}
			variants = append(variants, vwo)
		}

		// Load customer groups
		groups, txErr = d.CustomerGroupStore.List(ctx, tx)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	// Render options panel (primary swap target)
	admin.OptionsPanel(product, options).Render(ctx, w) //nolint:errcheck
	// Render variants panel as OOB swap so its option dropdowns stay in sync
	admin.VariantsPanel(product, variants, options, taxonName, groups, templ.Attributes{"hx-swap-oob": "outerHTML"}).Render(ctx, w) //nolint:errcheck
}

// renderVariantsPanel re-fetches variants for a product and renders the partial.
func (d *Deps) renderVariantsPanel(w http.ResponseWriter, r *http.Request, productID uuid.UUID) {
	ctx := r.Context()
	var product *domain.Product
	var variants []admin.VariantWithOptions
	var options []admin.OptionWithValues
	var groups []domain.CustomerGroup
	var taxonName string

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		product, txErr = d.CatalogService.GetProduct(ctx, tx, productID)
		if txErr != nil {
			return txErr
		}

		if product.TaxonID != uuid.Nil {
			if taxon, tErr := d.CatalogService.GetTaxon(ctx, tx, product.TaxonID); tErr == nil {
				taxonName = taxon.Name
			}
		}

		// Load options + values
		opts, txErr := d.CatalogService.ListProductOptions(ctx, tx, productID)
		if txErr != nil {
			return txErr
		}
		for _, opt := range opts {
			vals, vErr := d.CatalogService.ListProductOptionValues(ctx, tx, opt.ID)
			if vErr != nil {
				return vErr
			}
			options = append(options, admin.OptionWithValues{
				Option: opt,
				Values: vals,
			})
		}

		// Load variants with option value names
		rawVariants, txErr := d.CatalogService.ListVariants(ctx, tx, productID)
		if txErr != nil {
			return txErr
		}

		// Load prices for all variants at once
		priceMap, txErr := d.PricingService.ListBasePricesByProduct(ctx, tx, productID, "USD")
		if txErr != nil {
			return txErr
		}

		// Load group prices
		groupPriceMap, txErr := d.PricingService.ListGroupPricesByProduct(ctx, tx, productID, "USD")
		if txErr != nil {
			return txErr
		}

		for _, v := range rawVariants {
			vov, vErr := d.CatalogService.ListVariantOptionValues(ctx, tx, v.ID)
			if vErr != nil {
				return vErr
			}
			vwo := admin.VariantWithOptions{
				Variant:      v,
				OptionValues: sortedOptionValueNames(vov, options),
				GroupPrices:  groupPriceMap[v.ID],
			}
			if cents, ok := priceMap[v.ID]; ok {
				vwo.PriceCents = &cents
			}
			variants = append(variants, vwo)
		}

		// Load customer groups
		groups, txErr = d.CustomerGroupStore.List(ctx, tx)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}
	admin.VariantsPanel(product, variants, options, taxonName, groups, nil).Render(ctx, w) //nolint:errcheck
}

// --- Variants ---

func (d *Deps) handleAdminVariantCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	params := store.CreateVariantParams{
		ProductID: productID,
		SKU:       r.FormValue("sku"),
		IsDefault: r.FormValue("is_default") == "true",
	}

	if barcode := r.FormValue("barcode"); barcode != "" {
		params.Barcode = &barcode
	}
	if oz, err := strconv.ParseFloat(r.FormValue("weight_oz"), 64); err == nil && oz > 0 {
		grams := int(math.Round(oz * 28.3495))
		params.WeightGrams = &grams
	}

	// Collect option value selections from form (field names like "option_{optionID}")
	var optionValueIDs []uuid.UUID
	for key, values := range r.Form {
		if !strings.HasPrefix(key, "option_") || len(values) == 0 || values[0] == "" {
			continue
		}
		if valID, parseErr := uuid.Parse(values[0]); parseErr == nil {
			optionValueIDs = append(optionValueIDs, valID)
		}
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		if len(optionValueIDs) > 0 {
			if txErr := d.CatalogService.CheckDuplicateVariantOptions(ctx, tx, productID, optionValueIDs); txErr != nil {
				return txErr
			}
		}
		variant, txErr := d.CatalogService.CreateVariant(ctx, tx, params, staffActor(r))
		if txErr != nil {
			return txErr
		}
		for _, valID := range optionValueIDs {
			if txErr := d.CatalogService.CreateVariantOptionValue(ctx, tx, variant.ID, valID); txErr != nil {
				return txErr
			}
		}
		return nil
	})
	if err != nil {
		if IsHTMX(r) {
			// Re-render the panel so it stays visible, plus an OOB toast for the error
			d.renderVariantsPanel(w, r, productID)
			_, msg := mapError(err)
			toast.Toast(toast.VariantError, msg).Render(r.Context(), w) //nolint:errcheck
			return
		}
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		d.renderVariantsPanel(w, r, productID)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Variant+added", productID), http.StatusSeeOther)
}

func (d *Deps) handleAdminVariantUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	variantID, err := uuid.Parse(r.PathValue("variantID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	params := store.UpdateVariantParams{
		ID:        variantID,
		SKU:       r.FormValue("sku"),
		IsDefault: r.FormValue("is_default") == "true",
	}

	if barcode := r.FormValue("barcode"); barcode != "" {
		params.Barcode = &barcode
	}
	if oz, err := strconv.ParseFloat(r.FormValue("weight_oz"), 64); err == nil && oz > 0 {
		grams := int(math.Round(oz * 28.3495))
		params.WeightGrams = &grams
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CatalogService.UpdateVariant(ctx, tx, variantID, params, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		d.renderVariantsPanel(w, r, productID)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Variant+updated", productID), http.StatusSeeOther)
}

func (d *Deps) handleAdminVariantDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	variantID, err := uuid.Parse(r.PathValue("variantID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CatalogService.DeleteVariant(ctx, tx, variantID, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		d.renderVariantsPanel(w, r, productID)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Variant+deleted", productID), http.StatusSeeOther)
}

// --- Variant Pricing ---

func (d *Deps) handleAdminVariantPriceUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	variantID, err := uuid.Parse(r.PathValue("variantID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	dollars, err := strconv.ParseFloat(r.FormValue("price"), 64)
	if err != nil || dollars < 0 {
		if IsHTMX(r) {
			d.renderVariantsPanel(w, r, productID)
			toast.Toast(toast.VariantError, "Please enter a valid price").Render(r.Context(), w) //nolint:errcheck
			return
		}
		http.Error(w, "invalid price", http.StatusBadRequest)
		return
	}

	cents := int(math.Round(dollars * 100))

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.PricingService.SetBasePrice(ctx, tx, variantID, cents, "USD")
		return txErr
	})
	if err != nil {
		if IsHTMX(r) {
			d.renderVariantsPanel(w, r, productID)
			_, msg := mapError(err)
			toast.Toast(toast.VariantError, msg).Render(r.Context(), w) //nolint:errcheck
			return
		}
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		d.renderVariantsPanel(w, r, productID)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Price+updated", productID), http.StatusSeeOther)
}

func (d *Deps) handleAdminVariantGroupPriceUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	variantID, err := uuid.Parse(r.PathValue("variantID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	groupID, err := uuid.Parse(r.FormValue("group_id"))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	priceStr := r.FormValue("price")

	// Empty price = delete group price
	if priceStr == "" {
		err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			return d.PricingService.DeleteGroupPrice(ctx, tx, variantID, groupID, "USD")
		})
		if err != nil {
			Error(w, r, err)
			return
		}
		if IsHTMX(r) {
			d.renderVariantsPanel(w, r, productID)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Group+price+removed", productID), http.StatusSeeOther)
		return
	}

	dollars, err := strconv.ParseFloat(priceStr, 64)
	if err != nil || dollars < 0 {
		if IsHTMX(r) {
			d.renderVariantsPanel(w, r, productID)
			toast.Toast(toast.VariantError, "Please enter a valid price").Render(r.Context(), w) //nolint:errcheck
			return
		}
		http.Error(w, "invalid price", http.StatusBadRequest)
		return
	}

	cents := int(math.Round(dollars * 100))

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.PricingService.SetGroupPrice(ctx, tx, variantID, groupID, cents, "USD")
		return txErr
	})
	if err != nil {
		if IsHTMX(r) {
			d.renderVariantsPanel(w, r, productID)
			_, msg := mapError(err)
			toast.Toast(toast.VariantError, msg).Render(r.Context(), w) //nolint:errcheck
			return
		}
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		d.renderVariantsPanel(w, r, productID)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Group+price+updated", productID), http.StatusSeeOther)
}

// --- Options ---

func (d *Deps) handleAdminOptionCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CatalogService.CreateProductOption(ctx, tx, productID, name, 0)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		d.renderOptionsPanel(w, r, productID)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Option+added", productID), http.StatusSeeOther)
}

func (d *Deps) handleAdminOptionDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	optionID, err := uuid.Parse(r.PathValue("optionID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CatalogService.DeleteProductOption(ctx, tx, optionID)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		d.renderOptionsPanel(w, r, productID)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Option+deleted", productID), http.StatusSeeOther)
}

// --- Option Values ---

func (d *Deps) handleAdminOptionValueCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	optionID, err := uuid.Parse(r.PathValue("optionID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	value := r.FormValue("value")
	if value == "" {
		http.Error(w, "value is required", http.StatusBadRequest)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CatalogService.CreateProductOptionValue(ctx, tx, optionID, value, 0)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		d.renderOptionsPanel(w, r, productID)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Value+added", productID), http.StatusSeeOther)
}

func (d *Deps) handleAdminOptionValueDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	productID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	valueID, err := uuid.Parse(r.PathValue("valueID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CatalogService.DeleteProductOptionValue(ctx, tx, valueID)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		d.renderOptionsPanel(w, r, productID)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Value+deleted", productID), http.StatusSeeOther)
}

// --- Categories ---

func (d *Deps) handleAdminCategoryList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var taxons []domain.Taxon
	var editing *domain.Taxon

	editID := r.URL.Query().Get("edit")

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		taxons, txErr = d.CatalogService.ListRootTaxons(ctx, tx)
		if txErr != nil {
			return txErr
		}
		if editID != "" {
			if id, parseErr := uuid.Parse(editID); parseErr == nil {
				editing, txErr = d.CatalogService.GetTaxon(ctx, tx, id)
				if txErr != nil {
					return txErr
				}
			}
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.CategoryListProps{
		Taxons:    taxons,
		Flash:     r.URL.Query().Get("flash"),
		Editing:   editing,
		StaffName: name,
		StaffRole: role,
	}
	if IsHTMX(r) {
		admin.CategoryListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.CategoryList(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAdminCategoryCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	position, _ := strconv.Atoi(r.FormValue("position"))

	params := store.CreateTaxonParams{
		Name:     r.FormValue("name"),
		Slug:     r.FormValue("slug"),
		Position: position,
		Depth:    0, // root taxon
	}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CatalogService.CreateTaxon(ctx, tx, params)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/categories?flash=Category+created", http.StatusSeeOther)
}

func (d *Deps) handleAdminCategoryUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	position, _ := strconv.Atoi(r.FormValue("position"))

	params := store.UpdateTaxonParams{
		ID:       id,
		Name:     r.FormValue("name"),
		Slug:     r.FormValue("slug"),
		Position: position,
		Depth:    0,
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CatalogService.UpdateTaxon(ctx, tx, params)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/categories?flash=Category+updated", http.StatusSeeOther)
}

func (d *Deps) handleAdminCategoryDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CatalogService.DeleteTaxon(ctx, tx, id)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/categories?flash=Category+deleted", http.StatusSeeOther)
}
