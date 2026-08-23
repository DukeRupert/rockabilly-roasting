package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// CatalogStore provides database access for products, variants, options, taxons, and media.
type CatalogStore struct{}

// NewCatalogStore creates a new CatalogStore.
func NewCatalogStore() *CatalogStore {
	return &CatalogStore{}
}

// --- Products ---

// CreateProductParams holds the fields needed to create a product.
type CreateProductParams struct {
	Slug          string
	Title         string
	Description   string
	Status        domain.ProductStatus
	ProductTypeID *uuid.UUID
	TaxonID       uuid.UUID
	Metadata      map[string]any
	AvailableOn   *time.Time
	DiscontinueOn *time.Time
}

// CreateProduct inserts a new product and returns it.
func (s *CatalogStore) CreateProduct(ctx context.Context, tx pgx.Tx, p CreateProductParams) (*domain.Product, error) {
	taxonID := &p.TaxonID
	if p.TaxonID == uuid.Nil {
		taxonID = nil
	}
	row, err := sqlcgen.New(tx).CreateProduct(ctx, sqlcgen.CreateProductParams{
		ID:            uuid.New(),
		Slug:          p.Slug,
		Title:         p.Title,
		Description:   p.Description,
		Status:        string(p.Status),
		ProductTypeID: p.ProductTypeID,
		TaxonID:       taxonID,
		Metadata:      metadataToJSON(p.Metadata),
		AvailableOn:   timestampToPG(p.AvailableOn),
		DiscontinueOn: timestampToPG(p.DiscontinueOn),
	})
	if err != nil {
		return nil, fmt.Errorf("insert product: %w", err)
	}
	return productFromRow(row), nil
}

// GetProductByID returns a product by ID.
func (s *CatalogStore) GetProductByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Product, error) {
	row, err := sqlcgen.New(tx).GetProductByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get product %s: %w", id, err)
	}
	return productFromRow(row), nil
}

// GetProductBySlug returns a product by slug.
func (s *CatalogStore) GetProductBySlug(ctx context.Context, tx pgx.Tx, slug string) (*domain.Product, error) {
	row, err := sqlcgen.New(tx).GetProductBySlug(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("get product by slug: %w", err)
	}
	return productFromRow(row), nil
}

// UpdateProductParams holds the fields to update a product.
type UpdateProductParams struct {
	ID            uuid.UUID
	Slug          string
	Title         string
	Description   string
	ProductTypeID *uuid.UUID
	TaxonID       uuid.UUID
	Metadata      map[string]any
	AvailableOn   *time.Time
	DiscontinueOn *time.Time
}

// UpdateProduct updates a product and returns it.
func (s *CatalogStore) UpdateProduct(ctx context.Context, tx pgx.Tx, p UpdateProductParams) (*domain.Product, error) {
	taxonID := &p.TaxonID
	if p.TaxonID == uuid.Nil {
		taxonID = nil
	}
	row, err := sqlcgen.New(tx).UpdateProduct(ctx, sqlcgen.UpdateProductParams{
		ID:            p.ID,
		Slug:          p.Slug,
		Title:         p.Title,
		Description:   p.Description,
		ProductTypeID: p.ProductTypeID,
		TaxonID:       taxonID,
		Metadata:      metadataToJSON(p.Metadata),
		AvailableOn:   timestampToPG(p.AvailableOn),
		DiscontinueOn: timestampToPG(p.DiscontinueOn),
	})
	if err != nil {
		return nil, fmt.Errorf("update product: %w", err)
	}
	return productFromRow(row), nil
}

// UpdateProductStatus updates a product's status and returns it.
func (s *CatalogStore) UpdateProductStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.ProductStatus) (*domain.Product, error) {
	row, err := sqlcgen.New(tx).UpdateProductStatus(ctx, sqlcgen.UpdateProductStatusParams{
		ID:     id,
		Status: string(status),
	})
	if err != nil {
		return nil, fmt.Errorf("update product status: %w", err)
	}
	return productFromRow(row), nil
}

// UpdateProductSubscribable updates whether a product is eligible for subscriptions.
func (s *CatalogStore) UpdateProductSubscribable(ctx context.Context, tx pgx.Tx, id uuid.UUID, subscribable bool) (*domain.Product, error) {
	row, err := sqlcgen.New(tx).UpdateProductSubscribable(ctx, sqlcgen.UpdateProductSubscribableParams{
		ID:           id,
		Subscribable: subscribable,
	})
	if err != nil {
		return nil, fmt.Errorf("update product subscribable: %w", err)
	}
	return productFromRow(row), nil
}

// UpdateProductVisibility updates a product's visibility and returns it.
func (s *CatalogStore) UpdateProductVisibility(ctx context.Context, tx pgx.Tx, id uuid.UUID, visibility domain.ProductVisibility) (*domain.Product, error) {
	row, err := sqlcgen.New(tx).UpdateProductVisibility(ctx, sqlcgen.UpdateProductVisibilityParams{
		ID:         id,
		Visibility: string(visibility),
	})
	if err != nil {
		return nil, fmt.Errorf("update product visibility: %w", err)
	}
	return productFromRow(row), nil
}

