package domain

import (
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
)

// PriceListType represents the type of a price list.
type PriceListType string

const (
	PriceListTypeSale     PriceListType = "sale"
	PriceListTypeOverride PriceListType = "override"
)

// PriceListStatus represents the lifecycle state of a price list.
type PriceListStatus string

const (
	PriceListStatusDraft   PriceListStatus = "draft"
	PriceListStatusActive  PriceListStatus = "active"
	PriceListStatusExpired PriceListStatus = "expired"
)

// PriceSet groups prices for a variant.
type PriceSet struct {
	ID        uuid.UUID
	VariantID uuid.UUID
}

// Price represents a single price entry within a price set.
//
// MinQuantity nil marks the base rung of a ladder — the price that applies
// until a volume tier takes over. MaxQuantity is vestigial: volume tiers are
// open-ended ladders keyed on MinQuantity alone, where each rung runs until the
// next one starts. Storing an upper bound as well would admit gaps and overlaps
// that make some quantity unpriceable, so it is always left NULL. See
// TierLadder.
type Price struct {
	ID           uuid.UUID
	PriceSetID   uuid.UUID
	Amount       int
	CurrencyCode string
	MinQuantity  *int
	MaxQuantity  *int
	PriceListID  *uuid.UUID
	StartsAt     *time.Time
	EndsAt       *time.Time
}

// PriceList groups promotional or override prices.
type PriceList struct {
	ID       uuid.UUID
	Name     string
	Type     PriceListType
	Status   PriceListStatus
	StartsAt *time.Time
	EndsAt   *time.Time
}

// ListLadder is a variant's ladder on one named price list. Used where a
// variant's pricing has to be shown across several lists at once — the admin
// variant typeahead, which prefills a base price but must show staff what volume
// breaks exist, since it cannot know which customer the order is for.
type ListLadder struct {
	PriceListID uuid.UUID
	ListName    string
	Ladder      TierLadder
}

// Nudge proximity thresholds. A volume upgrade is only suggested when the buyer
// is within max(NudgeProximityFloor, ceil(NudgeProximityPct * next rung)) units
// of the next rung.
//
// The two knobs cover opposite ends of the ladder. A flat percentage collapses
// on cheap rungs — 10% of a 12-unit break only nudges at 11 — so the floor takes
// over there and is deliberately generous in relative terms (3 of 12 is 25%). On
// deep rungs the floor would be noise, so the percentage takes over: a 100-unit
// break nudges from 90. Whichever is larger wins, so neither end is neglected.
const (
	NudgeProximityPct   = 0.10
	NudgeProximityFloor = 3
)

// PriceTier is one rung of a volume price ladder: the first quantity at which a
// unit price takes effect. Pricing is all-units — reaching the rung reprices the
// whole line, it does not price only the units above the threshold.
type PriceTier struct {
	MinQuantity int // first quantity at which this rung applies
	Amount      int // unit price in cents
}

// TierLadder answers what a quantity costs, and what changing that quantity
// would cost. It is the single source of truth for volume pricing: the price a
// cart charges and the saving a nudge advertises are read from the same rungs,
// so the two can never disagree.
//
// The zero value is a valid empty ladder. Build populated ladders with
// NewTierLadder.
type TierLadder struct {
	rungs []PriceTier // sorted ascending by MinQuantity, thresholds unique
}

// NewTierLadder normalizes tiers into a ladder: sorts them ascending, collapses
// duplicate thresholds (later entries win), and clamps any threshold at or below
// 1 to exactly 1 so the base rung has a single canonical form. Callers may pass
// unsorted, duplicated input — which is what makes it safe to build a ladder
// straight from query rows without pre-sorting.
//
// Tiers priced at or above the rung below them are kept, not dropped: an
// inverted ladder is a data-entry mistake for admin validation to catch, not
// something this type should silently paper over. Upgrade declines to advertise
// such a rung.
func NewTierLadder(tiers []PriceTier) TierLadder {
	if len(tiers) == 0 {
		return TierLadder{}
	}

	byThreshold := make(map[int]int, len(tiers))
	for _, t := range tiers {
		min := t.MinQuantity
		if min < 1 {
			min = 1
		}
		byThreshold[min] = t.Amount
	}

	rungs := make([]PriceTier, 0, len(byThreshold))
	for min, amount := range byThreshold {
		rungs = append(rungs, PriceTier{MinQuantity: min, Amount: amount})
	}
	sort.Slice(rungs, func(i, j int) bool { return rungs[i].MinQuantity < rungs[j].MinQuantity })

	return TierLadder{rungs: rungs}
}

// IsEmpty reports whether the ladder has no rungs — the variant has no price at
// all on the list it was built from.
func (l TierLadder) IsEmpty() bool { return len(l.rungs) == 0 }

// Rungs returns the ladder's tiers in ascending order. The result is a copy;
// mutating it does not affect the ladder.
func (l TierLadder) Rungs() []PriceTier {
	out := make([]PriceTier, len(l.rungs))
	copy(out, l.rungs)
	return out
}

// IsTiered reports whether the ladder has volume breaks. A single-rung ladder
// behaves exactly like a flat price, which is what lets base prices and untiered
// list prices share this type instead of needing a separate path.
func (l TierLadder) IsTiered() bool { return len(l.rungs) > 1 }

