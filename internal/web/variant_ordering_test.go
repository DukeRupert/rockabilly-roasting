package web

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

// buildOrdering constructs a productOptionOrdering for a two-option product
// (Size: 12oz<3lb, Grind: Drip<French<Espresso<Whole) the same way
// loadProductOptionOrdering would, returning the ordering plus the value IDs so
// tests can assemble variants.
func buildOrdering() (productOptionOrdering, map[string]uuid.UUID) {
	sizeOpt := uuid.New()
	grindOpt := uuid.New()
	ids := map[string]uuid.UUID{}
	o := productOptionOrdering{
		labels:      map[uuid.UUID]string{},
		valuePos:    map[uuid.UUID]int{},
		valueOption: map[uuid.UUID]uuid.UUID{},
		// Size first even though grind would come first in catalog position order.
		optionOrder: []uuid.UUID{sizeOpt, grindOpt},
	}
	add := func(opt uuid.UUID, name string, pos int) {
		id := uuid.New()
		ids[name] = id
		o.labels[id] = name
		o.valuePos[id] = pos
		o.valueOption[id] = opt
	}
	add(sizeOpt, "12oz", 0)
	add(sizeOpt, "3lb", 1)
	add(grindOpt, "Drip", 0)
	add(grindOpt, "French Press", 1)
	add(grindOpt, "Espresso", 2)
	add(grindOpt, "Whole Bean", 3)
	return o, ids
}

func vovs(ids map[string]uuid.UUID, names ...string) []domain.VariantOptionValue {
	out := make([]domain.VariantOptionValue, len(names))
	for i, n := range names {
		out[i] = domain.VariantOptionValue{ProductOptionValueID: ids[n]}
	}
	return out
}

func TestProductOptionOrdering_LabelIsSizeFirst(t *testing.T) {
	o, ids := buildOrdering()
	// Even when the variant's links list grind before size, the label is size-first.
	assert.Equal(t, "12oz / Drip", o.label(vovs(ids, "Drip", "12oz")))
	assert.Equal(t, "3lb / Whole Bean", o.label(vovs(ids, "Whole Bean", "3lb")))
}

func TestProductOptionOrdering_SortKeySizeThenGrind(t *testing.T) {
	o, ids := buildOrdering()
	// 12oz sorts before 3lb regardless of grind.
	assert.True(t, lessVariantKey(
		o.sortKey(vovs(ids, "12oz", "Whole Bean")),
		o.sortKey(vovs(ids, "3lb", "Drip")),
	))
	// Within the same size, grind follows its configured order.
	assert.True(t, lessVariantKey(
		o.sortKey(vovs(ids, "12oz", "Drip")),
		o.sortKey(vovs(ids, "12oz", "Espresso")),
	))
}

func TestSortVariantsByKey_GroupsBySizeThenGrind(t *testing.T) {
	o, ids := buildOrdering()

	type row struct {
		id   uuid.UUID
		sku  string
		size string
		grnd string
	}
	// Deliberately scrambled input order.
	rows := []row{
		{uuid.New(), "C", "3lb", "Drip"},
		{uuid.New(), "A", "12oz", "Whole Bean"},
		{uuid.New(), "B", "12oz", "Drip"},
		{uuid.New(), "D", "3lb", "Whole Bean"},
		{uuid.New(), "E", "12oz", "Espresso"},
	}
	keys := map[uuid.UUID][]int{}
	for _, r := range rows {
		keys[r.id] = o.sortKey(vovs(ids, r.size, r.grnd))
	}

	sortVariantsByKey(rows, keys, func(r row) (uuid.UUID, string) { return r.id, r.sku })

	got := make([]string, len(rows))
	for i, r := range rows {
		got[i] = r.size + " " + r.grnd
	}
	assert.Equal(t, []string{
		"12oz Drip",
		"12oz Espresso",
		"12oz Whole Bean",
		"3lb Drip",
		"3lb Whole Bean",
	}, got)
}

func TestSortVariantsByKey_TiebreaksOnSKU(t *testing.T) {
	// Two variants with identical (empty) keys fall back to SKU order.
	keys := map[uuid.UUID][]int{}
	a := struct {
		id  uuid.UUID
		sku string
	}{uuid.New(), "ZZZ"}
	b := struct {
		id  uuid.UUID
		sku string
	}{uuid.New(), "AAA"}
	keys[a.id] = []int{}
	keys[b.id] = []int{}
	rows := []struct {
		id  uuid.UUID
		sku string
	}{a, b}
	sortVariantsByKey(rows, keys, func(r struct {
		id  uuid.UUID
		sku string
	}) (uuid.UUID, string) { return r.id, r.sku })
	assert.Equal(t, "AAA", rows[0].sku)
}