// SetProductCustomerVisibility grants a customer access to a private product.
func (s *CatalogStore) SetProductCustomerVisibility(ctx context.Context, tx pgx.Tx, productID, customerID uuid.UUID) error {
	if err := sqlcgen.New(tx).SetProductCustomerVisibility(ctx, sqlcgen.SetProductCustomerVisibilityParams{
		ProductID:  productID,
		CustomerID: customerID,
	}); err != nil {
		return fmt.Errorf("set product customer visibility: %w", err)
	}
	return nil
}

// RemoveProductCustomerVisibility revokes a customer's access to a private product.
func (s *CatalogStore) RemoveProductCustomerVisibility(ctx context.Context, tx pgx.Tx, productID, customerID uuid.UUID) error {
	if err := sqlcgen.New(tx).RemoveProductCustomerVisibility(ctx, sqlcgen.RemoveProductCustomerVisibilityParams{
		ProductID:  productID,
		CustomerID: customerID,
	}); err != nil {
		return fmt.Errorf("remove product customer visibility: %w", err)
	}
	return nil
}

// ListProductCustomerVisibility returns the customer IDs granted access to a private product.
func (s *CatalogStore) ListProductCustomerVisibility(ctx context.Context, tx pgx.Tx, productID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := sqlcgen.New(tx).ListProductCustomerVisibility(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("list product customer visibility: %w", err)
	}
	return ids, nil
}

// UpdateProductTaxExempt updates whether a product is tax exempt.
func (s *CatalogStore) UpdateProductTaxExempt(ctx context.Context, tx pgx.Tx, id uuid.UUID, taxExempt bool) (*domain.Product, error) {
	row, err := sqlcgen.New(tx).UpdateProductTaxExempt(ctx, sqlcgen.UpdateProductTaxExemptParams{
		ID:        id,
		TaxExempt: taxExempt,
	})
	if err != nil {
		return nil, fmt.Errorf("update product tax exempt: %w", err)
	}
	return productFromRow(row), nil
}

// UpdateProductFeatured updates whether a product is featured on the storefront home page.
func (s *CatalogStore) UpdateProductFeatured(ctx context.Context, tx pgx.Tx, id uuid.UUID, isFeatured bool) (*domain.Product, error) {
	row, err := sqlcgen.New(tx).UpdateProductFeatured(ctx, sqlcgen.UpdateProductFeaturedParams{
		ID:         id,
		IsFeatured: isFeatured,
	})
	if err != nil {
		return nil, fmt.Errorf("update product featured: %w", err)
	}
	return productFromRow(row), nil
}

// ClearOtherFeaturedProducts unsets is_featured on every product other than the given ID.
// Used to enforce the "single featured product" invariant when promoting a new one.
func (s *CatalogStore) ClearOtherFeaturedProducts(ctx context.Context, tx pgx.Tx, keepID uuid.UUID) error {
	if err := sqlcgen.New(tx).ClearOtherFeaturedProducts(ctx, keepID); err != nil {
		return fmt.Errorf("clear other featured products: %w", err)
	}
	return nil
}

// DeleteProduct removes a product by ID.
func (s *CatalogStore) DeleteProduct(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteProduct(ctx, id); err != nil {
		return fmt.Errorf("delete product: %w", err)
	}
	return nil
}

// VisibilityContext holds the viewer's context for product visibility filtering.
// CustomerID (nil for anonymous viewers) gates 'private' products: a private product
// is visible only to the customers explicitly granted access.
type VisibilityContext struct {
	IsWholesale bool
	CustomerID  *uuid.UUID
}

// ProductFilter holds optional filters for listing products.
// AttributeFilter represents a single facet filter on a product attribute.
// For enum/boolean: Value is set.
// For multi_enum: Values is set (OR logic — matches products with ANY of the values).
type AttributeFilter struct {
	KeySlug string
	Value   string   // for enum/boolean
	Values  []string // for multi_enum (OR)
}

type ProductFilter struct {
	Status       *domain.ProductStatus
	TaxonID      *uuid.UUID
	Subscribable *bool
	IsFeatured   *bool
	Visibility   *VisibilityContext
	// WhiteLabel filters on the wholesale white-label onboarding metadata stamp:
	// true selects only submissions, false only everything else.
	WhiteLabel *bool
	Search     string // ILIKE search on title and description
	Attributes []AttributeFilter
	Limit      int
	Offset     int
}

// visibilityClause returns the SQL predicate restricting products to those the given
// viewer may access, with positional params starting at argStart. It is the single
// source of truth for the access rule — shared by productFilterWhere (set reads) and
// the IsProductAccessible/IsVariantAccessible single-row checks, so a listing and a
// scalar check can never disagree.
//
// The fragment references the products table by name (products.visibility, products.id),
// so any query embedding it MUST expose the products table unaliased.
//
// The predicate is the OR of: public always; wholesale when the viewer is wholesale;
// and private products granted to the viewer's CustomerID. Positional params are
// allocated starting at argStart in the order they are appended to the returned args
// slice.
func visibilityClause(vc *VisibilityContext, argStart int) (string, []any) {
	if vc == nil {
		return "", nil
	}

	preds := []string{"products.visibility = 'public'"}
	var args []any
	argN := argStart

	if vc.IsWholesale {
		// The wholesale tier is open to every approved wholesale customer.
		preds = append(preds, "products.visibility = 'wholesale'")
	}

	if vc.CustomerID != nil {
		// 'private' products are visible only to customers explicitly granted access.
		preds = append(preds, fmt.Sprintf(`(products.visibility = 'private' AND EXISTS (
			SELECT 1 FROM product_customer_visibility pcv
			WHERE pcv.product_id = products.id
			AND pcv.customer_id = $%d
		))`, argN))
		args = append(args, *vc.CustomerID)
		argN++
	}

	if len(preds) == 1 {
		return " AND products.visibility = 'public'", args
	}
	return " AND (\n\t\t" + strings.Join(preds, "\n\t\tOR ") + "\n\t)", args
}

