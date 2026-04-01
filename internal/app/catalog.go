package app

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// CatalogService contains business logic for products, variants, and taxonomy.
type CatalogService struct {
	catalog *store.CatalogStore
	audit   *audit.AuditWriter
	metrics *metrics.Registry
}

// NewCatalogService creates a new CatalogService.
func NewCatalogService(catalog *store.CatalogStore, audit *audit.AuditWriter, metrics *metrics.Registry) *CatalogService {
	return &CatalogService{
		catalog: catalog,
		audit:   audit,
		metrics: metrics,
	}
}

// --- Products ---

// CreateProduct creates a new product and records an audit entry.
func (s *CatalogService) CreateProduct(ctx context.Context, tx pgx.Tx, p store.CreateProductParams, actor Actor) (*domain.Product, error) {
	product, err := s.catalog.CreateProduct(ctx, tx, p)
	if err != nil {
		return nil, fmt.Errorf("create product: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditProductCreated,
		ResourceType: "product",
		ResourceID:   product.ID,
		After:        product,
	}); err != nil {
		return nil, fmt.Errorf("audit product created: %w", err)
	}

	return product, nil
}

// GetProduct returns a product by ID.
func (s *CatalogService) GetProduct(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Product, error) {
	p, err := s.catalog.GetProductByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProductNotFound
		}
		return nil, fmt.Errorf("get product: %w", err)
	}
	return p, nil
}

// GetProductBySlug returns a product by slug.
func (s *CatalogService) GetProductBySlug(ctx context.Context, tx pgx.Tx, slug string) (*domain.Product, error) {
	p, err := s.catalog.GetProductBySlug(ctx, tx, slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProductNotFound
		}
		return nil, fmt.Errorf("get product by slug: %w", err)
	}
	return p, nil
}

// UpdateProduct updates a product and records an audit entry.
func (s *CatalogService) UpdateProduct(ctx context.Context, tx pgx.Tx, id uuid.UUID, p store.UpdateProductParams, actor Actor) (*domain.Product, error) {
	p.ID = id
	product, err := s.catalog.UpdateProduct(ctx, tx, p)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProductNotFound
		}
		return nil, fmt.Errorf("update product: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditProductUpdated,
		ResourceType: "product",
		ResourceID:   product.ID,
		After:        product,
	}); err != nil {
		return nil, fmt.Errorf("audit product updated: %w", err)
	}

	return product, nil
}

// UpdateProductStatus updates a product's status and records an audit entry when archiving.
func (s *CatalogService) UpdateProductStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.ProductStatus, actor Actor) (*domain.Product, error) {
	product, err := s.catalog.UpdateProductStatus(ctx, tx, id, status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProductNotFound
		}
		return nil, fmt.Errorf("update product status: %w", err)
	}

	action := audit.AuditProductUpdated
	if status == domain.ProductStatusArchived {
		action = audit.AuditProductArchived
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       action,
		ResourceType: "product",
		ResourceID:   product.ID,
		After:        product,
	}); err != nil {
		return nil, fmt.Errorf("audit product status: %w", err)
	}

	return product, nil
}

// UpdateProductSubscribable sets whether a product is eligible for subscriptions.
func (s *CatalogService) UpdateProductSubscribable(ctx context.Context, tx pgx.Tx, id uuid.UUID, subscribable bool, actor Actor) (*domain.Product, error) {
	product, err := s.catalog.UpdateProductSubscribable(ctx, tx, id, subscribable)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProductNotFound
		}
		return nil, fmt.Errorf("update product subscribable: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditProductUpdated,
		ResourceType: "product",
		ResourceID:   product.ID,
		After:        product,
	}); err != nil {
		return nil, fmt.Errorf("audit product subscribable: %w", err)
	}

	return product, nil
}

// ListProducts returns products matching the given filter.
func (s *CatalogService) ListProducts(ctx context.Context, tx pgx.Tx, f store.ProductFilter) ([]domain.Product, error) {
	products, err := s.catalog.ListProducts(ctx, tx, f)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	return products, nil
}

// CountProducts returns the total number of products matching the filter.
func (s *CatalogService) CountProducts(ctx context.Context, tx pgx.Tx, f store.ProductFilter) (int, error) {
	count, err := s.catalog.CountProducts(ctx, tx, f)
	if err != nil {
		return 0, fmt.Errorf("count products: %w", err)
	}
	return count, nil
}

