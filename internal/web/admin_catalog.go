package web

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/a-h/templ"
	"github.com/dukerupert/hiri/internal/app"
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
	whiteLabelFilter := r.URL.Query().Get("white_label")
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

	// ?white_label=pending is the review queue: submissions still sitting in draft.
	// "Pending" is inferred rather than stored — publishing a submission (draft →
	// active) or archiving it is what takes it off the queue.
	if whiteLabelFilter == whiteLabelPending {
		yes := true
		filter.WhiteLabel = &yes
		draft := domain.ProductStatusDraft
		filter.Status = &draft
		statusFilter = "" // the status tabs and this tab are mutually exclusive
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
		WhiteLabel:   whiteLabelFilter == whiteLabelPending,
		// Same count the sidebar badge uses — already computed by withAdminBadges.
		WhiteLabelPending: domain.AdminBadgesFrom(ctx).WhiteLabelPending,
		Search:            search,
		TotalCount:        totalCount,
		Page:              page,
		PerPage:           perPage,
		HasMore:           hasMore,
		MerchantTZ:        d.MerchantTZ,
		StaffName:         name,
		StaffRole:         role,
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

// productTab returns the validated tab name from the query string.
func productTab(r *http.Request) string {
	switch r.URL.Query().Get("tab") {
	case "details", "attributes", "media", "variants", "pricing", "activity":
		return r.URL.Query().Get("tab")
	default:
		return "details"
	}
}

func (d *Deps) handleAdminProductEdit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	tab := productTab(r)

	var product *domain.Product
	var taxons []domain.Taxon
	var variants []admin.VariantWithOptions
	var options []admin.OptionWithValues
	var wholesaleCustomers []domain.Customer
	var customerAccessIDs []uuid.UUID
	var mediaList []domain.ProductMedia
	var assignedSets []domain.AttributeSet
	var allSets []domain.AttributeSet
	var attrValues []domain.ProductAttributeValue
	var taxonName string
	var priceLists []domain.PriceList
	var pricing admin.ProductPriceListPricing
	var activity []domain.AuditEntry
	var whiteLabelBase *domain.Product
	var whiteLabelBaseChoices []admin.WhiteLabelBaseOption
	var whiteLabelChildren []domain.Product

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error

		// Always load the product (needed for page header and tab bar)
		product, txErr = d.CatalogService.GetProduct(ctx, tx, id)
		if txErr != nil {
			return txErr
		}

		// Load tab-specific data
		switch tab {
		case "details":
			taxons, txErr = d.CatalogService.ListRootTaxons(ctx, tx)
			if txErr != nil {
				return txErr
			}
			// Approved wholesale customers + current grants drive the private (white-label) panel.
			wholesaleCustomers, txErr = d.CustomerService.ListCustomers(ctx, tx, store.CustomerFilter{
				AccountType:     ptrTo(domain.AccountTypeWholesale),
				WholesaleStatus: ptrTo(domain.WholesaleStatusApproved),
				Limit:           500,
			})
			if txErr != nil {
				return txErr
			}
			customerAccessIDs, txErr = d.CatalogService.ListProductCustomerAccess(ctx, tx, id)
			if txErr != nil {
				return txErr
			}
			// White-label lineage, both directions. Children are one cheap query and
			// always worth showing — they are what blocks archiving this coffee. The
			// base-choice list walks every active product's variants, so it is only
			// built for products that actually have a base to reassign.
			whiteLabelChildren, txErr = d.WhiteLabelService.ListChildren(ctx, tx, id)
			if txErr != nil {
				return txErr
			}
			if domain.IsWhiteLabelSubmission(product.Metadata) {
				choices, cErr := d.WhiteLabelService.BaseCoffeeChoices(ctx, tx)
				if cErr != nil {
					return cErr
				}
				for _, c := range choices {
					whiteLabelBaseChoices = append(whiteLabelBaseChoices, admin.WhiteLabelBaseOption{ID: c.ProductID, Title: c.Title})
				}
				if raw, ok := product.Metadata[domain.ProductMetaWhiteLabelBaseID].(string); ok {
					if baseID, pErr := uuid.Parse(raw); pErr == nil {
						// A missing base is not an error — the coffee may have been
						// deleted outright. The panel just shows no selection.
						whiteLabelBase, _ = d.CatalogService.GetProduct(ctx, tx, baseID)
					}
				}
			}
			return nil

		case "media":
			mediaList, txErr = d.CatalogService.ListProductMedia(ctx, tx, id)
			return txErr

		case "attributes":
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

		case "variants":
			options, txErr = d.loadProductOptions(ctx, tx, id)
			if txErr != nil {
				return txErr
			}
			taxons, txErr = d.CatalogService.ListRootTaxons(ctx, tx)
			if txErr != nil {
				return txErr
			}
			variants, txErr = d.loadVariantsWithPrices(ctx, tx, id, options)
			return txErr

		case "pricing":
			// The pricing tab shows base and every price list side by side, so it
			// loads the same shape the all-products matrix does rather than the
			// base-price-only variant list.
			priceLists, txErr = d.PriceListService.List(ctx, tx)
			if txErr != nil {
				return txErr
			}
			pricing, txErr = d.buildPriceListProduct(ctx, tx, *product)
			return txErr

		case "activity":
			// The product's own entries, merged with those recorded against its
			// variants — variant.* is keyed to the variant, so a price change or
			// an archived SKU never shows up under the product's resource_id.
			// Archived variants are included on purpose: pulling a SKU is exactly
			// the change someone comes here to attribute.
			activity, txErr = d.AuditQueryService.ListByResource(ctx, tx, "product", id)
			if txErr != nil {
				return txErr
			}
			variantRows, vErr := d.CatalogService.ListVariants(ctx, tx, id)
			if vErr != nil {
				return vErr
			}
			variantIDs := make([]uuid.UUID, len(variantRows))
			for i, v := range variantRows {
				variantIDs[i] = v.ID
			}
			if len(variantIDs) > 0 {
				variantEntries, aErr := d.AuditQueryService.ListByRelatedResource(ctx, tx, "variant", variantIDs, "variant.")
				if aErr != nil {
					return aErr
				}
				activity = append(activity, variantEntries...)
			}
			// Each query is ordered only within itself.
			slices.SortStableFunc(activity, func(a, b domain.AuditEntry) int {
				return b.CreatedAt.Compare(a.CreatedAt)
			})
			return nil
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	for _, t := range taxons {
		if t.ID == product.TaxonID {
			taxonName = t.Name
			break
		}
	}

	name, role := staffNameRole(r)
	props := admin.ProductEditProps{
		Product:            product,
		Taxons:             taxons,
		Variants:           variants,
		Options:            options,
		WholesaleCustomers: wholesaleCustomers,
		CustomerAccessIDs:  customerAccessIDs,
		Media:              mediaList,
		MediaConfig:        d.MediaConfig,
		TaxonName:          taxonName,
		Flash:              r.URL.Query().Get("flash"),
		StaffName:          name,
		StaffRole:          role,
		AssignedSets:       assignedSets,
		AllSets:            allSets,
		AttributeValues:    attrValues,
		ActiveTab:          tab,
		PriceLists:         priceLists,
		Pricing:            pricing,
		Activity:           activity,
		MerchantTZ:         d.MerchantTZ,

		WhiteLabelBase:        whiteLabelBase,
		WhiteLabelBaseChoices: whiteLabelBaseChoices,
		WhiteLabelChildren:    whiteLabelChildren,
	}

	// htmx request (sidebar nav or tab click via hx-boost): return page content
	if IsHTMX(r) {
		admin.ProductEditContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	// Direct URL: return full page
	admin.ProductEdit(props).Render(ctx, w) //nolint:errcheck
}

// loadProductOptions loads options with their values for a product.
func (d *Deps) loadProductOptions(ctx context.Context, tx pgx.Tx, productID uuid.UUID) ([]admin.OptionWithValues, error) {
	opts, err := d.CatalogService.ListProductOptions(ctx, tx, productID)
	if err != nil {
		return nil, err
	}
	var options []admin.OptionWithValues
	for _, opt := range opts {
		vals, vErr := d.CatalogService.ListProductOptionValues(ctx, tx, opt.ID)
		if vErr != nil {
			return nil, vErr
		}
		options = append(options, admin.OptionWithValues{
			Option: opt,
			Values: vals,
		})
	}
	return options, nil
}

// loadVariantsWithPrices loads variants with their base prices, plus the customer
// group list (used by the product's restricted-visibility controls).
// productOptionOrdering captures, for one product, the metadata needed to order
// its variants by size then grind: each option value's display name, position
// within its option, and owning option, plus an option-priority list that puts
// the "Size" option first and the remaining options (grind, etc.) in their
// configured position order. Products without a size option fall back to plain
// option-position order. Build it once per product with loadProductOptionOrdering
// and reuse across the catalog, pricing, and subscription pages.
type productOptionOrdering struct {
	labels      map[uuid.UUID]string    // option value ID -> display value
	valuePos    map[uuid.UUID]int       // option value ID -> position within its option
	valueOption map[uuid.UUID]uuid.UUID // option value ID -> owning option ID
	optionOrder []uuid.UUID             // option IDs: size first, then position order
}

func (d *Deps) loadProductOptionOrdering(ctx context.Context, tx pgx.Tx, productID uuid.UUID) (productOptionOrdering, error) {
	o := productOptionOrdering{
		labels:      map[uuid.UUID]string{},
		valuePos:    map[uuid.UUID]int{},
		valueOption: map[uuid.UUID]uuid.UUID{},
	}
	opts, err := d.CatalogService.ListProductOptions(ctx, tx, productID)
	if err != nil {
		return o, err
	}
	var sizeOptionID uuid.UUID
	for _, opt := range opts {
		if sizeOptionID == uuid.Nil && strings.Contains(strings.ToLower(opt.Name), "size") {
			sizeOptionID = opt.ID
		}
		vals, vErr := d.CatalogService.ListProductOptionValues(ctx, tx, opt.ID)
		if vErr != nil {
			return o, vErr
		}
		for _, val := range vals {
			o.labels[val.ID] = val.Value
			o.valuePos[val.ID] = val.Position
			o.valueOption[val.ID] = opt.ID
		}
	}
	if sizeOptionID != uuid.Nil {
		o.optionOrder = append(o.optionOrder, sizeOptionID)
	}
	for _, opt := range opts {
		if opt.ID != sizeOptionID {
			o.optionOrder = append(o.optionOrder, opt.ID)
		}
	}
	return o, nil
}

// variantOptionMissing ranks a variant that lacks one of the product's options
// after those that have it.
const variantOptionMissing = 1 << 30

// valueByOption maps a variant's option-value links to {option ID -> value ID}.
func (o productOptionOrdering) valueByOption(vovs []domain.VariantOptionValue) map[uuid.UUID]uuid.UUID {
	m := make(map[uuid.UUID]uuid.UUID, len(vovs))
	for _, vov := range vovs {
		if oid, ok := o.valueOption[vov.ProductOptionValueID]; ok {
			m[oid] = vov.ProductOptionValueID
		}
	}
	return m
}

// sortKey builds the size-then-grind sort key for a variant from its option
// value links. Lower keys sort first.
func (o productOptionOrdering) sortKey(vovs []domain.VariantOptionValue) []int {
	vbo := o.valueByOption(vovs)
	key := make([]int, len(o.optionOrder))
	for i, oid := range o.optionOrder {
		if valID, ok := vbo[oid]; ok {
			key[i] = o.valuePos[valID]
		} else {
			key[i] = variantOptionMissing
		}
	}
	return key
}

// label builds a size-first variant label (e.g. "12oz / Drip") from its option
// value links, following the same size-then-grind priority as sortKey.
func (o productOptionOrdering) label(vovs []domain.VariantOptionValue) string {
	vbo := o.valueByOption(vovs)
	parts := make([]string, 0, len(o.optionOrder))
	for _, oid := range o.optionOrder {
		if valID, ok := vbo[oid]; ok {
			if s := o.labels[valID]; s != "" {
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, " / ")
}

// lessVariantKey reports whether sort key a sorts strictly before b
// (lexicographic, then shorter-first). Equal keys return false both ways so
// callers can apply a stable tiebreak (e.g. SKU).
func lessVariantKey(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func (d *Deps) loadVariantsWithPrices(ctx context.Context, tx pgx.Tx, productID uuid.UUID, options []admin.OptionWithValues) ([]admin.VariantWithOptions, error) {
	rawVariants, err := d.CatalogService.ListVariants(ctx, tx, productID)
	if err != nil {
		return nil, err
	}

	priceMap, err := d.PricingService.ListBasePricesByProduct(ctx, tx, productID, "USD")
	if err != nil {
		return nil, err
	}

	ordering, err := d.loadProductOptionOrdering(ctx, tx, productID)
	if err != nil {
		return nil, err
	}

	var variants []admin.VariantWithOptions
	keys := make(map[uuid.UUID][]int, len(rawVariants))
	for _, v := range rawVariants {
		vov, vErr := d.CatalogService.ListVariantOptionValues(ctx, tx, v.ID)
		if vErr != nil {
			return nil, vErr
		}
		keys[v.ID] = ordering.sortKey(vov)
		vwo := admin.VariantWithOptions{
			Variant:      v,
			OptionValues: sortedOptionValueNames(vov, options),
		}
		if cents, ok := priceMap[v.ID]; ok {
			vwo.PriceCents = &cents
		}
		variants = append(variants, vwo)
	}
	// Order by size then grind so the pricing and variant tabs read 12oz before
	// 3lb, each grind group in its configured order, instead of catalog order.
	sortVariantsByKey(variants, keys, func(v admin.VariantWithOptions) (uuid.UUID, string) {
		return v.Variant.ID, v.Variant.SKU
	})

	return variants, nil
}

// sortVariantsByKey stably orders any slice of variant wrappers by their
// precomputed size-then-grind keys, falling back to SKU for ties. id extracts
// the variant ID (to look up its key) and SKU (the tiebreak) from each element.
func sortVariantsByKey[T any](items []T, keys map[uuid.UUID][]int, id func(T) (uuid.UUID, string)) {
	sort.SliceStable(items, func(i, j int) bool {
		idi, skui := id(items[i])
		idj, skuj := id(items[j])
		ki, kj := keys[idi], keys[idj]
		if lessVariantKey(ki, kj) {
			return true
		}
		if lessVariantKey(kj, ki) {
			return false
		}
		return skui < skuj
	})
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
			// Use the mapped message rather than a generic one: refusing to archive
			// a coffee names the white-label products still based on it, and that
			// list is the only place staff can see them.
			_, msg := mapError(err)
			toast.Toast(toast.VariantError, msg).Render(ctx, w) //nolint:errcheck
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

// handleAdminProductWhiteLabelBase repoints a white-label product at a different
// base coffee. Staff reach for this when the original base is being retired —
// archiving a coffee is refused while white-label products still name it.
func (d *Deps) handleAdminProductWhiteLabelBase(w http.ResponseWriter, r *http.Request) {
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

	baseID, err := uuid.Parse(r.FormValue("base_product_id"))
	if err != nil {
		Error(w, r, app.ErrWhiteLabelBaseInvalid)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.WhiteLabelService.ReassignBase(ctx, tx, id, baseID, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Base+coffee+updated", id), http.StatusSeeOther)
}

// handleAdminProductVisibilityUpdate sets a product's visibility tier and, when the tier
// is private, the set of customers granted access. Both writes happen in one
// transaction; switching away from private clears any existing grants.
func (d *Deps) handleAdminProductVisibilityUpdate(w http.ResponseWriter, r *http.Request) {
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

	visibility := domain.ProductVisibility(r.FormValue("visibility"))
	switch visibility {
	case domain.ProductVisibilityPublic, domain.ProductVisibilityWholesale, domain.ProductVisibilityPrivate:
		// valid
	default:
		http.Error(w, "invalid visibility", http.StatusBadRequest)
		return
	}

	// Customer grants only apply to private products; any other tier clears them.
	var customerIDs []uuid.UUID
	if visibility == domain.ProductVisibilityPrivate {
		for _, raw := range r.Form["customer_ids"] {
			cid, parseErr := uuid.Parse(raw)
			if parseErr != nil {
				continue
			}
			customerIDs = append(customerIDs, cid)
		}
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		if _, txErr := d.CatalogService.UpdateProductVisibility(ctx, tx, id, visibility, staffActor(r)); txErr != nil {
			return txErr
		}
		return d.CatalogService.SetProductCustomerAccess(ctx, tx, id, customerIDs, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Visibility+updated", id), http.StatusSeeOther)
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

func (d *Deps) handleAdminProductFeaturedUpdate(w http.ResponseWriter, r *http.Request) {
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
	featured := r.FormValue("featured") == "true"

	var product *domain.Product
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		product, txErr = d.CatalogService.UpdateProductFeatured(ctx, tx, id, featured, staffActor(r))
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
				admin.FeaturedPanel(current).Render(ctx, w) //nolint:errcheck
			}
			toast.Toast(toast.VariantError, "Failed to update featured setting.").Render(ctx, w) //nolint:errcheck
			return
		}
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		admin.FeaturedPanel(product).Render(ctx, w) //nolint:errcheck
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Featured+setting+updated", id), http.StatusSeeOther)
}

func (d *Deps) handleAdminProductClone(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var cloned *domain.Product
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		cloned, txErr = d.CatalogService.CloneProduct(ctx, tx, id, d.PricingService, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Product+cloned", cloned.ID), http.StatusSeeOther)
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

		for _, v := range rawVariants {
			vov, vErr := d.CatalogService.ListVariantOptionValues(ctx, tx, v.ID)
			if vErr != nil {
				return vErr
			}
			vwo := admin.VariantWithOptions{
				Variant:      v,
				OptionValues: sortedOptionValueNames(vov, options),
			}
			if cents, ok := priceMap[v.ID]; ok {
				vwo.PriceCents = &cents
			}
			variants = append(variants, vwo)
		}

		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	// Render options panel (primary swap target)
	admin.OptionsPanel(product, options).Render(ctx, w) //nolint:errcheck
	// Render variants panel as OOB swap so its option dropdowns stay in sync
	admin.VariantsPanel(product, variants, options, taxonName, templ.Attributes{"hx-swap-oob": "outerHTML"}).Render(ctx, w) //nolint:errcheck
}

// renderVariantsPanel re-fetches variants for a product and renders the partial.
func (d *Deps) renderVariantsPanel(w http.ResponseWriter, r *http.Request, productID uuid.UUID) {
	ctx := r.Context()
	var product *domain.Product
	var variants []admin.VariantWithOptions
	var options []admin.OptionWithValues
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

		for _, v := range rawVariants {
			vov, vErr := d.CatalogService.ListVariantOptionValues(ctx, tx, v.ID)
			if vErr != nil {
				return vErr
			}
			vwo := admin.VariantWithOptions{
				Variant:      v,
				OptionValues: sortedOptionValueNames(vov, options),
			}
			if cents, ok := priceMap[v.ID]; ok {
				vwo.PriceCents = &cents
			}
			variants = append(variants, vwo)
		}

		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}
	// Check which panel the htmx request targets
	if IsHTMX(r) && r.Header.Get("HX-Target") == "pricing-panel" {
		props, pErr := d.buildProductPricingProps(ctx, *product)
		if pErr != nil {
			Error(w, r, pErr)
			return
		}
		admin.ProductPricingPanel(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.VariantsPanel(product, variants, options, taxonName, nil).Render(ctx, w) //nolint:errcheck
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
		ProductID:          productID,
		SKU:                r.FormValue("sku"),
		IsDefault:          r.FormValue("is_default") == "true",
		RetailAvailable:    r.FormValue("retail_available") == "true",
		WholesaleAvailable: r.FormValue("wholesale_available") == "true",
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
		ID:                 variantID,
		SKU:                r.FormValue("sku"),
		IsDefault:          r.FormValue("is_default") == "true",
		RetailAvailable:    r.FormValue("retail_available") == "true",
		WholesaleAvailable: r.FormValue("wholesale_available") == "true",
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

func (d *Deps) handleAdminVariantArchive(w http.ResponseWriter, r *http.Request) {
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
		_, txErr := d.CatalogService.ArchiveVariant(ctx, tx, variantID, staffActor(r))
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
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Variant+archived", productID), http.StatusSeeOther)
}

// handleAdminVariantChannels toggles a variant's per-channel availability
// (retail/wholesale). It is a focused partial update so it never disturbs SKU, weight,
// or default status — unlike the full variant update.
func (d *Deps) handleAdminVariantChannels(w http.ResponseWriter, r *http.Request) {
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

	retail := r.FormValue("retail_available") == "true"
	wholesale := r.FormValue("wholesale_available") == "true"

	var variant *domain.Variant
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		variant, txErr = d.CatalogService.UpdateVariantChannels(ctx, tx, variantID, retail, wholesale, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	// Re-render only this variant's toggle fragment (hx-target="this"), so the rest of
	// the variant table never re-renders or reflows.
	if IsHTMX(r) {
		admin.VariantChannelToggles(productID, *variant).Render(ctx, w) //nolint:errcheck
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Variant+updated", productID), http.StatusSeeOther)
}

// handleAdminVariantWholesaleMOQ saves a variant's wholesale minimum order
// quantity and order multiple. A blank field clears that constraint.
func (d *Deps) handleAdminVariantWholesaleMOQ(w http.ResponseWriter, r *http.Request) {
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

	minQty, err := optionalPositiveInt("minimum order quantity", r.FormValue("wholesale_min_qty"))
	if err != nil {
		Error(w, r, err)
		return
	}
	multiple, err := optionalPositiveInt("order multiple", r.FormValue("wholesale_multiple"))
	if err != nil {
		Error(w, r, err)
		return
	}

	var variant *domain.Variant
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		variant, txErr = d.CatalogService.UpdateVariantWholesaleMOQ(ctx, tx, variantID, minQty, multiple, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	// Re-render just this variant's MOQ fields, like the channel toggles do, so
	// the rest of the variant table does not reflow under the operator.
	if IsHTMX(r) {
		admin.VariantMOQFields(productID, *variant).Render(ctx, w) //nolint:errcheck
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Order+rules+updated", productID), http.StatusSeeOther)
}

// optionalPositiveInt parses a form field that may be left blank. Blank means
// "no constraint" and yields nil; anything non-numeric or below 1 is an error
// rather than a silent nil, so a typo cannot quietly remove a live MOQ rule.
//
// The field name is threaded through so the error toast names the box the
// operator got wrong — there are two of them side by side.
func optionalPositiveInt(field, raw string) (*int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%s must be a whole number: %w", field, app.ErrInvalidWholesaleMOQ)
	}
	if n < 1 {
		return nil, fmt.Errorf("%s must be at least 1: %w", field, app.ErrInvalidWholesaleMOQ)
	}
	return &n, nil
}

func (d *Deps) handleAdminVariantUnarchive(w http.ResponseWriter, r *http.Request) {
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
		_, txErr := d.CatalogService.UnarchiveVariant(ctx, tx, variantID, staffActor(r))
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
	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Variant+unarchived", productID), http.StatusSeeOther)
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
