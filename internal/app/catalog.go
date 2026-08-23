package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// pgFKViolation is the SQLSTATE code Postgres returns when a DELETE or UPDATE
// violates a foreign key constraint with ON DELETE RESTRICT/NO ACTION.
const pgFKViolation = "23503"

// catalogCustomerReader reads a customer for access-identity resolution.
// *store.CustomerStore satisfies it.
type catalogCustomerReader interface {
	GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Customer, error)
}

// CatalogService contains business logic for products, variants, and taxonomy.
type CatalogService struct {
	catalog   *store.CatalogStore
	customers catalogCustomerReader
	audit     *audit.AuditWriter
	metrics   *metrics.Registry
}

// NewCatalogService creates a new CatalogService.
func NewCatalogService(catalog *store.CatalogStore, customers catalogCustomerReader, audit *audit.AuditWriter, metrics *metrics.Registry) *CatalogService {
	return &CatalogService{
		catalog:   catalog,
		customers: customers,
		audit:     audit,
		metrics:   metrics,
	}
}

// --- Product access (visibility + per-customer grants) ---

// ResolveViewer builds the access identity for a customer: whether they are an
// approved wholesale account, and who they are (which gates private products).
func (s *CatalogService) ResolveViewer(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) (domain.ProductViewer, error) {
	customer, err := s.customers.GetByID(ctx, tx, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ProductViewer{}, ErrCustomerNotFound
		}
		return domain.ProductViewer{}, fmt.Errorf("resolve viewer customer: %w", err)
	}

	return domain.ProductViewer{
		IsWholesale: customer.IsApprovedWholesale(),
		CustomerID:  &customer.ID,
	}, nil
}

// AccessibleFilter maps a viewer to the store visibility filter used for list reads.
// The retail/anonymous zero-value viewer yields a public-only filter.
func (s *CatalogService) AccessibleFilter(v domain.ProductViewer) store.VisibilityContext {
	return store.VisibilityContext{IsWholesale: v.IsWholesale, CustomerID: v.CustomerID}
}

// CanAccessProduct reports whether the viewer may see/purchase the product. It uses the
// same predicate as AccessibleFilter, so list and scalar checks never disagree.
func (s *CatalogService) CanAccessProduct(ctx context.Context, tx pgx.Tx, v domain.ProductViewer, productID uuid.UUID) (bool, error) {
	ok, err := s.catalog.IsProductAccessible(ctx, tx, productID, s.AccessibleFilter(v))
	if err != nil {
		return false, fmt.Errorf("can access product: %w", err)
	}
	return ok, nil
}

// CanAccessVariant reports whether the viewer may purchase the variant, using the same
// access predicate as CanAccessProduct.
func (s *CatalogService) CanAccessVariant(ctx context.Context, tx pgx.Tx, v domain.ProductViewer, variantID uuid.UUID) (bool, error) {
	ok, err := s.catalog.IsVariantAccessible(ctx, tx, variantID, s.AccessibleFilter(v))
	if err != nil {
		return false, fmt.Errorf("can access variant: %w", err)
	}
	return ok, nil
}

// UpdateProductVisibility sets a product's visibility tier.
func (s *CatalogService) UpdateProductVisibility(ctx context.Context, tx pgx.Tx, id uuid.UUID, vis domain.ProductVisibility, actor Actor) (*domain.Product, error) {
	product, err := s.catalog.UpdateProductVisibility(ctx, tx, id, vis)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProductNotFound
		}
		return nil, fmt.Errorf("update product visibility: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditProductVisibilityUpdated,
		ResourceType: "product",
		ResourceID:   product.ID,
		After:        product,
	}); err != nil {
		return nil, fmt.Errorf("audit product visibility: %w", err)
	}

	return product, nil
}