// CloneProduct deep-copies a product including its options, option values,
// variants, variant-option links, and prices. The clone gets a "Copy of" title,
// a "-copy" slug suffix, draft status, and "-copy" SKU suffixes. Media is not cloned.
func (s *CatalogService) CloneProduct(ctx context.Context, tx pgx.Tx, sourceID uuid.UUID, pricing *PricingService, actor Actor) (*domain.Product, error) {
	// 1. Fetch source product
	source, err := s.catalog.GetProductByID(ctx, tx, sourceID)
	if err != nil {
		return nil, fmt.Errorf("get source product: %w", err)
	}

	// 2. Create new product as draft
	newProduct, err := s.catalog.CreateProduct(ctx, tx, store.CreateProductParams{
		Slug:          source.Slug + "-copy",
		Title:         "Copy of " + source.Title,
		Description:   source.Description,
		Status:        domain.ProductStatusDraft,
		ProductTypeID: source.ProductTypeID,
		TaxonID:       source.TaxonID,
		Metadata:      source.Metadata,
		AvailableOn:   source.AvailableOn,
		DiscontinueOn: source.DiscontinueOn,
	})
	if err != nil {
		return nil, fmt.Errorf("create cloned product: %w", err)
	}

	// 3. Clone options and option values, building old→new ID map
	optionValueMap := make(map[uuid.UUID]uuid.UUID) // old option value ID → new
	sourceOptions, err := s.catalog.ListProductOptions(ctx, tx, sourceID)
	if err != nil {
		return nil, fmt.Errorf("list source options: %w", err)
	}
	for _, opt := range sourceOptions {
		newOpt, err := s.catalog.CreateProductOption(ctx, tx, newProduct.ID, opt.Name, opt.Position)
		if err != nil {
			return nil, fmt.Errorf("clone option %q: %w", opt.Name, err)
		}
		vals, err := s.catalog.ListProductOptionValues(ctx, tx, opt.ID)
		if err != nil {
			return nil, fmt.Errorf("list option values: %w", err)
		}
		for _, v := range vals {
			newVal, err := s.catalog.CreateProductOptionValue(ctx, tx, newOpt.ID, v.Value, v.Position)
			if err != nil {
				return nil, fmt.Errorf("clone option value %q: %w", v.Value, err)
			}
			optionValueMap[v.ID] = newVal.ID
		}
	}

	// 4. Clone variants, their option value links, and prices
	sourceVariants, err := s.catalog.ListVariantsByProduct(ctx, tx, sourceID)
	if err != nil {
		return nil, fmt.Errorf("list source variants: %w", err)
	}

	// Pre-fetch prices for all source variants
	var basePrices map[uuid.UUID]int
	var groupPrices map[uuid.UUID]map[uuid.UUID]int
	if pricing != nil {
		basePrices, err = pricing.ListBasePricesByProduct(ctx, tx, sourceID, "USD")
		if err != nil {
			return nil, fmt.Errorf("list source base prices: %w", err)
		}
		groupPrices, err = pricing.ListGroupPricesByProduct(ctx, tx, sourceID, "USD")
		if err != nil {
			return nil, fmt.Errorf("list source group prices: %w", err)
		}
	}

	for _, v := range sourceVariants {
		newVariant, err := s.catalog.CreateVariant(ctx, tx, store.CreateVariantParams{
			ProductID:   newProduct.ID,
			SKU:         v.SKU + "-copy",
			Barcode:     v.Barcode,
			Position:    v.Position,
			IsDefault:   v.IsDefault,
			WeightGrams: v.WeightGrams,
			Metadata:    v.Metadata,
		})
		if err != nil {
			return nil, fmt.Errorf("clone variant %s: %w", v.SKU, err)
		}

		// Link option values
		vovs, err := s.catalog.ListVariantOptionValues(ctx, tx, v.ID)
		if err != nil {
			return nil, fmt.Errorf("list variant option values: %w", err)
		}
		for _, vov := range vovs {
			newValID, ok := optionValueMap[vov.ProductOptionValueID]
			if !ok {
				continue
			}
			if err := s.catalog.CreateVariantOptionValue(ctx, tx, newVariant.ID, newValID); err != nil {
				return nil, fmt.Errorf("clone variant option value: %w", err)
			}
		}

		// Clone prices
		if pricing != nil {
			if cents, ok := basePrices[v.ID]; ok {
				if _, err := pricing.SetBasePrice(ctx, tx, newVariant.ID, cents, "USD"); err != nil {
					return nil, fmt.Errorf("clone base price: %w", err)
				}
			}
			if gp, ok := groupPrices[v.ID]; ok {
				for groupID, cents := range gp {
					if _, err := pricing.SetGroupPrice(ctx, tx, newVariant.ID, groupID, cents, "USD"); err != nil {
						return nil, fmt.Errorf("clone group price: %w", err)
					}
				}
			}
		}
	}

	// 5. Audit
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditProductCloned,
		ResourceType: "product",
		ResourceID:   newProduct.ID,
		After:        newProduct,
		Metadata:     map[string]any{"source_product_id": sourceID.String()},
	}); err != nil {
		return nil, fmt.Errorf("audit product cloned: %w", err)
	}

	return newProduct, nil
}

