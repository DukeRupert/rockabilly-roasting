package storefront

import (
	"fmt"
	"strings"

	"github.com/dukerupert/hiri/internal/domain"
)

// Volume-pricing display helpers, shared by the wholesale order sheet and the
// cart. Everything here reads a domain.TierLadder — the same value the server
// prices the line with — so a break shown to a buyer is a break they will get.

// orderMultiple normalizes a variant's case multiple for the ladder helpers,
// which treat anything below 1 as unconstrained.
func orderMultiple(multiple *int) int {
	if multiple == nil || *multiple < 1 {
		return 1
	}
	return *multiple
}

// ladderHint renders a ladder's volume breaks as "12+ $10.00 · 24+ $9.50",
// leaving out the base rung — that price is already displayed beside it.
// Returns "" for a flat price, so callers can drop the element entirely.
func ladderHint(l domain.TierLadder) string {
	var parts []string
	for _, t := range l.Rungs() {
		if t.MinQuantity > 1 {
			parts = append(parts, fmt.Sprintf("%d+ %s", t.MinQuantity, formatCents(t.Amount)))
		}
	}
	return strings.Join(parts, " · ")
}

// ladderJSON serializes a ladder as [[qty,cents],…] for the order sheet, whose
// quantities change without a server round trip. Numbers only, so it is safe in
// an attribute without further escaping.
func ladderJSON(l domain.TierLadder) string {
	rungs := l.Rungs()
	parts := make([]string, len(rungs))
	for i, t := range rungs {
		parts[i] = fmt.Sprintf("[%d,%d]", t.MinQuantity, t.Amount)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// upgradeFor returns the volume upgrade worth showing on a cart line, or nil.
//
// The cart is the only surface that nudges. It re-renders on every quantity
// change, so this is computed in Go against the same ladder that priced the
// line — exact, and with nothing mirrored in the browser to drift from it.
func upgradeFor(item WholesaleCheckoutItem) *domain.Upgrade {
	u, ok := item.Ladder.Upgrade(item.Quantity, orderMultiple(item.Multiple))
	if !ok {
		return nil
	}
	return &u
}

// upgradeLine is the sentence a buyer reads on a cart line. When moving up
// lowers the line total outright — common just under a break, because a break
// reprices every unit — that is the version worth saying: more coffee for less
// money is a stronger reason than a better rate.
func upgradeLine(u domain.Upgrade) string {
	if u.CostsLess() {
		return fmt.Sprintf("Add %d more and pay %s less — the %d+ price is %s each.",
			u.AddQty, formatCents(u.TotalSavingCents), u.TargetQty, formatCents(u.TargetUnitPrice))
	}
	return fmt.Sprintf("Add %d more to reach %s each — %s off every unit.",
		u.AddQty, formatCents(u.TargetUnitPrice), formatCents(u.UnitSavingCents))
}

// dropLine tells a buyer their unit price just went up because they ordered
// fewer. Stated plainly and after the fact: the reduction is theirs to make, and
// it has already been applied.
//
// It does not name the coffee. It is rendered on the line it describes, so
// position identifies it — and naming it would be worse than redundant here,
// since a cart routinely holds two lines of the same coffee in different grinds.
func dropLine(d domain.Drop) string {
	return fmt.Sprintf("Now %s each — you were getting %s at %d+.",
		formatCents(d.ToUnitPrice), formatCents(d.FromUnitPrice), d.LostTierMinQty)
}

// PriceNoteKind selects how a line's price note is styled and, more importantly,
// says what kind of thing it is.
type PriceNoteKind string

const (
	PriceNoteNone    PriceNoteKind = ""
	PriceNoteDrop    PriceNoteKind = "drop"
	PriceNoteUpgrade PriceNoteKind = "upgrade"
	PriceNoteLadder  PriceNoteKind = "ladder"
)

// PriceNote is the one line of small type under a line's unit price.
//
// One line, one slot, whatever the state. The three things worth saying about a
// volume price — you just lost a rung, you are near a rung, these rungs exist —
// are the same fact at different distances, so saying two of them at once
// restates rather than informs. They rank: what just happened to you, then what
// is within reach, then what exists.
type PriceNote struct {
	Text string
	Kind PriceNoteKind
}

// dropLineWithReturn states the new price and, when the rung is still within
// reach, what it takes to get back to it.
//
// A drop and the upgrade that undoes it are the same thought, and immediately
// after a reduction they are nearly always both true — the buyer has just
// stepped one unit below a break. Rendered as two sentences they would repeat
// each other; the price the upgrade targets is the price the drop just lost. So
// they merge: what it costs now, and what it takes to undo.
func dropLineWithReturn(d domain.Drop, u *domain.Upgrade) string {
	if u == nil {
		return dropLine(d)
	}
	return fmt.Sprintf("Now %s each — add %d back to get %s again.",
		formatCents(d.ToUnitPrice), u.AddQty, formatCents(u.TargetUnitPrice))
}

// priceNoteFor picks the single note a cart line should carry.
func priceNoteFor(item WholesaleCheckoutItem) PriceNote {
	if item.Drop != nil {
		return PriceNote{Text: dropLineWithReturn(*item.Drop, upgradeFor(item)), Kind: PriceNoteDrop}
	}
	if u := upgradeFor(item); u != nil {
		return PriceNote{Text: upgradeLine(*u), Kind: PriceNoteUpgrade}
	}
	if hint := ladderHint(item.Ladder); hint != "" {
		return PriceNote{Text: hint, Kind: PriceNoteLadder}
	}
	return PriceNote{Kind: PriceNoteNone}
}

// priceNoteClass styles a note by what it is. A drop and an upgrade are both
// ink: they are sentences the buyer has to read, and ink is what is legible at
// this size on paper. A ladder is muted — reference, not news.
//
// The upgrade is distinguished by an amber marker rather than by coloured text.
// Amber is the brand's colour for a highlight, but #F2A03D on bone paper fails
// contrast well before 12px, so it cannot carry words. Rust could, but rust is
// locked to calls to action and links, and a nudge is neither — spending it here
// would blunt it everywhere it does mean "click this".
func priceNoteClass(kind PriceNoteKind) string {
	switch kind {
	case PriceNoteLadder:
		return "text-ink-soft"
	case PriceNoteNone:
		return ""
	default:
		return "text-ink"
	}
}
