package web

import (
	"net/http"

	"github.com/dukerupert/hiri/internal/ui/admin"
)

func (d *Deps) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if IsHTMX(r) {
		admin.DashboardContent().Render(ctx, w) //nolint:errcheck
		return
	}
	name, role := staffNameRole(r)
	admin.Dashboard(name, role).Render(ctx, w) //nolint:errcheck
}
