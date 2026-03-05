package web

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
	"github.com/jackc/pgx/v5"
)

// devActor returns a placeholder actor until auth is wired.
func devActor() app.Actor {
	return app.Actor{
		Type: domain.AuditActorTypeStaff,
		ID:   nil,
		Name: "Dev User",
	}
}

// --- Product List ---

func (d *Deps) handleAdminProductList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	statusFilter := r.URL.Query().Get("status")
	taxonFilter := r.URL.Query().Get("taxon")
	pageStr := r.URL.Query().Get("page")

	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}

	perPage := 25
	filter := store.ProductFilter{
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

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		products, txErr = d.CatalogService.ListProducts(ctx, tx, filter)
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

	admin.ProductList(admin.ProductListProps{
		Products:     products,
		Taxons:       taxons,
		TaxonMap:     taxonMap,
		StatusFilter: statusFilter,
		TaxonFilter:  taxonFilter,
		Page:         page,
		PerPage:      perPage,
		HasMore:      hasMore,
	}).Render(ctx, w) //nolint:errcheck
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

	admin.ProductForm(admin.ProductFormProps{
		IsNew:  true,
		Taxons: taxons,
		Action: "/admin/catalog",
	}).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAdminProductCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	params := store.CreateProductParams{
		Title:       r.FormValue("title"),
		Slug:        r.FormValue("slug"),
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
		admin.ProductForm(admin.ProductFormProps{
			Product: &p,
			IsNew:   true,
			Taxons:  taxons,
			Errors:  errs,
			Action:  "/admin/catalog",
		}).Render(ctx, w) //nolint:errcheck
		return
	}

	var product *domain.Product
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		product, txErr = d.CatalogService.CreateProduct(ctx, tx, params, devActor())
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
	var variants []domain.Variant
	var options []admin.OptionWithValues

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
		variants, txErr = d.CatalogService.ListVariants(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
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
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	admin.ProductEdit(admin.ProductEditProps{
		Product:  product,
		Taxons:   taxons,
		Variants: variants,
		Options:  options,
		Flash:    r.URL.Query().Get("flash"),
	}).Render(ctx, w) //nolint:errcheck
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
		_, txErr := d.CatalogService.UpdateProduct(ctx, tx, id, params, devActor())
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

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CatalogService.UpdateProductStatus(ctx, tx, id, status, devActor())
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Status+updated", id), http.StatusSeeOther)
}

func (d *Deps) handleAdminProductDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CatalogService.DeleteProduct(ctx, tx, id)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/catalog?flash=Product+deleted", http.StatusSeeOther)
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
	if w, err := strconv.Atoi(r.FormValue("weight_grams")); err == nil && w > 0 {
		params.WeightGrams = &w
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CatalogService.CreateVariant(ctx, tx, params)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
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
	if wt, err := strconv.Atoi(r.FormValue("weight_grams")); err == nil && wt > 0 {
		params.WeightGrams = &wt
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CatalogService.UpdateVariant(ctx, tx, variantID, params)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
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
		return d.CatalogService.DeleteVariant(ctx, tx, variantID)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/catalog/%s?flash=Variant+deleted", productID), http.StatusSeeOther)
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

	admin.CategoryList(admin.CategoryListProps{
		Taxons:  taxons,
		Flash:   r.URL.Query().Get("flash"),
		Editing: editing,
	}).Render(ctx, w) //nolint:errcheck
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