// SetProductCustomerAccess replaces the product's entire per-customer access set with
// customerIDs (declarative — desired state, not deltas). This is the grant set for a
// 'private' white-labelled product; passing an empty slice clears all grants. Records
// one audit entry capturing the new set, with the previous set in metadata.
func (s *CatalogService) SetProductCustomerAccess(ctx context.Context, tx pgx.Tx, productID uuid.UUID, customerIDs []uuid.UUID, actor Actor) error {
	current, err := s.catalog.ListProductCustomerVisibility(ctx, tx, productID)
	if err != nil {
		return fmt.Errorf("list current customer access: %w", err)
	}

	desired := make(map[uuid.UUID]bool, len(customerIDs))
	for _, id := range customerIDs {
		desired[id] = true
	}
	existing := make(map[uuid.UUID]bool, len(current))
	for _, id := range current {
		existing[id] = true
	}

	for id := range desired {
		if !existing[id] {
			if err := s.catalog.SetProductCustomerVisibility(ctx, tx, productID, id); err != nil {
				return fmt.Errorf("grant customer access: %w", err)
			}
		}
	}
	for id := range existing {
		if !desired[id] {
			if err := s.catalog.RemoveProductCustomerVisibility(ctx, tx, productID, id); err != nil {
				return fmt.Errorf("revoke customer access: %w", err)
			}
		}
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditProductCustomerAccessUpdated,
		ResourceType: "product",
		ResourceID:   productID,
		After:        customerIDs,
		Metadata:     map[string]any{"previous_customer_ids": current},
	}); err != nil {
		return fmt.Errorf("audit customer access: %w", err)
	}

	return nil
}