// DeleteProduct removes a product by ID and records an audit entry.
func (s *CatalogService) DeleteProduct(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) error {
	if err := s.catalog.DeleteProduct(ctx, tx, id); err != nil {
		return fmt.Errorf("delete product: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditProductDeleted,
		ResourceType: "product",
		ResourceID:   id,
	}); err != nil {
		return fmt.Errorf("audit product deleted: %w", err)
	}

	return nil
}

// --- Variants ---

// CreateVariant creates a new variant after checking SKU uniqueness and records an audit entry.
func (s *CatalogService) CreateVariant(ctx context.Context, tx pgx.Tx, p store.CreateVariantParams, actor Actor) (*domain.Variant, error) {
	_, err := s.catalog.GetVariantBySKU(ctx, tx, p.SKU)
	if err == nil {
		return nil, ErrSKUAlreadyExists
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check sku uniqueness: %w", err)
	}

	variant, err := s.catalog.CreateVariant(ctx, tx, p)
	if err != nil {
		return nil, fmt.Errorf("create variant: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditVariantCreated,
		ResourceType: "variant",
		ResourceID:   variant.ID,
		After:        variant,
	}); err != nil {
		return nil, fmt.Errorf("audit variant created: %w", err)
	}

	return variant, nil
}

// GetVariant returns a variant by ID.
func (s *CatalogService) GetVariant(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Variant, error) {
	v, err := s.catalog.GetVariantByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVariantNotFound
		}
		return nil, fmt.Errorf("get variant: %w", err)
	}
	return v, nil
}

// ListVariants returns all variants for a product.
func (s *CatalogService) ListVariants(ctx context.Context, tx pgx.Tx, productID uuid.UUID) ([]domain.Variant, error) {
	variants, err := s.catalog.ListVariantsByProduct(ctx, tx, productID)
	if err != nil {
		return nil, fmt.Errorf("list variants: %w", err)
	}
	return variants, nil
}

// UpdateVariant updates a variant after checking SKU uniqueness if changed and records an audit entry.
func (s *CatalogService) UpdateVariant(ctx context.Context, tx pgx.Tx, id uuid.UUID, p store.UpdateVariantParams, actor Actor) (*domain.Variant, error) {
	existing, err := s.catalog.GetVariantByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVariantNotFound
		}
		return nil, fmt.Errorf("get variant for update: %w", err)
	}

	if p.SKU != existing.SKU {
		_, err := s.catalog.GetVariantBySKU(ctx, tx, p.SKU)
		if err == nil {
			return nil, ErrSKUAlreadyExists
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("check sku uniqueness: %w", err)
		}
	}

	// If setting as default, clear existing defaults first.
	if p.IsDefault {
		if err := s.catalog.ClearDefaultVariants(ctx, tx, existing.ProductID); err != nil {
			return nil, fmt.Errorf("clear default variants: %w", err)
		}
	}

	p.ID = id
	variant, err := s.catalog.UpdateVariant(ctx, tx, p)
	if err != nil {
		return nil, fmt.Errorf("update variant: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditVariantUpdated,
		ResourceType: "variant",
		ResourceID:   id,
		After:        variant,
	}); err != nil {
		return nil, fmt.Errorf("audit variant updated: %w", err)
	}

	return variant, nil
}

// DeleteVariant removes a variant by ID and records an audit entry.
func (s *CatalogService) DeleteVariant(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) error {
	if err := s.catalog.DeleteVariant(ctx, tx, id); err != nil {
		return fmt.Errorf("delete variant: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditVariantDeleted,
		ResourceType: "variant",
		ResourceID:   id,
	}); err != nil {
		return fmt.Errorf("audit variant deleted: %w", err)
	}

	return nil
}

// --- Taxons ---

// CreateTaxon creates a new taxon.
func (s *CatalogService) CreateTaxon(ctx context.Context, tx pgx.Tx, p store.CreateTaxonParams) (*domain.Taxon, error) {
	t, err := s.catalog.CreateTaxon(ctx, tx, p)
	if err != nil {
		return nil, fmt.Errorf("create taxon: %w", err)
	}
	return t, nil
}