// IsProductAccessible reports whether the product is visible to the given viewer.
// It returns true iff the product would appear in a ListProducts filtered by the same
// VisibilityContext (shared predicate via visibilityClause).
func (s *CatalogStore) IsProductAccessible(ctx context.Context, tx pgx.Tx, productID uuid.UUID, vc VisibilityContext) (bool, error) {
	clause, visArgs := visibilityClause(&vc, 2)
	query := "SELECT EXISTS(SELECT 1 FROM products WHERE id = $1" + clause + ")"
	args := append([]any{productID}, visArgs...)
	var ok bool
	if err := tx.QueryRow(ctx, query, args...).Scan(&ok); err != nil {
		return false, fmt.Errorf("is product accessible: %w", err)
	}
	return ok, nil
}

// IsVariantAccessible reports whether the variant's product is visible to the given
// viewer, using the same access predicate as IsProductAccessible.
func (s *CatalogStore) IsVariantAccessible(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, vc VisibilityContext) (bool, error) {
	clause, visArgs := visibilityClause(&vc, 2)
	query := "SELECT EXISTS(SELECT 1 FROM products JOIN variants v ON v.product_id = products.id WHERE v.id = $1" + clause + ")"
	args := append([]any{variantID}, visArgs...)
	var ok bool
	if err := tx.QueryRow(ctx, query, args...).Scan(&ok); err != nil {
		return false, fmt.Errorf("is variant accessible: %w", err)
	}
	return ok, nil
}

// productFilterWhere builds the shared WHERE clause for product listing and counting.
// Returns the clause fragment (starting with " AND ...") and the positional args.
func productFilterWhere(f ProductFilter) (string, []any, int) {
	where := ""
	args := []any{}
	argN := 1

	if f.Status != nil {
		where += fmt.Sprintf(" AND status = $%d", argN)
		args = append(args, string(*f.Status))
		argN++
	}
	if f.TaxonID != nil {
		where += fmt.Sprintf(" AND taxon_id = $%d", argN)
		args = append(args, *f.TaxonID)
		argN++
	}
	if f.Subscribable != nil {
		where += fmt.Sprintf(" AND subscribable = $%d", argN)
		args = append(args, *f.Subscribable)
		argN++
	}
	if f.IsFeatured != nil {
		where += fmt.Sprintf(" AND is_featured = $%d", argN)
		args = append(args, *f.IsFeatured)
		argN++
	}
	if f.WhiteLabel != nil {
		// metadata->>'source' is NULL for every product that predates the flow, so
		// the negative arm needs IS DISTINCT FROM to keep those rows.
		if *f.WhiteLabel {
			where += fmt.Sprintf(" AND metadata->>'source' = $%d", argN)
		} else {
			where += fmt.Sprintf(" AND metadata->>'source' IS DISTINCT FROM $%d", argN)
		}
		args = append(args, domain.ProductSourceWhiteLabel)
		argN++
	}
	if f.Search != "" {
		where += fmt.Sprintf(" AND (title ILIKE $%d OR description ILIKE $%d)", argN, argN)
		args = append(args, "%"+f.Search+"%")
		argN++
	}
	if f.Visibility != nil {
		clause, visArgs := visibilityClause(f.Visibility, argN)
		where += clause
		args = append(args, visArgs...)
		argN += len(visArgs)
	}
	for _, af := range f.Attributes {
		if len(af.Values) > 0 {
			// multi_enum: match products that have ANY of the given values
			where += fmt.Sprintf(` AND EXISTS (
				SELECT 1 FROM product_attribute_values pav
				JOIN attribute_keys ak ON ak.id = pav.attribute_key_id
				WHERE pav.product_id = products.id
				AND ak.slug = $%d
				AND pav.values ?| $%d
			)`, argN, argN+1)
			args = append(args, af.KeySlug, af.Values)
			argN += 2
		} else if af.Value != "" {
			// enum/boolean: exact match on single value
			where += fmt.Sprintf(` AND EXISTS (
				SELECT 1 FROM product_attribute_values pav
				JOIN attribute_keys ak ON ak.id = pav.attribute_key_id
				WHERE pav.product_id = products.id
				AND ak.slug = $%d
				AND pav.value = $%d
			)`, argN, argN+1)
			args = append(args, af.KeySlug, af.Value)
			argN += 2
		}
	}

	return where, args, argN
}

// ListProducts returns products matching the given filter (hand-written for dynamic WHERE).
func (s *CatalogStore) ListProducts(ctx context.Context, tx pgx.Tx, f ProductFilter) ([]domain.Product, error) {
	where, args, argN := productFilterWhere(f)

	query := `SELECT ` + productColumns + `
	          FROM products WHERE true` + where

	query += " ORDER BY created_at DESC"

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	query += fmt.Sprintf(" LIMIT $%d", argN)
	args = append(args, limit)
	argN++

	if f.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argN)
		args = append(args, f.Offset)
	}

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	defer rows.Close()

	return scanProducts(rows)
}

