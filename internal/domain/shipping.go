package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// ShipmentStatus represents the lifecycle state of a shipment.
type ShipmentStatus string

const (
	ShipmentStatusPending      ShipmentStatus = "pending"
	ShipmentStatusLabelCreated ShipmentStatus = "label_created"
	ShipmentStatusInTransit    ShipmentStatus = "in_transit"
	ShipmentStatusDelivered    ShipmentStatus = "delivered"
	ShipmentStatusException    ShipmentStatus = "exception"
)

// ShippingConfig holds the merchant's shipping rules.
type ShippingConfig struct {
	FlatRateCents         int
	FreeShippingThreshold *int
	Currency              string
	LocalZipCodes         []string

	// Local fulfillment toggles. A local zip is always free, but the merchant
	// chooses which channels to offer:
	//   - LocalDeliveryEnabled: route delivery on configured days
	//   - LocalPickupEnabled:   customer picks up at the shop when notified
	// At least one should normally be true; if both are false the storefront
	// falls back to the standard "shipped" flow even for local zips.
	LocalDeliveryEnabled    bool
	LocalPickupEnabled      bool
	LocalPickupInstructions string // shown to customer at checkout + in the ready-for-pickup email
	LocalDeliveryDays       string // display string, e.g. "Mondays and Thursdays"

	// Origin address. OriginEmail and OriginPhone are required by USPS via
	// Shippo when buying labels — purchases fail without them. The other
	// origin fields feed Shippo's address_from + show on the storefront.
	OriginName    string
	OriginStreet1 string
	OriginStreet2 string
	OriginCity    string
	OriginState   string
	OriginZip     string
	OriginCountry string
	OriginEmail   string
	OriginPhone   string

	// TareWeightOz is the packaging weight (box + dunnage) added to every
	// computed shipment weight on export.
	TareWeightOz float64
}

// IsLocal reports whether a ship-to zip falls inside the local delivery zone.
// Accepts either 5-digit or ZIP+4 formats and ignores surrounding whitespace.
func (c ShippingConfig) IsLocal(shipToZip string) bool {
	z := normalizeZip(shipToZip)
	if z == "" {
		return false
	}
	for _, local := range c.LocalZipCodes {
		if normalizeZip(local) == z {
			return true
		}
	}
	return false
}

// Calculate returns the shipping cost in cents for a given subtotal and
// destination zip. Local zips always ship free; otherwise the flat rate applies
// unless the subtotal meets the free-shipping threshold.
func (c ShippingConfig) Calculate(subtotalCents int, shipToZip string) int {
	if c.IsLocal(shipToZip) {
		return 0
	}
	if c.FreeShippingThreshold != nil && subtotalCents >= *c.FreeShippingThreshold {
		return 0
	}
	return c.FlatRateCents
}

// EligibleLocalMethods returns the local fulfillment methods available for a
// given ship-to zip, in stable display order (delivery first, then pickup).
// Returns an empty slice when the zip is not local or when no local toggles
// are enabled — callers should fall back to ShippingMethodShipped in that case.
func (c ShippingConfig) EligibleLocalMethods(shipToZip string) []ShippingMethod {
	if !c.IsLocal(shipToZip) {
		return nil
	}
	out := make([]ShippingMethod, 0, 2)
	if c.LocalDeliveryEnabled {
		out = append(out, ShippingMethodLocalDelivery)
	}
	if c.LocalPickupEnabled {
		out = append(out, ShippingMethodPickup)
	}
	return out
}

// normalizeZip trims whitespace and strips a ZIP+4 suffix so "99336-1234 "
// compares equal to "99336".
func normalizeZip(zip string) string {
	z := strings.TrimSpace(zip)
	if i := strings.Index(z, "-"); i >= 0 {
		z = z[:i]
	}
	return z
}

// BoxPreset is a named carton size the merchant ships in. When a label is
// purchased the system picks the smallest preset whose MaxWeightOz covers the
// computed shipment weight; that preset's dimensions are sent to the carrier
// for rating.
type BoxPreset struct {
	ID          uuid.UUID
	Name        string
	LengthIn    float64
	WidthIn     float64
	HeightIn    float64
	MaxWeightOz float64
	SortOrder   int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// SelectBoxForWeight returns the smallest preset (by MaxWeightOz) whose
// MaxWeightOz is at least weightOz. presets must be sorted by MaxWeightOz
// ascending. If no preset fits, the heaviest one is returned with ok=false
// so the caller can choose to proceed (oversized) or surface a warning.
// Returns nil when the preset list is empty.
func SelectBoxForWeight(presets []BoxPreset, weightOz float64) (*BoxPreset, bool) {
	if len(presets) == 0 {
		return nil, false
	}
	for i := range presets {
		if presets[i].MaxWeightOz >= weightOz {
			return &presets[i], true
		}
	}
	return &presets[len(presets)-1], false
}

// Shipment represents a physical shipment with tracking. Several fields are
// pointer-typed because Pirate Ship CSV imports do not carry a label artifact
// or box dimensions — only EasyPost-purchased shipments populate them.
type Shipment struct {
	ID             uuid.UUID
	OrderID        uuid.UUID
	Status         ShipmentStatus
	Provider       string
	TrackingNumber string
	LabelURL       *string
	CarrierName    string
	ServiceName    string
	LabelCostCents int
	LabelCurrency  string
	WeightOz       float64
	LengthIn       *float64
	WidthIn        *float64
	HeightIn       *float64
	CreatedBy      uuid.UUID
	CreatedAt      time.Time
	LabelCreatedAt *time.Time
	ShippedAt      *time.Time
	DeliveredAt    *time.Time
	LabelR2Key     *string
	LabelFormat    *string
}
