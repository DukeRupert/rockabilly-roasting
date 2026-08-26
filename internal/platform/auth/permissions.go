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

	// Equipment service. Gated twice over: these only matter on an instance
	// that has switched the equipment_service module on, and requireModule
	// runs before the permission check, so on a shop that does not service
	// machines these grants never come up at all.
	PermViewService  = "service:view"
	PermWriteService = "service:write"
)

// rolePermissions maps each staff role to its allowed permissions.
var rolePermissions = map[domain.StaffRole][]string{
	domain.StaffRoleAdmin: {
		PermViewOrders, PermUpdateFulfillment, PermIssueRefunds,
		PermViewReports, PermManageProducts, PermManagePricing,
		PermViewCustomers, PermEditCustomers, PermManageStaff,
		PermCreateDraftOrders, PermManageInventory, PermManageSystem,
		PermViewService, PermWriteService,
	},
	// The tech who services the espresso machine is nearly always the person
	// already driving the van, so fulfillment gets service work outright.
	domain.StaffRoleFulfillment: {
		PermViewOrders, PermUpdateFulfillment, PermManageInventory,
		PermViewService, PermWriteService,
	},
	// Finance reads the hours and the part costs; it does not edit repair
	// records. Read-only is the whole grant.
	domain.StaffRoleFinance: {
		PermViewOrders, PermIssueRefunds, PermViewReports, PermManagePricing,
		PermViewService,
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
	// Support takes the 6am "the machine is down" call. Someone who can read a
	// ticket but not log the call they just took is no use, so this is write
	// as well as view.
	domain.StaffRoleSupport: {
		PermViewOrders, PermViewCustomers, PermEditCustomers, PermCreateDraftOrders,
		PermViewService, PermWriteService,
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
