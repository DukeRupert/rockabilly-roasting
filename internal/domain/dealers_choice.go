package domain

import (
	"encoding/binary"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PickRule marks a variant as a dealer's choice pack: each unit ordered ships
// PickCount bags the crew chooses. ExcludeDecaf keeps decaf coffees out of the
// suggestions and the staff dropdowns for that variant.
type PickRule struct {
	VariantID    uuid.UUID
	PickCount    int
	ExcludeDecaf bool
}

// MaxPickCount bounds a pick rule. The database enforces the same limit.
const MaxPickCount = 24

// LineItemPick is one confirmed bag in a dealer's choice line.
type LineItemPick struct {
	LineItemID uuid.UUID
	Position   int
	ProductID  uuid.UUID
	PickedAt   time.Time
}

// PreviousPicks is what went out in a customer's last dealer's choice order,
// shown to staff beside the dropdowns and used to steer suggestions away from
// repeats. Titles is in bag order across every pack on that order.
type PreviousPicks struct {
	OrderID     uuid.UUID
	OrderNumber string
	PlacedAt    time.Time
	ProductIDs  []uuid.UUID
	Titles      []string
}

// PickCandidate is a coffee the crew may put in a dealer's choice box.
type PickCandidate struct {
	ProductID uuid.UUID
	Title     string
	IsDecaf   bool
}

// FilterPickCandidates returns the candidates a rule allows, sorted by title so
// that anything built from the result (dropdowns, suggestions) does not depend
// on database row order.
func FilterPickCandidates(pool []PickCandidate, rule PickRule) []PickCandidate {
	out := make([]PickCandidate, 0, len(pool))
	for _, c := range pool {
		if rule.ExcludeDecaf && c.IsDecaf {
			continue
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b PickCandidate) int {
		if c := strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)); c != 0 {
			return c
		}
		return strings.Compare(a.ProductID.String(), b.ProductID.String())
	})
	return out
}

// SuggestPicks proposes n coffees for a dealer's choice line.
//
// It is pseudo-random but deterministic: the same seed (the line item's ID)
// and the same pool always give the same picks, so the preset dropdowns do not
// reshuffle every time someone reloads the order page.
//
// Variety comes first, then freshness. Coffees not in avoid (what the customer
// got last time) are used first, each once, then the avoided ones, and only
// after every coffee has been used once does the list repeat. So a 6-bag box
// from a 4-coffee pool repeats two coffees rather than leaving one out, and a
// customer only gets last month's coffee again when there aren't enough others.
//
// pool must already be filtered (see FilterPickCandidates). An empty pool or
// n < 1 returns nil.
func SuggestPicks(seed uuid.UUID, n int, pool []PickCandidate, avoid map[uuid.UUID]bool) []uuid.UUID {
	if n < 1 || len(pool) == 0 {
		return nil
	}
	rng := rand.New(rand.NewPCG(binary.BigEndian.Uint64(seed[:8]), binary.BigEndian.Uint64(seed[8:])))

	fresh := make([]uuid.UUID, 0, len(pool))
	stale := make([]uuid.UUID, 0)
	for _, c := range pool {
		if avoid[c.ProductID] {
			stale = append(stale, c.ProductID)
		} else {
			fresh = append(fresh, c.ProductID)
		}
	}
	rng.Shuffle(len(fresh), func(i, j int) { fresh[i], fresh[j] = fresh[j], fresh[i] })
	rng.Shuffle(len(stale), func(i, j int) { stale[i], stale[j] = stale[j], stale[i] })
	order := append(fresh, stale...)

	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = order[i%len(order)]
	}
	return out
}
