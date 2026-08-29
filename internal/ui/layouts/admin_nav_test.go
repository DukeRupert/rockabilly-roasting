package layouts

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// The sidebar is where "optional module" becomes visible. A row pointing at a
// section this shop has switched off is worse than no row: it 404s, and there
// is nothing on the page to explain why.

// ungatedCount is how many rows every shop sees, whatever its modules.
func ungatedCount() int {
	n := 0
	for _, item := range adminNavItems() {
		if item.Module == "" {
			n++
		}
	}
	return n
}

func TestVisibleNavItemsKeepsUngatedRows(t *testing.T) {
	items := visibleNavItems(nil)

	require.Len(t, items, ungatedCount(),
		"an empty module set must still show every unconditional row")
	assert.Equal(t, "Dashboard", items[0].Label)
}

func TestFilterNavItemsHidesDisabledModuleRow(t *testing.T) {
	items := filterNavItems(adminNavItems(), nil)

	require.Len(t, items, ungatedCount(), "a disabled module contributes no sidebar row")
	for _, item := range items {
		assert.NotEqual(t, "/admin/service", item.Href,
			"the Service row must not appear on a shop without the module")
	}
}

func TestFilterNavItemsShowsEnabledModuleRow(t *testing.T) {
	items := filterNavItems(adminNavItems(),
		domain.ModuleSet{domain.ModuleEquipmentService: true})

	require.Len(t, items, ungatedCount()+1, "an enabled module adds exactly its own row")
	assert.Equal(t, "/admin/service", items[len(items)-1].Href)
}

func TestFilterNavItemsIgnoresUnrelatedModules(t *testing.T) {
	// A set carrying some other module must not switch this row on.
	items := filterNavItems(adminNavItems(), domain.ModuleSet{"something_else": true})

	require.Len(t, items, ungatedCount())
}

// The sidebar row and the routes have to agree on which path lights up, or
// navigating into the section un-highlights the row that took you there.
func TestResolveActiveNavCoversTheServiceSection(t *testing.T) {
	assert.Equal(t, "/admin/service", resolveActiveNav("/admin/service"))
	assert.Equal(t, "/admin/service", resolveActiveNav("/admin/service/equipment"))
	assert.Equal(t, "/admin/service", resolveActiveNav("/admin/service/equipment/new"))
	assert.Equal(t, "/admin/service",
		resolveActiveNav("/admin/service/equipment/2f1c4f6e-0000-0000-0000-000000000000"))
}

// Every module-gated row must name a module this binary knows about, or it can
// never render at all — a typo here is a feature that silently does not exist.
func TestEveryGatedNavRowNamesAKnownModule(t *testing.T) {
	for _, item := range adminNavItems() {
		if item.Module == "" {
			continue
		}
		_, ok := domain.LookupModule(string(item.Module))
		assert.True(t, ok, "%s is gated on unknown module %q", item.Label, item.Module)
	}
}
