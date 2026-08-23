package auth

import "github.com/dukerupert/hiri/internal/domain"

// Permission constants.
const (
	PermViewOrders        = "orders:view"
	PermUpdateFulfillment = "orders:fulfill"
	PermIssueRefunds      = "orders:refund"
	PermViewReports       = "reports:view"
	PermManageProducts    = "products:write"
	PermManagePricing     = "pricing:write"
	PermViewCustomers     = "customers:view"
	PermEditCustomers     = "customers:write"
	PermManageStaff       = "staff:write"
	PermCreateDraftOrders = "orders:draft"
	PermManageInventory   = "inventory:write"
	// PermManageSystem covers operator-level plumbing that is not any one
	// department's job — background job health and retries, and the whole
	// Settings section. Admin only: a retried job can send customer mail or
	// move money, and a settings change moves the rules under every order at
	// once (the flat rate, the delivery schedule, the QuickBooks connection
	// wholesale invoicing runs through).
	PermManageSystem = "system:write"
)

// rolePermissions maps each staff role to its allowed permissions.
var rolePermissions = map[domain.StaffRole][]string{
	domain.StaffRoleAdmin: {
		PermViewOrders, PermUpdateFulfillment, PermIssueRefunds,
		PermViewReports, PermManageProducts, PermManagePricing,
		PermViewCustomers, PermEditCustomers, PermManageStaff,
		PermCreateDraftOrders, PermManageInventory, PermManageSystem,
	},
	domain.StaffRoleFulfillment: {
		PermViewOrders, PermUpdateFulfillment, PermManageInventory,
	},
	domain.StaffRoleFinance: {
		PermViewOrders, PermIssueRefunds, PermViewReports, PermManagePricing,
	},
	domain.StaffRoleCatalog: {
		PermManageProducts, PermManagePricing, PermManageInventory,
	},
	// Support fields the "we lost access to our shop inbox" calls, so they hold
	// customers:write. Today that gates exactly one route — changing a
	// customer's sign-in address — and every use of it lands in the audit log
	// with the staff member's name against customer.email_updated. Before
	// hanging a second route off customers:write, re-check that support should
	// have it too; this grant was made for the email case specifically.
	domain.StaffRoleSupport: {
		PermViewOrders, PermViewCustomers, PermEditCustomers, PermCreateDraftOrders,
	},
}

// HasPermission checks whether a staff role has a given permission.
func HasPermission(role domain.StaffRole, perm string) bool {
	perms, ok := rolePermissions[role]
	if !ok {
		return false
	}
	for _, p := range perms {
		if p == perm {
			return true
		}
	}
	return false
}