// productColumns is the select list every product listing scans, in the order
// scanProducts expects. Kept as one constant so a listing and the scanner can
// never drift apart.
const productColumns = `id, slug, title, description, status, product_type_id, taxon_id,
	subscribable, visibility, tax_exempt, is_featured, metadata, available_on, discontinue_on,
	created_at, updated_at`

// scanProducts drains rows selected with productColumns into domain products.
func scanProducts(rows pgx.Rows) ([]domain.Product, error) {
	var products []domain.Product
	for rows.Next() {
		var p domain.Product
		var status, visibility string
		var productTypeID, taxonID *uuid.UUID
		var metadata []byte
		var availableOn, discontinueOn pgtype.Timestamptz
		if err := rows.Scan(
			&p.ID, &p.Slug, &p.Title, &p.Description, &status, &productTypeID, &taxonID,
			&p.Subscribable, &visibility, &p.TaxExempt, &p.IsFeatured, &metadata, &availableOn, &discontinueOn, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan product: %w", err)
		}
		p.Status = domain.ProductStatus(status)
		p.Visibility = domain.ProductVisibility(visibility)
		p.ProductTypeID = productTypeID
		if taxonID != nil {
			p.TaxonID = *taxonID
		}
		p.Metadata = metadataFromJSON(metadata)
		p.AvailableOn = timestampFromPG(availableOn)
		p.DiscontinueOn = timestampFromPG(discontinueOn)
		products = append(products, p)
	}
	return products, rows.Err()
}

// ListWhiteLabelChildren returns the live white-label products based on the given
// coffee — submissions stamped with its base_product_id — excluding ones already
// archived. Deliberately unpaginated: this backs the check that blocks archiving a
// base coffee, and a truncated list there would be a wrong answer, not a short one.
func (s *CatalogStore) ListWhiteLabelChildren(ctx context.Context, tx pgx.Tx, baseID uuid.UUID) ([]domain.Product, error) {
	query := `SELECT ` + productColumns + `
	          FROM products
	          WHERE metadata->>'` + domain.ProductMetaSource + `' = $1
	            AND metadata->>'` + domain.ProductMetaWhiteLabelBaseID + `' = $2
	            AND status <> $3
	          ORDER BY title`

	rows, err := tx.Query(ctx, query,
		domain.ProductSourceWhiteLabel, baseID.String(), string(domain.ProductStatusArchived))
	if err != nil {
		return nil, fmt.Errorf("list white-label children: %w", err)
	}
	defer rows.Close()

	return scanProducts(rows)
}

// SetProductWhiteLabelBase repoints a white-label product at a different base
// coffee, rewriting only that one metadata key so the source stamp, the customer
// stamp, and anything else staff have put in metadata survive.
func (s *CatalogStore) SetProductWhiteLabelBase(ctx context.Context, tx pgx.Tx, productID, baseID uuid.UUID) (*domain.Product, error) {
	query := `UPDATE products
	          SET metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), $2, to_jsonb($3::text), true),
	              updated_at = now()
	          WHERE id = $1
	          RETURNING ` + productColumns

	rows, err := tx.Query(ctx, query, productID,
		"{"+domain.ProductMetaWhiteLabelBaseID+"}", baseID.String())
	if err != nil {
		return nil, fmt.Errorf("set white-label base: %w", err)
	}
	defer rows.Close()

	products, err := scanProducts(rows)
	if err != nil {
		return nil, err
	}
	if len(products) == 0 {
		return nil, pgx.ErrNoRows
	}
	return &products[0], nil
}

// CountProducts returns the total number of products matching the filter (for pagination).
func (s *CatalogStore) CountProducts(ctx context.Context, tx pgx.Tx, f ProductFilter) (int, error) {
	where, args, _ := productFilterWhere(f)
	query := `SELECT COUNT(*) FROM products WHERE true` + where

	var count int
	if err := tx.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count products: %w", err)
	}
	return count, nil
}

// --- Variants ---

// CreateVariantParams holds the fields needed to create a variant.
type CreateVariantParams struct {
	ProductID          uuid.UUID
	SKU                string
	Barcode            *string
	Position           int
	IsDefault          bool
	WeightGrams        *int
	RetailAvailable    bool
	WholesaleAvailable bool
	Metadata           map[string]any
}

// CreateVariant inserts a new variant and returns it.
func (s *CatalogStore) CreateVariant(ctx context.Context, tx pgx.Tx, p CreateVariantParams) (*domain.Variant, error) {
	row, err := sqlcgen.New(tx).CreateVariant(ctx, sqlcgen.CreateVariantParams{
		ID:                 uuid.New(),
		ProductID:          p.ProductID,
		Sku:                p.SKU,
		Barcode:            p.Barcode,
		Position:           int32(p.Position),
		IsDefault:          p.IsDefault,
		WeightGrams:        intPtrToInt32Ptr(p.WeightGrams),
		RetailAvailable:    p.RetailAvailable,
		WholesaleAvailable: p.WholesaleAvailable,
		Metadata:           metadataToJSON(p.Metadata),
	})
	if err != nil {
		return nil, fmt.Errorf("insert variant: %w", err)
	}
	return variantFromRow(row), nil
}

// GetVariantByID returns a variant by ID.
func (s *CatalogStore) GetVariantByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Variant, error) {
	row, err := sqlcgen.New(tx).GetVariantByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get variant %s: %w", id, err)
	}
	return variantFromRow(row), nil
}

