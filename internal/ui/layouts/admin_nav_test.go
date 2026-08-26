package layouts

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/ui/components/icon"
)

// The sidebar is where "optional module" becomes visible, so the filter gets
// its own test rather than waiting for the first module section to be built.
// A row pointing at a section that does not exist is worse than no row.

// gatedNav is the live sidebar plus one module-gated row, standing in for the
// Service row that arrives with the equipment section.
func gatedNav() []navItem {
	return append(adminNavItems(), navItem{
		Label:  "Service",
		Href:   "/admin/service",
		IconFn: icon.Package,
		Module: domain.ModuleEquipmentService,
	})
}

func TestVisibleNavItemsKeepsUngatedRows(t *testing.T) {
	items := visibleNavItems(nil)
	require.Len(t, items, len(adminNavItems()),
		"an empty module set must still show every unconditional row")
	assert.Equal(t, "Dashboard", items[0].Label)
}

func TestFilterNavItemsHidesDisabledModuleRow(t *testing.T) {
	items := filterNavItems(gatedNav(), nil)

	require.Len(t, items, len(adminNavItems()), "a disabled module contributes no sidebar row")
	for _, item := range items {
		assert.NotEqual(t, "/admin/service", item.Href)
	}
}

func TestFilterNavItemsShowsEnabledModuleRow(t *testing.T) {
	items := filterNavItems(gatedNav(), domain.ModuleSet{domain.ModuleEquipmentService: true})

	require.Len(t, items, len(adminNavItems())+1, "an enabled module adds exactly its own row")
	assert.Equal(t, "/admin/service", items[len(items)-1].Href)
}

func TestFilterNavItemsIgnoresUnrelatedModules(t *testing.T) {
	// A set carrying a module this row does not name must not switch it on.
	items := filterNavItems(gatedNav(), domain.ModuleSet{"something_else": true})

	require.Len(t, items, len(adminNavItems()))
}
