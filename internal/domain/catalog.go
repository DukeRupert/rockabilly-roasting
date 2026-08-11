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
	// ProductVisibilityPrivate is a white-labelled product visible/orderable only by
	// the specific customers granted access (product_customer_visibility).
	ProductVisibilityPrivate ProductVisibility = "private"
)

// Product metadata keys stamped on products created by the wholesale white-label
// onboarding flow, so staff and the admin UI can recognise a submission and trace
// it back to its base coffee and the client who owns it. They live in domain
// because every layer needs them: app writes the stamp, store filters on it, and
// the admin UI reads it to flag a row.
const (
	ProductMetaSource             = "source"
	ProductSourceWhiteLabel       = "white_label_onboarding"
	ProductMetaWhiteLabelBaseID   = "base_product_id"
	ProductMetaWhiteLabelCustomer = "white_label_customer_id"
)

// IsWhiteLabelSubmission reports whether a product was created through the
// wholesale white-label onboarding flow.
func IsWhiteLabelSubmission(meta map[string]any) bool {
	if meta == nil {
		return false
	}
	src, _ := meta[ProductMetaSource].(string)
	return src == ProductSourceWhiteLabel
}

// SalesChannel identifies a purchasing surface. Variants carry per-channel
// availability so a size can be sold retail-only or wholesale-only.
type SalesChannel string

const (
	ChannelRetail    SalesChannel = "retail"
	ChannelWholesale SalesChannel = "wholesale"
)

// ChannelFor maps a viewer to the channel they purchase through: an approved
// wholesale viewer buys on the wholesale channel, everyone else on retail.
func ChannelFor(v ProductViewer) SalesChannel {
	if v.IsWholesale {
		return ChannelWholesale
	}
	return ChannelRetail
}

// ProductViewer is the resolved access identity of whoever is asking — the wholesale
// flag plus the customer groups they belong to. It carries no pricing or currency.
//
// The zero value (ProductViewer{}) is the retail/anonymous viewer: not wholesale, no
// groups, and therefore restricted to public products by construction. Callers with no
// authenticated customer use the zero value directly; for an authenticated wholesale
// customer, obtain the viewer from CatalogService.ResolveViewer — never hand-assemble
// GroupIDs in a handler.
//
// CustomerID is the authenticated customer's ID (nil for the anonymous/retail-browse
// viewer). It gates 'private' products: only customers explicitly granted a private
// product may see or order it. A nil CustomerID never sees private products.
type ProductViewer struct {
	IsWholesale bool
	GroupIDs    []uuid.UUID
	CustomerID  *uuid.UUID
}

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
	ID                uuid.UUID
	ProductID         uuid.UUID
	SKU               string
	Barcode           *string
	Position          int
	IsDefault         bool
	WeightGrams       *int
	WholesaleMinQty   *int
	WholesaleMultiple *int
	// RetailAvailable / WholesaleAvailable gate which sales channels may order this
	// variant. Both default true. A variant hidden from a channel is dropped from that
	// channel's catalog listings and refused at add-to-cart.
	RetailAvailable    bool
	WholesaleAvailable bool
	Metadata           map[string]any
	ArchivedAt         *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Available reports whether the variant may be ordered on the given sales channel.
func (v Variant) Available(c SalesChannel) bool {
	switch c {
	case ChannelWholesale:
		return v.WholesaleAvailable
	default:
		return v.RetailAvailable
	}
}

// VariantSearchResult is a flattened read model for the admin variant picker:
// a variant joined with its product title and base (USD) price, used by the
// manual-order line-item typeahead. BasePriceCents is nil when the variant has
// no base price set, in which case staff enter the unit price by hand.
type VariantSearchResult struct {
	VariantID      uuid.UUID
	ProductTitle   string
	SKU            string
	BasePriceCents *int
}

// FilterVariantsForChannel returns only the variants orderable on the given channel,
// preserving order. Used by customer-facing storefront/wholesale surfaces so a variant
// hidden from a channel never renders or gets priced there.
func FilterVariantsForChannel(vs []Variant, c SalesChannel) []Variant {
	out := make([]Variant, 0, len(vs))
	for _, v := range vs {
		if v.Available(c) {
			out = append(out, v)
		}
	}
	return out
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