// GetTaxon returns a taxon by ID.
func (s *CatalogService) GetTaxon(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Taxon, error) {
	t, err := s.catalog.GetTaxonByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTaxonNotFound
		}
		return nil, fmt.Errorf("get taxon: %w", err)
	}
	return t, nil
}

// GetTaxonBySlug returns a taxon by slug.
func (s *CatalogService) GetTaxonBySlug(ctx context.Context, tx pgx.Tx, slug string) (*domain.Taxon, error) {
	t, err := s.catalog.GetTaxonBySlug(ctx, tx, slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTaxonNotFound
		}
		return nil, fmt.Errorf("get taxon by slug: %w", err)
	}
	return t, nil
}

// ListTaxonsByParent returns all child taxons of a parent.
func (s *CatalogService) ListTaxonsByParent(ctx context.Context, tx pgx.Tx, parentID uuid.UUID) ([]domain.Taxon, error) {
	taxons, err := s.catalog.ListTaxonsByParent(ctx, tx, parentID)
	if err != nil {
		return nil, fmt.Errorf("list taxons by parent: %w", err)
	}
	return taxons, nil
}

// ListRootTaxons returns all top-level taxons.
func (s *CatalogService) ListRootTaxons(ctx context.Context, tx pgx.Tx) ([]domain.Taxon, error) {
	taxons, err := s.catalog.ListRootTaxons(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("list root taxons: %w", err)
	}
	return taxons, nil
}

// UpdateTaxon updates a taxon.
func (s *CatalogService) UpdateTaxon(ctx context.Context, tx pgx.Tx, p store.UpdateTaxonParams) (*domain.Taxon, error) {
	t, err := s.catalog.UpdateTaxon(ctx, tx, p)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTaxonNotFound
		}
		return nil, fmt.Errorf("update taxon: %w", err)
	}
	return t, nil
}

// DeleteTaxon removes a taxon by ID.
func (s *CatalogService) DeleteTaxon(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := s.catalog.DeleteTaxon(ctx, tx, id); err != nil {
		return fmt.Errorf("delete taxon: %w", err)
	}
	return nil
}

// --- Product Options ---

// CreateProductOption creates a new product option.
func (s *CatalogService) CreateProductOption(ctx context.Context, tx pgx.Tx, productID uuid.UUID, name string, position int) (*domain.ProductOption, error) {
	opt, err := s.catalog.CreateProductOption(ctx, tx, productID, name, position)
	if err != nil {
		return nil, fmt.Errorf("create product option: %w", err)
	}
	return opt, nil
}

// ListProductOptions returns all options for a product.
func (s *CatalogService) ListProductOptions(ctx context.Context, tx pgx.Tx, productID uuid.UUID) ([]domain.ProductOption, error) {
	opts, err := s.catalog.ListProductOptions(ctx, tx, productID)
	if err != nil {
		return nil, fmt.Errorf("list product options: %w", err)
	}
	return opts, nil
}

// DeleteProductOption removes a product option by ID.
func (s *CatalogService) DeleteProductOption(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := s.catalog.DeleteProductOption(ctx, tx, id); err != nil {
		return fmt.Errorf("delete product option: %w", err)
	}
	return nil
}

// --- Product Option Values ---

// CreateProductOptionValue creates a new option value.
func (s *CatalogService) CreateProductOptionValue(ctx context.Context, tx pgx.Tx, productOptionID uuid.UUID, value string, position int) (*domain.ProductOptionValue, error) {
	v, err := s.catalog.CreateProductOptionValue(ctx, tx, productOptionID, value, position)
	if err != nil {
		return nil, fmt.Errorf("create product option value: %w", err)
	}
	return v, nil
}

// ListProductOptionValues returns all values for an option.
func (s *CatalogService) ListProductOptionValues(ctx context.Context, tx pgx.Tx, productOptionID uuid.UUID) ([]domain.ProductOptionValue, error) {
	vals, err := s.catalog.ListProductOptionValues(ctx, tx, productOptionID)
	if err != nil {
		return nil, fmt.Errorf("list product option values: %w", err)
	}
	return vals, nil
}

// DeleteProductOptionValue removes an option value by ID.
func (s *CatalogService) DeleteProductOptionValue(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := s.catalog.DeleteProductOptionValue(ctx, tx, id); err != nil {
		return fmt.Errorf("delete product option value: %w", err)
	}
	return nil
}

// --- Variant Option Values ---

