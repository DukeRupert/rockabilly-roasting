package domain

import (
	"time"

	"github.com/google/uuid"
)

// ProductVisibility controls who can see a product.
type ProductVisibility string

const (
	ProductVisibilityPublic     ProductVisibility = "public"
	ProductVisibilityWholesale  ProductVisibility = "wholesale"
	ProductVisibilityRestricted ProductVisibility = "restricted"
)

// ProductStatus represents the lifecycle state of a product.
type ProductStatus string

const (
	ProductStatusDraft    ProductStatus = "draft"
	ProductStatusActive   ProductStatus = "active"
	ProductStatusArchived ProductStatus = "archived"
)

// MediaType represents the type of product media.
type MediaType string

const (
	MediaTypeImage MediaType = "image"
	MediaTypeVideo MediaType = "video"
)

// Product represents a catalog product.
type Product struct {
	ID            uuid.UUID
	Slug          string
	Title         string
	Description   string
	Status        ProductStatus
	ProductTypeID *uuid.UUID
	TaxonID       uuid.UUID
	Subscribable  bool
	Visibility    ProductVisibility
	TaxExempt     bool
	IsFeatured    bool
	Metadata      map[string]any
	AvailableOn   *time.Time
	DiscontinueOn *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Variant represents a purchasable variant of a product.
//
// ArchivedAt soft-deletes the variant: when set, the variant is hidden from
// storefront/wholesale listings and refused at add-to-cart, but order history,
// invoices, and existing subscriptions on the variant remain functional.
type Variant struct {
	ID          uuid.UUID
	ProductID   uuid.UUID
	SKU         string
	Barcode     *string
	Position    int
	IsDefault   bool
	WeightGrams        *int
	WholesaleMinQty    *int
	WholesaleMultiple  *int
	Metadata           map[string]any
	ArchivedAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ProductOption represents a configurable option for a product (e.g., "Roast Level").
type ProductOption struct {
	ID        uuid.UUID
	ProductID uuid.UUID
	Name      string
	Position  int
}

// ProductOptionValue represents a value for a product option (e.g., "Light").
type ProductOptionValue struct {
	ID              uuid.UUID
	ProductOptionID uuid.UUID
	Value           string
	Position        int
}

// VariantOptionValue is a join table linking variants to option values.
type VariantOptionValue struct {
	VariantID            uuid.UUID
	ProductOptionValueID uuid.UUID
}

// ProductMedia represents an image or video associated with a product.
// R2Key is the object key in Cloudflare R2 — URLs are constructed at
// render time using Cloudflare Image Transformations.
type ProductMedia struct {
	ID        uuid.UUID
	ProductID uuid.UUID
	VariantID *uuid.UUID
	R2Key     string
	AltText   string
	Position  int
	MediaType MediaType
}

// Taxon represents a taxonomy node for product categorization.
type Taxon struct {
	ID       uuid.UUID
	ParentID *uuid.UUID
	Name     string
	Slug     string
	Position int
	Depth    int
}
