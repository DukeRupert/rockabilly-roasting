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
)

// rolePermissions maps each staff role to its allowed permissions.
var rolePermissions = map[domain.StaffRole][]string{
	domain.StaffRoleAdmin: {
		PermViewOrders, PermUpdateFulfillment, PermIssueRefunds,
		PermViewReports, PermManageProducts, PermManagePricing,
		PermViewCustomers, PermEditCustomers, PermManageStaff,
		PermCreateDraftOrders, PermManageInventory,
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
	domain.StaffRoleSupport: {
		PermViewOrders, PermViewCustomers, PermCreateDraftOrders,
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