// CreateVariantOptionValue links a variant to an option value.
func (s *CatalogService) CreateVariantOptionValue(ctx context.Context, tx pgx.Tx, variantID, productOptionValueID uuid.UUID) error {
	if err := s.catalog.CreateVariantOptionValue(ctx, tx, variantID, productOptionValueID); err != nil {
		return fmt.Errorf("create variant option value: %w", err)
	}
	return nil
}

// ListVariantOptionValues returns all option values for a variant.
func (s *CatalogService) ListVariantOptionValues(ctx context.Context, tx pgx.Tx, variantID uuid.UUID) ([]domain.VariantOptionValue, error) {
	vals, err := s.catalog.ListVariantOptionValues(ctx, tx, variantID)
	if err != nil {
		return nil, fmt.Errorf("list variant option values: %w", err)
	}
	return vals, nil
}

// CheckDuplicateVariantOptions returns ErrDuplicateVariantOptions if any existing
// variant for the product already has the exact same set of option values.
func (s *CatalogService) CheckDuplicateVariantOptions(ctx context.Context, tx pgx.Tx, productID uuid.UUID, optionValueIDs []uuid.UUID) error {
	variants, err := s.catalog.ListVariantsByProduct(ctx, tx, productID)
	if err != nil {
		return fmt.Errorf("list variants: %w", err)
	}

	// Build a canonical key for the incoming set
	newKey := canonicalOptionKey(optionValueIDs)

	for _, v := range variants {
		existing, err := s.catalog.ListVariantOptionValues(ctx, tx, v.ID)
		if err != nil {
			return fmt.Errorf("list variant option values: %w", err)
		}
		ids := make([]uuid.UUID, len(existing))
		for i, e := range existing {
			ids[i] = e.ProductOptionValueID
		}
		if canonicalOptionKey(ids) == newKey {
			return ErrDuplicateVariantOptions
		}
	}
	return nil
}

// canonicalOptionKey sorts option value UUIDs and joins them into a comparable string.
func canonicalOptionKey(ids []uuid.UUID) string {
	strs := make([]string, len(ids))
	for i, id := range ids {
		strs[i] = id.String()
	}
	sort.Strings(strs)
	key := ""
	for i, s := range strs {
		if i > 0 {
			key += ","
		}
		key += s
	}
	return key
}

// DeleteVariantOptionValues removes all option values for a variant.
func (s *CatalogService) DeleteVariantOptionValues(ctx context.Context, tx pgx.Tx, variantID uuid.UUID) error {
	if err := s.catalog.DeleteVariantOptionValues(ctx, tx, variantID); err != nil {
		return fmt.Errorf("delete variant option values: %w", err)
	}
	return nil
}

// --- Product Media ---

// CreateProductMedia creates a new product media item and records an audit entry.
func (s *CatalogService) CreateProductMedia(ctx context.Context, tx pgx.Tx, p store.CreateProductMediaParams, actor Actor) (*domain.ProductMedia, error) {
	m, err := s.catalog.CreateProductMedia(ctx, tx, p)
	if err != nil {
		return nil, fmt.Errorf("create product media: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditProductMediaAdded,
		ResourceType: "product_media",
		ResourceID:   m.ID,
		After:        m,
	}); err != nil {
		return nil, fmt.Errorf("audit product media added: %w", err)
	}

	return m, nil
}

// ListProductMedia returns all media for a product.
func (s *CatalogService) ListProductMedia(ctx context.Context, tx pgx.Tx, productID uuid.UUID) ([]domain.ProductMedia, error) {
	media, err := s.catalog.ListProductMedia(ctx, tx, productID)
	if err != nil {
		return nil, fmt.Errorf("list product media: %w", err)
	}
	return media, nil
}

// UpdateProductMediaPosition updates the position of a product media item.
func (s *CatalogService) UpdateProductMediaPosition(ctx context.Context, tx pgx.Tx, id uuid.UUID, position int) error {
	if err := s.catalog.UpdateProductMediaPosition(ctx, tx, id, position); err != nil {
		return fmt.Errorf("update product media position: %w", err)
	}
	return nil
}

// DeleteProductMedia removes a product media item by ID and returns the
// R2 key for cleanup (e.g. enqueue a River job to delete from R2).
func (s *CatalogService) DeleteProductMedia(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (string, error) {
	r2Key, err := s.catalog.DeleteProductMedia(ctx, tx, id)
	if err != nil {
		return "", fmt.Errorf("delete product media: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditProductMediaDeleted,
		ResourceType: "product_media",
		ResourceID:   id,
		Metadata:     map[string]any{"r2_key": r2Key},
	}); err != nil {
		return "", fmt.Errorf("audit product media deleted: %w", err)
	}

	return r2Key, nil
}