// ListProductCustomerAccess returns the customer IDs granted access to a private product.
func (s *CatalogService) ListProductCustomerAccess(ctx context.Context, tx pgx.Tx, productID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := s.catalog.ListProductCustomerVisibility(ctx, tx, productID)
	if err != nil {
		return nil, fmt.Errorf("list product customer access: %w", err)
	}
	return ids, nil
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
//
// Archiving is refused while white-label products are still based on this coffee.
// A white-label product is a clone, not a view — archiving its base leaves it fully
// orderable but with nothing on record saying which coffee actually goes in the bag.
// Staff reassign the children to another coffee first; see
// WhiteLabelService.ReassignBase.
func (s *CatalogService) UpdateProductStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.ProductStatus, actor Actor) (*domain.Product, error) {
	if status == domain.ProductStatusArchived {
		children, err := s.catalog.ListWhiteLabelChildren(ctx, tx, id)
		if err != nil {
			return nil, fmt.Errorf("check white-label children: %w", err)
		}
		if len(children) > 0 {
			return nil, &WhiteLabelChildrenError{Children: children}
		}
	}

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

// UpdateProductFeatured sets whether a product is featured on the storefront home page.
// Only one product can be featured at a time — turning a product on clears the flag
// on every other product in the same transaction.
func (s *CatalogService) UpdateProductFeatured(ctx context.Context, tx pgx.Tx, id uuid.UUID, isFeatured bool, actor Actor) (*domain.Product, error) {
	if isFeatured {
		if err := s.catalog.ClearOtherFeaturedProducts(ctx, tx, id); err != nil {
			return nil, err
		}
	}

	product, err := s.catalog.UpdateProductFeatured(ctx, tx, id, isFeatured)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProductNotFound
		}
		return nil, fmt.Errorf("update product featured: %w", err)
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
		return nil, fmt.Errorf("audit product featured: %w", err)
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
	if pricing != nil {
		basePrices, err = pricing.ListBasePricesByProduct(ctx, tx, sourceID, "USD")
		if err != nil {
			return nil, fmt.Errorf("list source base prices: %w", err)
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

		// Clone base price
		if pricing != nil {
			if cents, ok := basePrices[v.ID]; ok {
				if _, err := pricing.SetBasePrice(ctx, tx, newVariant.ID, cents, "USD"); err != nil {
					return nil, fmt.Errorf("clone base price: %w", err)
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
//
// Blocked, like archiving, while white-label products are still based on it. The
// base link is metadata rather than a foreign key, so nothing in the database
// would stop the delete — it would just leave the children pointing at an ID that
// no longer resolves, which is the same orphaning archiving is guarded against,
// only unrecoverable.
func (s *CatalogService) DeleteProduct(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) error {
	children, err := s.catalog.ListWhiteLabelChildren(ctx, tx, id)
	if err != nil {
		return fmt.Errorf("check white-label children: %w", err)
	}
	if len(children) > 0 {
		return &WhiteLabelChildrenError{Children: children}
	}

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

// GetVariantBySKU returns a variant by its SKU.
func (s *CatalogService) GetVariantBySKU(ctx context.Context, tx pgx.Tx, sku string) (*domain.Variant, error) {
	v, err := s.catalog.GetVariantBySKU(ctx, tx, sku)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVariantNotFound
		}
		return nil, fmt.Errorf("get variant by sku: %w", err)
	}
	return v, nil
}

// SearchVariants returns variants matching the query (SKU or product title) for
// the admin manual-order picker, capped at a sane limit. A blank query returns a
// first page of variants so the dropdown can prime before the staffer types.
func (s *CatalogService) SearchVariants(ctx context.Context, tx pgx.Tx, query string, limit int) ([]domain.VariantSearchResult, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	results, err := s.catalog.SearchVariants(ctx, tx, strings.TrimSpace(query), limit)
	if err != nil {
		return nil, fmt.Errorf("search variants: %w", err)
	}
	return results, nil
}

// ListVariants returns all variants for a product, including archived ones.
// Use ListActiveVariants for storefront/customer-facing surfaces.
func (s *CatalogService) ListVariants(ctx context.Context, tx pgx.Tx, productID uuid.UUID) ([]domain.Variant, error) {
	variants, err := s.catalog.ListVariantsByProduct(ctx, tx, productID)
	if err != nil {
		return nil, fmt.Errorf("list variants: %w", err)
	}
	return variants, nil
}

// ListActiveVariants returns all non-archived variants for a product, regardless of
// channel availability. Admin/internal surfaces want every active variant; for
// customer-facing storefront/wholesale surfaces use ListActiveVariantsForChannel so
// channel-hidden variants are excluded.
func (s *CatalogService) ListActiveVariants(ctx context.Context, tx pgx.Tx, productID uuid.UUID) ([]domain.Variant, error) {
	variants, err := s.catalog.ListActiveVariantsByProduct(ctx, tx, productID)
	if err != nil {
		return nil, fmt.Errorf("list active variants: %w", err)
	}
	return variants, nil
}

// ListActiveVariantsForChannel returns the non-archived variants orderable on the given
// sales channel. This is the right call for customer-facing storefront (retail) and
// wholesale catalog surfaces.
func (s *CatalogService) ListActiveVariantsForChannel(ctx context.Context, tx pgx.Tx, productID uuid.UUID, channel domain.SalesChannel) ([]domain.Variant, error) {
	variants, err := s.ListActiveVariants(ctx, tx, productID)
	if err != nil {
		return nil, err
	}
	return domain.FilterVariantsForChannel(variants, channel), nil
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

// UpdateVariantChannels sets a variant's per-channel availability (retail/wholesale)
// and records an audit entry. This is a focused partial update — it does not touch SKU,
// weight, or default status — so toggling availability never disturbs other fields.
func (s *CatalogService) UpdateVariantChannels(ctx context.Context, tx pgx.Tx, id uuid.UUID, retail, wholesale bool, actor Actor) (*domain.Variant, error) {
	variant, err := s.catalog.UpdateVariantChannels(ctx, tx, id, retail, wholesale)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVariantNotFound
		}
		return nil, fmt.Errorf("update variant channels: %w", err)
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
		return nil, fmt.Errorf("audit variant channels: %w", err)
	}

	return variant, nil
}

// validateWholesaleMOQ rejects the three ways a variant's wholesale minimum and
// order multiple can contradict each other. Pure so it can be tested without a
// database — the rule about divisibility is the non-obvious one.
func validateWholesaleMOQ(minQty, multiple *int) error {
	if minQty != nil && *minQty < 1 {
		return fmt.Errorf("minimum order quantity must be at least 1: %w", ErrInvalidWholesaleMOQ)
	}
	if multiple != nil && *multiple < 1 {
		return fmt.Errorf("order multiple must be at least 1: %w", ErrInvalidWholesaleMOQ)
	}
	if minQty != nil && multiple != nil && *minQty%*multiple != 0 {
		return fmt.Errorf("minimum %d is not a multiple of %d: %w", *minQty, *multiple, ErrInvalidWholesaleMOQ)
	}
	return nil
}

// UpdateVariantWholesaleMOQ sets a variant's wholesale minimum order quantity
// and order multiple. Nil clears a constraint.
//
// These are enforced at wholesale checkout by domain.ValidateWholesaleCart, and
// until now had no admin UI at all — the only writer was the white-label
// variant copier, so the values could only ever have come from the importer or
// hand-written SQL.
//
// A multiple that does not divide the minimum is rejected: the two constraints
// are applied in sequence, so e.g. min 10 with multiple 4 makes 10 itself an
// invalid quantity and the smallest orderable amount is silently 12. Staff
// should say what they mean rather than discover that at a customer's checkout.
func (s *CatalogService) UpdateVariantWholesaleMOQ(ctx context.Context, tx pgx.Tx, id uuid.UUID, minQty, multiple *int, actor Actor) (*domain.Variant, error) {
	if err := validateWholesaleMOQ(minQty, multiple); err != nil {
		return nil, err
	}

	variant, err := s.catalog.UpdateVariantWholesale(ctx, tx, id, minQty, multiple)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVariantNotFound
		}
		return nil, fmt.Errorf("update variant wholesale moq: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditVariantUpdated,
		ResourceType: "variant",
		ResourceID:   id,
		After:        variant,
		Metadata: map[string]any{
			"wholesale_min_qty":  intPtrOrNil(minQty),
			"wholesale_multiple": intPtrOrNil(multiple),
		},
	}); err != nil {
		return nil, fmt.Errorf("audit variant wholesale moq: %w", err)
	}

	return variant, nil
}

// intPtrOrNil unwraps an *int for audit metadata so the JSON records a number
// or null rather than a pointer address.
func intPtrOrNil(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

// DeleteVariant hard-deletes a variant by ID and records an audit entry.
// Returns ErrVariantInUse if a foreign key constraint blocks the delete
// (line_items, subscriptions, etc.) — callers should offer ArchiveVariant
// instead. Note: ErrVariantInUse aborts the transaction, so callers must
// not continue writing in the same tx after this returns.
func (s *CatalogService) DeleteVariant(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) error {
	if err := s.catalog.DeleteVariant(ctx, tx, id); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgFKViolation {
			return ErrVariantInUse
		}
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

// ArchiveVariant soft-deletes a variant: it stays in the database (so order
// history, invoices, and existing subscriptions keep resolving), but is
// hidden from storefront/wholesale listings and rejected at add-to-cart.
// Existing subscriptions on the variant continue to renew — archive does
// not pause or cancel customer subscriptions. Use UpdateSubscriptionVariant
// or cancel-and-recreate for those.
func (s *CatalogService) ArchiveVariant(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*domain.Variant, error) {
	variant, err := s.catalog.ArchiveVariant(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVariantNotFound
		}
		return nil, fmt.Errorf("archive variant: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditVariantArchived,
		ResourceType: "variant",
		ResourceID:   id,
		After:        variant,
	}); err != nil {
		return nil, fmt.Errorf("audit variant archived: %w", err)
	}

	return variant, nil
}

// UnarchiveVariant clears archived_at so the variant is sellable again.
func (s *CatalogService) UnarchiveVariant(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*domain.Variant, error) {
	variant, err := s.catalog.UnarchiveVariant(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVariantNotFound
		}
		return nil, fmt.Errorf("unarchive variant: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditVariantUnarchived,
		ResourceType: "variant",
		ResourceID:   id,
		After:        variant,
	}); err != nil {
		return nil, fmt.Errorf("audit variant unarchived: %w", err)
	}

	return variant, nil
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

// VariantLabel returns the variant's option values joined for customer-facing
// display — "Whole Bean · 12oz". Empty string when the variant has no options
// (single-variant product), so callers can fall back to nothing rather than
// showing an internal SKU.
func (s *CatalogService) VariantLabel(ctx context.Context, tx pgx.Tx, variantID uuid.UUID) (string, error) {
	labels, err := s.catalog.ListVariantOptionLabels(ctx, tx, variantID)
	if err != nil {
		return "", fmt.Errorf("list variant option labels: %w", err)
	}
	return strings.Join(labels, " · "), nil
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
