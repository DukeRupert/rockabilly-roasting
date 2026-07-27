package auth_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
)

// Support was granted customers:write so they can correct a customer's sign-in
// address when the customer loses access to the one on file — the call support
// actually fields. The grant is deliberately narrow: assert the roles that
// should not have it still don't, so a future reshuffle of the permission map
// has to break this test on its way through.
func TestEditCustomersGrants(t *testing.T) {
	granted := map[domain.StaffRole]bool{
		domain.StaffRoleAdmin:       true,
		domain.StaffRoleSupport:     true,
		domain.StaffRoleFulfillment: false,
		domain.StaffRoleFinance:     false,
		domain.StaffRoleCatalog:     false,
	}

	for role, want := range granted {
		assert.Equal(t, want, auth.HasPermission(role, auth.PermEditCustomers),
			"role %s and customers:write", role)
	}
}

// Widening customers:write must not have widened anything else for support.
func TestSupportRemainsReadOnlyElsewhere(t *testing.T) {
	for _, perm := range []string{
		auth.PermIssueRefunds,
		auth.PermManageStaff,
		auth.PermManageProducts,
		auth.PermManagePricing,
		auth.PermUpdateFulfillment,
		auth.PermManageInventory,
	} {
		assert.False(t, auth.HasPermission(domain.StaffRoleSupport, perm),
			"support should not hold %s", perm)
	}
}
