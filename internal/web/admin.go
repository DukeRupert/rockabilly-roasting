package web

import (
	"net/http"

	"github.com/dukerupert/hiri/internal/ui/layouts"
)

func (d *Deps) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	layouts.Admin(layouts.AdminProps{
		Title:      "Dashboard",
		ActivePath: "/admin",
		StaffName:  "Dev User", // TODO: from session
		StaffRole:  "admin",    // TODO: from session
	}).Render(r.Context(), w) //nolint:errcheck
}
