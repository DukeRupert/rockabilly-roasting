package domain

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The catalogue is a convenience and must never become a constraint. These
// guard the properties the add-machine form depends on — not the contents,
// which are expected to change as the shop learns what it actually services.

func TestEquipmentMakesAreDistinctAndSorted(t *testing.T) {
	makes := EquipmentMakes()
	require.NotEmpty(t, makes)

	assert.True(t, sort.StringsAreSorted(makes),
		"a datalist renders in the order given, so an unsorted list reads as random")

	seen := map[string]bool{}
	for _, m := range makes {
		assert.NotEmpty(t, m, "a blank option would render as an empty row in the dropdown")
		assert.False(t, seen[m], "duplicate make %q — every La Marzocco entry must collapse to one suggestion", m)
		seen[m] = true
	}
}

func TestEquipmentModelsAreDistinctAndSorted(t *testing.T) {
	models := EquipmentModels()
	require.NotEmpty(t, models)

	assert.True(t, sort.StringsAreSorted(models))

	seen := map[string]bool{}
	for _, m := range models {
		assert.NotEmpty(t, m)
		assert.False(t, seen[m], "duplicate model %q", m)
		seen[m] = true
	}
}

// Every entry must be complete and carry a category that actually exists —
// a typo'd category would silently break a future picker that sets the field
// from this list.
func TestEquipmentCatalogEntriesAreWellFormed(t *testing.T) {
	entries := EquipmentCatalog()
	require.NotEmpty(t, entries)

	for _, e := range entries {
		assert.NotEmpty(t, e.Make, "entry with model %q has no make", e.Model)
		assert.NotEmpty(t, e.Model, "entry with make %q has no model", e.Make)
		assert.True(t, e.Category.Valid(), "entry %q %q has invalid category %q", e.Make, e.Model, e.Category)
	}
}

// The accessors hand out copies. A caller sorting or truncating the result must
// not reorder the catalogue for everyone else.
func TestEquipmentCatalogIsNotMutableByCallers(t *testing.T) {
	first := EquipmentCatalog()
	require.NotEmpty(t, first)

	original := first[0]
	first[0] = EquipmentCatalogEntry{Make: "Scribbled", Model: "Over", Category: EquipmentCategoryOther}

	assert.Equal(t, original, EquipmentCatalog()[0], "EquipmentCatalog must return a copy")
}

// The list is a hint, never a whitelist: nothing here validates a machine, so a
// make that is not in the catalogue has to remain perfectly legal. This pins the
// absence of a validator, which is easy to add later by accident.
func TestCatalogDoesNotConstrainWhatCanBeRegistered(t *testing.T) {
	makes := EquipmentMakes()
	assert.NotContains(t, makes, "Some Machine Nobody Listed")

	// validateEquipment is what actually guards a registration, and it cares
	// only that a make was given at all.
	assert.True(t, EquipmentCategoryOther.Valid())
}
