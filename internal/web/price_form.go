package web

import (
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Shared parsing for the bulk price editors: the product pricing grid
// (admin_catalog.go / admin_price_lists.go) and the all-lists matrix both
// submit a cell per price with its previous value, and both diff the same way.

// priceOpKind enumerates the mutations a bulk price save can apply to one cell.
// opGroupSet/opGroupDelete set or clear a price-list price; opBaseSet sets the
// variant's base price — the product pricing grid edits both in one save. The
// "group" names predate customer groups being retired: the field they act on is
// the price list, which is what a price was ever actually keyed to.
type priceOpKind int

const (
	opGroupSet priceOpKind = iota
	opGroupDelete
	opBaseSet
)

type priceOp struct {
	kind      priceOpKind
	variantID uuid.UUID
	groupID   uuid.UUID
	cents     int
}

// splitGroupKey splits a "<priceListID>:<variantID>" form key into its two UUIDs.
func splitGroupKey(s string) (groupID, variantID uuid.UUID, ok bool) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return uuid.Nil, uuid.Nil, false
	}
	gid, err := uuid.Parse(parts[0])
	if err != nil {
		return uuid.Nil, uuid.Nil, false
	}
	vid, err := uuid.Parse(parts[1])
	if err != nil {
		return uuid.Nil, uuid.Nil, false
	}
	return gid, vid, true
}

// parseDollarCents parses a dollar string into cents. An empty string yields a nil
// pointer (meaning "unset"); a malformed or negative value yields an error.
func parseDollarCents(s string) (*int, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	dollars, err := strconv.ParseFloat(s, 64)
	if err != nil || dollars < 0 {
		return nil, errors.New("invalid price")
	}
	cents := int(math.Round(dollars * 100))
	return &cents, nil
}

func centsEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
