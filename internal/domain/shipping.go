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

// shipmentStatusRank orders shipment statuses along the normal delivery
// progression so tracking updates can be applied forward-only. Carrier
// tracking webhooks (Shippo's especially) arrive duplicated and out of order;
// ranking lets a stale "in transit" event be ignored once a shipment is
// delivered. "exception" (returned/failed) ranks just below delivered: a
// shipment can move from in-transit into exception, and an exception can still
// resolve to delivered, but a delivered shipment is treated as terminal — a
// post-delivery return is rare and handled manually rather than auto-regressed.
func shipmentStatusRank(s ShipmentStatus) int {
	switch s {
	case ShipmentStatusPending:
		return 0
	case ShipmentStatusLabelCreated:
		return 1
	case ShipmentStatusInTransit:
		return 2
	case ShipmentStatusException:
		return 3
	case ShipmentStatusDelivered:
		return 4
	default:
		return 0
	}
}

// CanAdvanceTo reports whether moving from the receiver to next is a forward
// transition (a strictly higher rank). Equal or lower targets return false,
// which makes applying a tracking update idempotent: replaying the same event,
// or a late one, is a no-op.
func (s ShipmentStatus) CanAdvanceTo(next ShipmentStatus) bool {
	return shipmentStatusRank(next) > shipmentStatusRank(s)
}

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

// WholesaleFulfillmentMethods returns the fulfillment options offered to a
// wholesale account shipping to shipToZip, in display order. Unlike retail,
// wholesale always offers free pickup at the shop and free carrier shipping;
// local delivery is added only when the zip is inside the local zone and local
// delivery is enabled. Wholesale shipping is always free regardless of method —
// the choice records how the order reaches the buyer, not what it costs.
func (c ShippingConfig) WholesaleFulfillmentMethods(shipToZip string) []ShippingMethod {
	out := make([]ShippingMethod, 0, 3)
	if c.LocalDeliveryEnabled && c.IsLocal(shipToZip) {
		out = append(out, ShippingMethodLocalDelivery)
	}
	out = append(out, ShippingMethodPickup, ShippingMethodShipped)
	return out
}

// WholesaleMethodAllowed reports whether method is a valid wholesale fulfillment
// choice for the given ship-to zip. Callers validate the buyer's submitted
// selection with this rather than trusting the form value.
func (c ShippingConfig) WholesaleMethodAllowed(shipToZip string, method ShippingMethod) bool {
	for _, m := range c.WholesaleFulfillmentMethods(shipToZip) {
		if m == method {
			return true
		}
	}
	return false
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

// LabelAttemptStatus summarizes the most recent BuyLabel job for an order
// for staff-facing UI. The underlying job state lives in River; this type
// collapses River's seven states into the two that matter to operators.
type LabelAttemptStatus string

const (
	// LabelAttemptStatusQueued means a buy is in flight: pending, scheduled,
	// available, running, or about to be retried. Staff should wait.
	LabelAttemptStatusQueued LabelAttemptStatus = "queued"

	// LabelAttemptStatusFailed means a buy was given up on: cancelled (by
	// the worker on deterministic failure) or discarded (retries exhausted).
	// Staff need to act — fix the underlying data and retry, or skip the order.
	LabelAttemptStatusFailed LabelAttemptStatus = "failed"
)

// LabelAttempt is the latest non-successful BuyLabel job for an order,
// surfaced to staff so they can see why a label hasn't appeared yet.
// Successful attempts aren't returned — those show up as Shipment rows.
type LabelAttempt struct {
	JobID       int64
	Status      LabelAttemptStatus
	Attempt     int
	MaxAttempts int
	LastError   string // empty for queued attempts; populated for failed
}

// Shipment represents a physical shipment with tracking. Several fields are
// pointer-typed because not every shipment source carries a label artifact
// or box dimensions — only carrier label purchases populate them.
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
