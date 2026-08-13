package domain

import (
	"fmt"
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

	// LocalDeliveryWeekdays are the days the van runs. Empty means no schedule
	// is configured, which disables date computation entirely — callers fall
	// back to method-only messaging rather than inventing a date.
	LocalDeliveryWeekdays []time.Weekday

	// LocalDeliveryCutoffMinutes is the order-by time on a delivery day,
	// expressed as minutes past local midnight (540 = 9:00am). An order placed
	// at or after the cutoff on a delivery day rides the *next* run, not that
	// morning's — the van is already loaded.
	LocalDeliveryCutoffMinutes int

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

// CalculateForMethod returns the shipping cost in cents once the fulfillment
// method is known. Local delivery and pickup are always free. A "shipped"
// method (or nil, the standard shipped flow) pays the normal carrier
// calculation — flat rate, waived above the free-shipping threshold — even from
// a local zip: a local customer who opts to have an order mailed is charged like
// any other shipment rather than getting the local-zone freeness. Use this
// instead of Calculate wherever the resolved method is available, so an opt-in
// mail-out from a local address is priced correctly.
func (c ShippingConfig) CalculateForMethod(subtotalCents int, shipToZip string, method *ShippingMethod) int {
	if method != nil && (*method == ShippingMethodLocalDelivery || *method == ShippingMethodPickup) {
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

// --- Local delivery schedule ---

// HasDeliverySchedule reports whether a delivery date can be computed: local
// delivery must be switched on and at least one weekday configured. When this
// is false the storefront and emails describe the method without promising a
// date, rather than guessing one.
func (c ShippingConfig) HasDeliverySchedule() bool {
	return c.LocalDeliveryEnabled && len(c.LocalDeliveryWeekdays) > 0
}

// NextDeliveryDate returns the delivery day an order placed at now would ride,
// as local midnight on that date in loc.
//
// The rule the shop works to: an order placed before the cutoff on a delivery
// day goes out on that day's run; at or after the cutoff it waits for the next
// one. Placement on a non-delivery day always rolls forward to the next
// scheduled weekday, cutoff untouched — there is no run that day to miss.
//
// The boundary is exclusive at the cutoff minute: 08:59 makes today's van,
// 09:00 does not. Staff start loading at nine, so nine itself is already late.
//
// ok is false when no schedule is configured (see HasDeliverySchedule).
func (c ShippingConfig) NextDeliveryDate(now time.Time, loc *time.Location) (time.Time, bool) {
	if !c.HasDeliverySchedule() {
		return time.Time{}, false
	}
	if loc == nil {
		loc = time.UTC
	}

	local := now.In(loc)
	minutesIn := local.Hour()*60 + local.Minute()

	// Midnight on the placement date, in the merchant's zone. Every candidate
	// is derived from this by calendar-day arithmetic rather than by adding
	// 24h durations, so a DST transition inside the search window shifts the
	// wall clock without shifting which date we land on.
	base := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)

	// Search today plus a full week. The eighth offset matters: with a
	// single-weekday schedule, an order placed past that day's cutoff has to
	// roll to the same weekday seven days out, which a 0..6 window would miss.
	// offset 0 is today and is the only candidate the cutoff can disqualify.
	for offset := 0; offset <= 7; offset++ {
		candidate := base.AddDate(0, 0, offset)
		if !c.deliversOn(candidate.Weekday()) {
			continue
		}
		if offset == 0 && minutesIn >= c.LocalDeliveryCutoffMinutes {
			continue
		}
		return candidate, true
	}
	return time.Time{}, false
}

// deliversOn reports whether the van runs on the given weekday.
func (c ShippingConfig) deliversOn(day time.Weekday) bool {
	for _, d := range c.LocalDeliveryWeekdays {
		if d == day {
			return true
		}
	}
	return false
}

// DeliveryDaysLabel renders the schedule as customer-facing prose — "Mondays
// and Thursdays". Derived from LocalDeliveryWeekdays rather than stored
// alongside them, so the sentence a customer reads and the arithmetic that
// picks their date cannot drift apart.
//
// Days are listed in week order starting Sunday regardless of how they were
// entered, so the label reads the same however admin ordered the checkboxes.
func (c ShippingConfig) DeliveryDaysLabel() string {
	names := make([]string, 0, len(c.LocalDeliveryWeekdays))
	for day := time.Sunday; day <= time.Saturday; day++ {
		if c.deliversOn(day) {
			names = append(names, day.String()+"s")
		}
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
	}
}

// CutoffLabel renders the cutoff as a short customer-facing time — "9am",
// "9:30am". Minutes are omitted on the hour because "9am" is how the shop says
// it, and a cutoff is only persuasive if it reads like a deadline.
func (c ShippingConfig) CutoffLabel() string {
	h := c.LocalDeliveryCutoffMinutes / 60
	m := c.LocalDeliveryCutoffMinutes % 60

	suffix := "am"
	if h >= 12 {
		suffix = "pm"
	}
	display := h % 12
	if display == 0 {
		display = 12
	}

	if m == 0 {
		return fmt.Sprintf("%d%s", display, suffix)
	}
	return fmt.Sprintf("%d:%02d%s", display, m, suffix)
}

// DeliveryDateLabel formats a computed delivery date the way it is spoken to a
// customer — "Thursday, August 14". Shared by checkout, the confirmation email,
// and admin so the same promise is worded identically everywhere it appears.
// The year is omitted: a delivery date is always within the week.
func DeliveryDateLabel(t time.Time) string {
	return t.Format("Monday, January 2")
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

// RefundStatus represents the state of a carrier label refund. Refunds are
// asynchronous: Shippo returns QUEUED immediately, then settles the request
// over up to 14 days. A poll job walks a shipment from Requested to the
// terminal Refunded or Failed.
type RefundStatus string

const (
	// RefundStatusNone — no refund has been requested (the default).
	RefundStatusNone RefundStatus = "none"
	// RefundStatusRequested — a refund was submitted to the carrier and is
	// pending resolution.
	RefundStatusRequested RefundStatus = "requested"
	// RefundStatusRefunded — the carrier accepted the refund. Terminal.
	RefundStatusRefunded RefundStatus = "refunded"
	// RefundStatusFailed — the carrier rejected the refund (label was used or
	// scanned) or the request timed out. Terminal. The label is live.
	RefundStatusFailed RefundStatus = "failed"
)

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

	// ProviderTransactionID is the carrier's transaction handle (Shippo's
	// transaction object_id) needed to request a refund. Nil for imported or
	// legacy shipments — those cannot be refunded through the API.
	ProviderTransactionID *string
	RefundStatus          RefundStatus
	RefundID              *string // carrier refund handle, for polling resolution
	RefundRequestedAt     *time.Time
	RefundRequestedBy     *uuid.UUID
	RefundedAt            *time.Time
}

// BlocksRebuy reports whether this shipment should prevent buying a new label
// for its order. A shipment blocks re-buy while its label is live — that is,
// when no refund has been requested (none) or a requested refund was rejected
// (failed, meaning the label was found to be used). A requested or completed
// refund frees the order to have a corrected label purchased.
//
// This is the single source of truth for the "one active label per order"
// rule — the server-side buy guard, the buy-label UI gate, and re-buy all
// read it. Do not re-express the rule inline anywhere else.
func (s Shipment) BlocksRebuy() bool {
	return s.RefundStatus == RefundStatusNone || s.RefundStatus == RefundStatusFailed
}

// CanRequestRefund reports whether a refund may be requested for this shipment:
// it must carry a provider transaction ID (so the carrier can be called), must
// not already have a refund in flight or completed, and must not be delivered
// (an obviously-used label the carrier will reject).
func (s Shipment) CanRequestRefund() bool {
	if s.ProviderTransactionID == nil || *s.ProviderTransactionID == "" {
		return false
	}
	if s.RefundStatus != RefundStatusNone && s.RefundStatus != RefundStatusFailed {
		return false
	}
	return s.Status != ShipmentStatusDelivered
}
