package store

import (
	"context"
	"fmt"
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

// DeleteProduct removes a product by ID.
func (s *CatalogStore) DeleteProduct(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteProduct(ctx, id); err != nil {
		return fmt.Errorf("delete product: %w", err)
	}
	return nil
}

// ProductFilter holds optional filters for listing products.
type ProductFilter struct {
	Status  *domain.ProductStatus
	TaxonID *uuid.UUID
	Limit   int
	Offset  int
}

// ListProducts returns products matching the given filter (hand-written for dynamic WHERE).
func (s *CatalogStore) ListProducts(ctx context.Context, tx pgx.Tx, f ProductFilter) ([]domain.Product, error) {
	query := `SELECT id, slug, title, description, status, product_type_id, taxon_id,
	                 subscribable, metadata, available_on, discontinue_on, created_at, updated_at
	          FROM products WHERE true`
	args := []any{}
	argN := 1

	if f.Status != nil {
		query += fmt.Sprintf(" AND status = $%d", argN)
		args = append(args, string(*f.Status))
		argN++
	}
	if f.TaxonID != nil {
		query += fmt.Sprintf(" AND taxon_id = $%d", argN)
		args = append(args, *f.TaxonID)
		argN++
	}

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

	var products []domain.Product
	for rows.Next() {
		var p domain.Product
		var status string
		var productTypeID, taxonID *uuid.UUID
		var metadata []byte
		var availableOn, discontinueOn pgtype.Timestamptz
		if err := rows.Scan(
			&p.ID, &p.Slug, &p.Title, &p.Description, &status, &productTypeID, &taxonID,
			&p.Subscribable, &metadata, &availableOn, &discontinueOn, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan product: %w", err)
		}
		p.Status = domain.ProductStatus(status)
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

// --- Variants ---

// CreateVariantParams holds the fields needed to create a variant.
type CreateVariantParams struct {
	ProductID   uuid.UUID
	SKU         string
	Barcode     *string
	Position    int
	IsDefault   bool
	WeightGrams *int
	Metadata    map[string]any
}

// CreateVariant inserts a new variant and returns it.
func (s *CatalogStore) CreateVariant(ctx context.Context, tx pgx.Tx, p CreateVariantParams) (*domain.Variant, error) {
	row, err := sqlcgen.New(tx).CreateVariant(ctx, sqlcgen.CreateVariantParams{
		ID:          uuid.New(),
		ProductID:   p.ProductID,
		Sku:         p.SKU,
		Barcode:     p.Barcode,
		Position:    int32(p.Position),
		IsDefault:   p.IsDefault,
		WeightGrams: intPtrToInt32Ptr(p.WeightGrams),
		Metadata:    metadataToJSON(p.Metadata),
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

// ListVariantsByProduct returns all variants for a product.
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

// UpdateVariantParams holds the fields to update a variant.
type UpdateVariantParams struct {
	ID          uuid.UUID
	SKU         string
	Barcode     *string
	Position    int
	IsDefault   bool
	WeightGrams *int
	Metadata    map[string]any
}

// UpdateVariant updates a variant and returns it.
func (s *CatalogStore) UpdateVariant(ctx context.Context, tx pgx.Tx, p UpdateVariantParams) (*domain.Variant, error) {
	row, err := sqlcgen.New(tx).UpdateVariant(ctx, sqlcgen.UpdateVariantParams{
		ID:          p.ID,
		Sku:         p.SKU,
		Barcode:     p.Barcode,
		Position:    int32(p.Position),
		IsDefault:   p.IsDefault,
		WeightGrams: intPtrToInt32Ptr(p.WeightGrams),
		Metadata:    metadataToJSON(p.Metadata),
	})
	if err != nil {
		return nil, fmt.Errorf("update variant: %w", err)
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
	URL       string
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
		Url:       p.URL,
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

// DeleteProductMedia removes a product media item by ID.
func (s *CatalogStore) DeleteProductMedia(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteProductMedia(ctx, id); err != nil {
		return fmt.Errorf("delete product media: %w", err)
	}
	return nil
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
		Metadata:      metadataFromJSON(r.Metadata),
		AvailableOn:   timestampFromPG(r.AvailableOn),
		DiscontinueOn: timestampFromPG(r.DiscontinueOn),
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}

func variantFromRow(r sqlcgen.Variant) *domain.Variant {
	return &domain.Variant{
		ID:          r.ID,
		ProductID:   r.ProductID,
		SKU:         r.Sku,
		Barcode:     r.Barcode,
		Position:    int(r.Position),
		IsDefault:   r.IsDefault,
		WeightGrams: int32PtrToIntPtr(r.WeightGrams),
		Metadata:    metadataFromJSON(r.Metadata),
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
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
		URL:       r.Url,
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
