package storefront

import (
	"fmt"
	"strconv"
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

// nudgePctAttr and nudgeFloorAttr publish the Go proximity thresholds to the
// order sheet's script, so the client and server agree on when a break is close
// enough to mention rather than each carrying its own copy of the numbers.
func nudgePctAttr() string {
	return strconv.FormatFloat(domain.NudgeProximityPct, 'f', -1, 64)
}

func nudgeFloorAttr() string {
	return strconv.Itoa(domain.NudgeProximityFloor)
}

// upgradeFor returns the volume upgrade worth showing on a cart line, or nil.
// The cart re-renders on every quantity change, so its nudge is computed here in
// Go rather than in the browser — exact, and with no second implementation to
// drift.
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
func dropLine(d domain.Drop) string {
	return fmt.Sprintf("Now %s each — you were getting %s at %d+.",
		formatCents(d.ToUnitPrice), formatCents(d.FromUnitPrice), d.LostTierMinQty)
}