// GetVariantBySKU returns a variant by SKU.
func (s *CatalogStore) GetVariantBySKU(ctx context.Context, tx pgx.Tx, sku string) (*domain.Variant, error) {
	row, err := sqlcgen.New(tx).GetVariantBySKU(ctx, sku)
	if err != nil {
		return nil, fmt.Errorf("get variant by sku: %w", err)
	}
	return variantFromRow(row), nil
}

// SearchVariants returns non-archived variants whose SKU or product title match
// the query (case-insensitive substring), flattened with the product title and
// the base USD price for the admin manual-order picker. A blank query returns
// the first `limit` variants ordered by product title.
func (s *CatalogStore) SearchVariants(ctx context.Context, tx pgx.Tx, query string, limit int) ([]domain.VariantSearchResult, error) {
	if limit <= 0 {
		limit = 20
	}
	const q = `
		SELECT v.id, p.title, v.sku,
		       (SELECT pr.amount
		          FROM price_sets ps
		          JOIN prices pr ON pr.price_set_id = ps.id
		         WHERE ps.variant_id = v.id
		           AND pr.currency_code = 'USD'
		           AND pr.price_list_id IS NULL
		           AND pr.min_quantity IS NULL
		         LIMIT 1) AS base_amount
		  FROM variants v
		  JOIN products p ON p.id = v.product_id
		 WHERE v.archived_at IS NULL
		   AND ($1 = '' OR v.sku ILIKE '%' || $1 || '%' OR p.title ILIKE '%' || $1 || '%')
		 ORDER BY p.title, v.position
		 LIMIT $2`

	rows, err := tx.Query(ctx, q, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search variants: %w", err)
	}
	defer rows.Close()

	var results []domain.VariantSearchResult
	for rows.Next() {
		var r domain.VariantSearchResult
		var amount *int32
		if err := rows.Scan(&r.VariantID, &r.ProductTitle, &r.SKU, &amount); err != nil {
			return nil, fmt.Errorf("scan variant search result: %w", err)
		}
		if amount != nil {
			cents := int(*amount)
			r.BasePriceCents = &cents
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// ListVariantsByProduct returns all variants for a product, including archived ones.
// Use ListActiveVariantsByProduct for storefront/customer-facing surfaces.
func (s *CatalogStore) ListVariantsByProduct(ctx context.Context, tx pgx.Tx, productID uuid.UUID) ([]domain.Variant, error) {
	rows, err := sqlcgen.New(tx).ListVariantsByProduct(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("list variants: %w", err)
	}
	variants := make([]domain.Variant, len(rows))
	for i, r := range rows {
		variants[i] = *variantFromRow(r)
	}
	return variants, nil
}

// ListActiveVariantsByProduct returns only non-archived variants for a product.
func (s *CatalogStore) ListActiveVariantsByProduct(ctx context.Context, tx pgx.Tx, productID uuid.UUID) ([]domain.Variant, error) {
	rows, err := sqlcgen.New(tx).ListActiveVariantsByProduct(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("list active variants: %w", err)
	}
	variants := make([]domain.Variant, len(rows))
	for i, r := range rows {
		variants[i] = *variantFromRow(r)
	}
	return variants, nil
}

// UpdateVariantParams holds the fields to update a variant.
type UpdateVariantParams struct {
	ID                 uuid.UUID
	SKU                string
	Barcode            *string
	Position           int
	IsDefault          bool
	WeightGrams        *int
	RetailAvailable    bool
	WholesaleAvailable bool
	Metadata           map[string]any
}

// ClearDefaultVariants clears the is_default flag on all variants for a product.
func (s *CatalogStore) ClearDefaultVariants(ctx context.Context, tx pgx.Tx, productID uuid.UUID) error {
	if err := sqlcgen.New(tx).ClearDefaultVariants(ctx, productID); err != nil {
		return fmt.Errorf("clear default variants: %w", err)
	}
	return nil
}

// UpdateVariant updates a variant and returns it.
func (s *CatalogStore) UpdateVariant(ctx context.Context, tx pgx.Tx, p UpdateVariantParams) (*domain.Variant, error) {
	row, err := sqlcgen.New(tx).UpdateVariant(ctx, sqlcgen.UpdateVariantParams{
		ID:                 p.ID,
		Sku:                p.SKU,
		Barcode:            p.Barcode,
		Position:           int32(p.Position),
		IsDefault:          p.IsDefault,
		WeightGrams:        intPtrToInt32Ptr(p.WeightGrams),
		RetailAvailable:    p.RetailAvailable,
		WholesaleAvailable: p.WholesaleAvailable,
		Metadata:           metadataToJSON(p.Metadata),
	})
	if err != nil {
		return nil, fmt.Errorf("update variant: %w", err)
	}
	return variantFromRow(row), nil
}

// UpdateVariantChannels updates a variant's per-channel availability flags.
func (s *CatalogStore) UpdateVariantChannels(ctx context.Context, tx pgx.Tx, id uuid.UUID, retail, wholesale bool) (*domain.Variant, error) {
	row, err := sqlcgen.New(tx).UpdateVariantChannels(ctx, sqlcgen.UpdateVariantChannelsParams{
		ID:                 id,
		RetailAvailable:    retail,
		WholesaleAvailable: wholesale,
	})
	if err != nil {
		return nil, fmt.Errorf("update variant channels: %w", err)
	}
	return variantFromRow(row), nil
}

// UpdateVariantWholesale updates a variant's wholesale MOQ settings.
func (s *CatalogStore) UpdateVariantWholesale(ctx context.Context, tx pgx.Tx, id uuid.UUID, minQty, multiple *int) (*domain.Variant, error) {
	row, err := sqlcgen.New(tx).UpdateVariantWholesale(ctx, sqlcgen.UpdateVariantWholesaleParams{
		ID:                id,
		WholesaleMinQty:   intPtrToInt32Ptr(minQty),
		WholesaleMultiple: intPtrToInt32Ptr(multiple),
	})
	if err != nil {
		return nil, fmt.Errorf("update variant wholesale: %w", err)
	}
	return variantFromRow(row), nil
}

// ArchiveVariant sets archived_at = now() on the variant.
func (s *CatalogStore) ArchiveVariant(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Variant, error) {
	row, err := sqlcgen.New(tx).ArchiveVariant(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("archive variant: %w", err)
	}
	return variantFromRow(row), nil
}

// UnarchiveVariant clears archived_at on the variant.
func (s *CatalogStore) UnarchiveVariant(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Variant, error) {
	row, err := sqlcgen.New(tx).UnarchiveVariant(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("unarchive variant: %w", err)
	}
	return variantFromRow(row), nil
}

// DeleteVariant removes a variant by ID.
func (s *CatalogStore) DeleteVariant(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteVariant(ctx, id); err != nil {
		return fmt.Errorf("delete variant: %w", err)
	}
	return nil
}

// --- Product Options ---

// CreateProductOption inserts a new product option and returns it.
func (s *CatalogStore) CreateProductOption(ctx context.Context, tx pgx.Tx, productID uuid.UUID, name string, position int) (*domain.ProductOption, error) {
	row, err := sqlcgen.New(tx).CreateProductOption(ctx, sqlcgen.CreateProductOptionParams{
		ID:        uuid.New(),
		ProductID: productID,
		Name:      name,
		Position:  int32(position),
	})
	if err != nil {
		return nil, fmt.Errorf("insert product option: %w", err)
	}
	return productOptionFromRow(row), nil
}

// ListProductOptions returns all options for a product.
func (s *CatalogStore) ListProductOptions(ctx context.Context, tx pgx.Tx, productID uuid.UUID) ([]domain.ProductOption, error) {
	rows, err := sqlcgen.New(tx).ListProductOptionsByProduct(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("list product options: %w", err)
	}
	options := make([]domain.ProductOption, len(rows))
	for i, r := range rows {
		options[i] = *productOptionFromRow(r)
	}
	return options, nil
}

// DeleteProductOption removes a product option by ID.
func (s *CatalogStore) DeleteProductOption(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteProductOption(ctx, id); err != nil {
		return fmt.Errorf("delete product option: %w", err)
	}
	return nil
}

// --- Product Option Values ---

// CreateProductOptionValue inserts a new option value and returns it.
func (s *CatalogStore) CreateProductOptionValue(ctx context.Context, tx pgx.Tx, productOptionID uuid.UUID, value string, position int) (*domain.ProductOptionValue, error) {
	row, err := sqlcgen.New(tx).CreateProductOptionValue(ctx, sqlcgen.CreateProductOptionValueParams{
		ID:              uuid.New(),
		ProductOptionID: productOptionID,
		Value:           value,
		Position:        int32(position),
	})
	if err != nil {
		return nil, fmt.Errorf("insert product option value: %w", err)
	}
	return &domain.ProductOptionValue{
		ID:              row.ID,
		ProductOptionID: row.ProductOptionID,
		Value:           row.Value,
		Position:        int(row.Position),
	}, nil
}

// ListProductOptionValues returns all values for an option.
func (s *CatalogStore) ListProductOptionValues(ctx context.Context, tx pgx.Tx, productOptionID uuid.UUID) ([]domain.ProductOptionValue, error) {
	rows, err := sqlcgen.New(tx).ListProductOptionValuesByOption(ctx, productOptionID)
	if err != nil {
		return nil, fmt.Errorf("list product option values: %w", err)
	}
	values := make([]domain.ProductOptionValue, len(rows))
	for i, r := range rows {
		values[i] = domain.ProductOptionValue{
			ID:              r.ID,
			ProductOptionID: r.ProductOptionID,
			Value:           r.Value,
			Position:        int(r.Position),
		}
	}
	return values, nil
}

// DeleteProductOptionValue removes an option value by ID.
func (s *CatalogStore) DeleteProductOptionValue(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteProductOptionValue(ctx, id); err != nil {
		return fmt.Errorf("delete product option value: %w", err)
	}
	return nil
}

// --- Variant Option Values ---

// CreateVariantOptionValue links a variant to an option value.
func (s *CatalogStore) CreateVariantOptionValue(ctx context.Context, tx pgx.Tx, variantID, productOptionValueID uuid.UUID) error {
	err := sqlcgen.New(tx).CreateVariantOptionValue(ctx, sqlcgen.CreateVariantOptionValueParams{
		VariantID:            variantID,
		ProductOptionValueID: productOptionValueID,
	})
	if err != nil {
		return fmt.Errorf("insert variant option value: %w", err)
	}
	return nil
}

// ListVariantOptionValues returns all option values for a variant.
func (s *CatalogStore) ListVariantOptionValues(ctx context.Context, tx pgx.Tx, variantID uuid.UUID) ([]domain.VariantOptionValue, error) {
	rows, err := sqlcgen.New(tx).ListVariantOptionValuesByVariant(ctx, variantID)
	if err != nil {
		return nil, fmt.Errorf("list variant option values: %w", err)
	}
	values := make([]domain.VariantOptionValue, len(rows))
	for i, r := range rows {
		values[i] = domain.VariantOptionValue{
			VariantID:            r.VariantID,
			ProductOptionValueID: r.ProductOptionValueID,
		}
	}
	return values, nil
}

// ListVariantOptionLabels returns the variant's option value strings in
// display order (option position, then value position) — e.g. ["Whole Bean",
// "12oz"]. Empty for single-variant products with no options.
func (s *CatalogStore) ListVariantOptionLabels(ctx context.Context, tx pgx.Tx, variantID uuid.UUID) ([]string, error) {
	labels, err := sqlcgen.New(tx).ListVariantOptionLabels(ctx, variantID)
	if err != nil {
		return nil, fmt.Errorf("list variant option labels: %w", err)
	}
	return labels, nil
}

// DeleteVariantOptionValues removes all option values for a variant.
func (s *CatalogStore) DeleteVariantOptionValues(ctx context.Context, tx pgx.Tx, variantID uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteVariantOptionValuesByVariant(ctx, variantID); err != nil {
		return fmt.Errorf("delete variant option values: %w", err)
	}
	return nil
}

// --- Product Media ---

// CreateProductMediaParams holds the fields needed to create product media.
type CreateProductMediaParams struct {
	ProductID uuid.UUID
	VariantID *uuid.UUID
	R2Key     string
	AltText   string
	Position  int
	MediaType domain.MediaType
}

// CreateProductMedia inserts a new product media and returns it.
func (s *CatalogStore) CreateProductMedia(ctx context.Context, tx pgx.Tx, p CreateProductMediaParams) (*domain.ProductMedia, error) {
	row, err := sqlcgen.New(tx).CreateProductMedia(ctx, sqlcgen.CreateProductMediaParams{
		ID:        uuid.New(),
		ProductID: p.ProductID,
		VariantID: p.VariantID,
		R2Key:     p.R2Key,
		AltText:   p.AltText,
		Position:  int32(p.Position),
		MediaType: string(p.MediaType),
	})
	if err != nil {
		return nil, fmt.Errorf("insert product media: %w", err)
	}
	return productMediaFromRow(row), nil
}

// ListProductMedia returns all media for a product.
func (s *CatalogStore) ListProductMedia(ctx context.Context, tx pgx.Tx, productID uuid.UUID) ([]domain.ProductMedia, error) {
	rows, err := sqlcgen.New(tx).ListProductMediaByProduct(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("list product media: %w", err)
	}
	media := make([]domain.ProductMedia, len(rows))
	for i, r := range rows {
		media[i] = *productMediaFromRow(r)
	}
	return media, nil
}

// UpdateProductMediaPosition updates the position of a product media item.
func (s *CatalogStore) UpdateProductMediaPosition(ctx context.Context, tx pgx.Tx, id uuid.UUID, position int) error {
	err := sqlcgen.New(tx).UpdateProductMediaPosition(ctx, sqlcgen.UpdateProductMediaPositionParams{
		ID:       id,
		Position: int32(position),
	})
	if err != nil {
		return fmt.Errorf("update product media position: %w", err)
	}
	return nil
}

// GetProductMediaByID returns a single product media item by ID.
func (s *CatalogStore) GetProductMediaByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.ProductMedia, error) {
	row, err := sqlcgen.New(tx).GetProductMediaByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get product media %s: %w", id, err)
	}
	return productMediaFromRow(row), nil
}

// DeleteProductMedia removes a product media item by ID and returns the
// R2 key so the caller can enqueue a cleanup job.
func (s *CatalogStore) DeleteProductMedia(ctx context.Context, tx pgx.Tx, id uuid.UUID) (string, error) {
	media, err := sqlcgen.New(tx).GetProductMediaByID(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get product media for delete: %w", err)
	}
	if err := sqlcgen.New(tx).DeleteProductMedia(ctx, id); err != nil {
		return "", fmt.Errorf("delete product media: %w", err)
	}
	return media.R2Key, nil
}

// --- Taxons ---

// CreateTaxonParams holds the fields needed to create a taxon.
type CreateTaxonParams struct {
	ParentID *uuid.UUID
	Name     string
	Slug     string
	Position int
	Depth    int
}

// CreateTaxon inserts a new taxon and returns it.
func (s *CatalogStore) CreateTaxon(ctx context.Context, tx pgx.Tx, p CreateTaxonParams) (*domain.Taxon, error) {
	row, err := sqlcgen.New(tx).CreateTaxon(ctx, sqlcgen.CreateTaxonParams{
		ID:       uuid.New(),
		ParentID: p.ParentID,
		Name:     p.Name,
		Slug:     p.Slug,
		Position: int32(p.Position),
		Depth:    int32(p.Depth),
	})
	if err != nil {
		return nil, fmt.Errorf("insert taxon: %w", err)
	}
	return taxonFromRow(row), nil
}

// GetTaxonByID returns a taxon by ID.
func (s *CatalogStore) GetTaxonByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Taxon, error) {
	row, err := sqlcgen.New(tx).GetTaxonByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get taxon %s: %w", id, err)
	}
	return taxonFromRow(row), nil
}