// TierAt returns the rung in force at the given quantity. Quantities below the
// lowest rung resolve to that lowest rung, which keeps the ladder total: every
// quantity has a price even if the base rung is missing. Returns the zero
// PriceTier for an empty ladder.
func (l TierLadder) TierAt(quantity int) PriceTier {
	if l.IsEmpty() {
		return PriceTier{}
	}
	found := l.rungs[0]
	for _, r := range l.rungs {
		if r.MinQuantity > quantity {
			break
		}
		found = r
	}
	return found
}

// UnitPriceAt returns the per-unit price in cents at the given quantity, or 0
// for an empty ladder. Callers holding an empty ladder should treat it as "no
// price on this list" and fall back, rather than charging zero.
func (l TierLadder) UnitPriceAt(quantity int) int {
	return l.TierAt(quantity).Amount
}

// NextTier returns the first rung above the given quantity, and whether one
// exists. The buyer is on the top rung when ok is false.
func (l TierLadder) NextTier(quantity int) (PriceTier, bool) {
	for _, r := range l.rungs {
		if r.MinQuantity > quantity {
			return r, true
		}
	}
	return PriceTier{}, false
}

// Upgrade is a suggestion to raise a line's quantity to reach a cheaper rung.
type Upgrade struct {
	TargetQty        int // quantity to reach; already a valid order multiple
	AddQty           int // units to add to get there
	CurrentUnitPrice int // per-unit price now, in cents
	TargetUnitPrice  int // per-unit price at TargetQty, in cents
	UnitSavingCents  int // per-unit saving; always positive
	TotalSavingCents int // line total now minus line total at TargetQty; may be negative
}

// CostsLess reports whether upgrading lowers the line total outright — the buyer
// pays less overall despite receiving more. All-units repricing makes this common
// just below a rung, and it is the strongest form of the nudge: "add one more and
// pay less" rather than "add one more and save per unit".
func (u Upgrade) CostsLess() bool { return u.TotalSavingCents > 0 }

// Upgrade returns the volume upgrade worth suggesting at the given quantity, and
// whether there is one. multiple is the variant's wholesale order multiple (0 or
// 1 for no constraint); the target is rounded up to satisfy it, so AddQty is
// always a quantity the buyer can actually order.
//
// It declines — returning false — when the buyer is on the top rung, when the
// next rung is further away than the proximity thresholds allow, or when
// reaching it would not lower the unit price.
func (l TierLadder) Upgrade(quantity, multiple int) (Upgrade, bool) {
	if quantity < 1 {
		return Upgrade{}, false
	}
	next, ok := l.NextTier(quantity)
	if !ok {
		return Upgrade{}, false
	}

	if multiple < 1 {
		multiple = 1
	}
	targetQty := ceilToMultiple(next.MinQuantity, multiple)

	// Round-up can overshoot into a rung beyond the next one, so price the
	// target quantity rather than assuming next.Amount applies to it.
	current := l.UnitPriceAt(quantity)
	target := l.UnitPriceAt(targetQty)
	if target >= current {
		return Upgrade{}, false
	}

	// Widen the proximity window to a whole order increment. A buyer of
	// six-packs sitting one pack short of a break is close to it, even when the
	// raw unit distance looks far — six units is the smallest move they can
	// make. Measuring proximity in units they cannot actually order would
	// suppress exactly the nudges worth showing.
	addQty := targetQty - quantity
	if addQty > ceilToMultiple(proximityThreshold(next.MinQuantity), multiple) {
		return Upgrade{}, false
	}

	return Upgrade{
		TargetQty:        targetQty,
		AddQty:           addQty,
		CurrentUnitPrice: current,
		TargetUnitPrice:  target,
		UnitSavingCents:  current - target,
		TotalSavingCents: quantity*current - targetQty*target,
	}, true
}

// proximityThreshold returns how many units short of a rung still earns a nudge.
func proximityThreshold(rungMinQty int) int {
	pct := int(math.Ceil(NudgeProximityPct * float64(rungMinQty)))
	if pct > NudgeProximityFloor {
		return pct
	}
	return NudgeProximityFloor
}

// ceilToMultiple rounds n up to the nearest multiple of m.
func ceilToMultiple(n, m int) int {
	if m < 2 {
		return n
	}
	if rem := n % m; rem != 0 {
		return n + m - rem
	}
	return n
}

// Drop describes a quantity reduction that raised the unit price by falling out
// of a volume rung.
type Drop struct {
	FromQty        int
	ToQty          int
	FromUnitPrice  int // per-unit price before the change, in cents
	ToUnitPrice    int // per-unit price after the change, in cents
	UnitLossCents  int // per-unit increase; always positive
	LostTierMinQty int // threshold of the rung left behind
}

// Drop reports the price increase caused by reducing a line from one quantity to
// another, and whether there was one. It is advisory: the reduction is always
// allowed and always repriced. Callers surface this so a buyer learns their unit
// price moved at the moment they change the quantity, rather than at checkout.
//
// Returns false when the quantity did not fall, or when it fell without crossing
// below a rung.
func (l TierLadder) Drop(fromQty, toQty int) (Drop, bool) {
	if toQty >= fromQty || toQty < 1 {
		return Drop{}, false
	}
	from := l.UnitPriceAt(fromQty)
	to := l.UnitPriceAt(toQty)
	if to <= from {
		return Drop{}, false
	}
	return Drop{
		FromQty:        fromQty,
		ToQty:          toQty,
		FromUnitPrice:  from,
		ToUnitPrice:    to,
		UnitLossCents:  to - from,
		LostTierMinQty: l.TierAt(fromQty).MinQuantity,
	}, true
}