// GetTaxonBySlug returns a taxon by slug.
func (s *CatalogStore) GetTaxonBySlug(ctx context.Context, tx pgx.Tx, slug string) (*domain.Taxon, error) {
	row, err := sqlcgen.New(tx).GetTaxonBySlug(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("get taxon by slug: %w", err)
	}
	return taxonFromRow(row), nil
}

// ListTaxonsByParent returns all child taxons of a parent.
func (s *CatalogStore) ListTaxonsByParent(ctx context.Context, tx pgx.Tx, parentID uuid.UUID) ([]domain.Taxon, error) {
	rows, err := sqlcgen.New(tx).ListTaxonsByParent(ctx, &parentID)
	if err != nil {
		return nil, fmt.Errorf("list taxons by parent: %w", err)
	}
	taxons := make([]domain.Taxon, len(rows))
	for i, r := range rows {
		taxons[i] = *taxonFromRow(r)
	}
	return taxons, nil
}

// ListRootTaxons returns all top-level taxons.
func (s *CatalogStore) ListRootTaxons(ctx context.Context, tx pgx.Tx) ([]domain.Taxon, error) {
	rows, err := sqlcgen.New(tx).ListRootTaxons(ctx)
	if err != nil {
		return nil, fmt.Errorf("list root taxons: %w", err)
	}
	taxons := make([]domain.Taxon, len(rows))
	for i, r := range rows {
		taxons[i] = *taxonFromRow(r)
	}
	return taxons, nil
}

// UpdateTaxonParams holds the fields to update a taxon.
type UpdateTaxonParams struct {
	ID       uuid.UUID
	ParentID *uuid.UUID
	Name     string
	Slug     string
	Position int
	Depth    int
}

// UpdateTaxon updates a taxon and returns it.
func (s *CatalogStore) UpdateTaxon(ctx context.Context, tx pgx.Tx, p UpdateTaxonParams) (*domain.Taxon, error) {
	row, err := sqlcgen.New(tx).UpdateTaxon(ctx, sqlcgen.UpdateTaxonParams{
		ID:       p.ID,
		ParentID: p.ParentID,
		Name:     p.Name,
		Slug:     p.Slug,
		Position: int32(p.Position),
		Depth:    int32(p.Depth),
	})
	if err != nil {
		return nil, fmt.Errorf("update taxon: %w", err)
	}
	return taxonFromRow(row), nil
}

// DeleteTaxon removes a taxon by ID.
func (s *CatalogStore) DeleteTaxon(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteTaxon(ctx, id); err != nil {
		return fmt.Errorf("delete taxon: %w", err)
	}
	return nil
}

// --- Row converters ---

func productFromRow(r sqlcgen.Product) *domain.Product {
	var taxonID uuid.UUID
	if r.TaxonID != nil {
		taxonID = *r.TaxonID
	}
	return &domain.Product{
		ID:            r.ID,
		Slug:          r.Slug,
		Title:         r.Title,
		Description:   r.Description,
		Status:        domain.ProductStatus(r.Status),
		ProductTypeID: r.ProductTypeID,
		TaxonID:       taxonID,
		Subscribable:  r.Subscribable,
		Visibility:    domain.ProductVisibility(r.Visibility),
		TaxExempt:     r.TaxExempt,
		IsFeatured:    r.IsFeatured,
		Metadata:      metadataFromJSON(r.Metadata),
		AvailableOn:   timestampFromPG(r.AvailableOn),
		DiscontinueOn: timestampFromPG(r.DiscontinueOn),
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}

func variantFromRow(r sqlcgen.Variant) *domain.Variant {
	return &domain.Variant{
		ID:                 r.ID,
		ProductID:          r.ProductID,
		SKU:                r.Sku,
		Barcode:            r.Barcode,
		Position:           int(r.Position),
		IsDefault:          r.IsDefault,
		WeightGrams:        int32PtrToIntPtr(r.WeightGrams),
		WholesaleMinQty:    int32PtrToIntPtr(r.WholesaleMinQty),
		WholesaleMultiple:  int32PtrToIntPtr(r.WholesaleMultiple),
		RetailAvailable:    r.RetailAvailable,
		WholesaleAvailable: r.WholesaleAvailable,
		Metadata:           metadataFromJSON(r.Metadata),
		ArchivedAt:         timestampFromPG(r.ArchivedAt),
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
	}
}

func productOptionFromRow(r sqlcgen.ProductOption) *domain.ProductOption {
	return &domain.ProductOption{
		ID:        r.ID,
		ProductID: r.ProductID,
		Name:      r.Name,
		Position:  int(r.Position),
	}
}

func productMediaFromRow(r sqlcgen.ProductMedium) *domain.ProductMedia {
	return &domain.ProductMedia{
		ID:        r.ID,
		ProductID: r.ProductID,
		VariantID: r.VariantID,
		R2Key:     r.R2Key,
		AltText:   r.AltText,
		Position:  int(r.Position),
		MediaType: domain.MediaType(r.MediaType),
	}
}

func taxonFromRow(r sqlcgen.Taxon) *domain.Taxon {
	return &domain.Taxon{
		ID:       r.ID,
		ParentID: r.ParentID,
		Name:     r.Name,
		Slug:     r.Slug,
		Position: int(r.Position),
		Depth:    int(r.Depth),
	}
}
