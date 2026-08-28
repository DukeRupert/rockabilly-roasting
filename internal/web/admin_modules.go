package web

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// Optional feature modules: the Settings tab that switches whole sections of
// the app on and off, and the two pieces of middleware that make a disabled
// section disappear.
//
// See docs/equipment-service-module.md for the design and
// db/migrations/076_modules.sql for why the registry of keys lives in Go.

// withModules attaches the instance's enabled modules to the request context so
// the admin layout can decide which sidebar rows exist. It reads the app-layer
// cache, not the database, so it costs nothing to run on every admin request.
func (d *Deps) withModules(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(domain.WithModules(r.Context(), d.ModuleService.Set())))
	})
}

// requireModule guards every route in a module's section, rendering the ordinary
// not-found page when the module is off.
//
// 404, not 403: a merchant who has not switched on equipment service does not
// have a service section that they lack permission for — they have no service
// section. The route should look exactly like any other URL that was never
// built, which is also what makes it safe to leave a stale bookmark or an old
// email link pointing at one.
//
// Mount this outside requirePermission, so a disabled module is invisible to
// everyone including admins rather than 403ing for some roles and 404ing for
// others.
func (d *Deps) requireModule(m domain.Module, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !d.ModuleService.Enabled(m) {
			d.handleNotFoundPage(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleAdminSettingsModules renders the Settings → Modules tab.
func (d *Deps) handleAdminSettingsModules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	section, err := d.loadSettingsSection(ctx)
	if err != nil {
		Error(w, r, err)
		return
	}

	var states []domain.ModuleState
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		states, txErr = d.ModuleService.List(ctx, tx)
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	staffName, staffRole := staffNameRole(r)
	props := admin.SettingsModulesProps{
		StaffName: staffName,
		StaffRole: staffRole,
		Nav:       section.nav(staffRole),
		Modules:   admin.ModuleRowsFrom(states),
		Flash:     settingsFlash(r),
	}

	if IsHTMX(r) {
		admin.SettingsModulesContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.SettingsModules(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminModuleToggle switches one module on or off.
func (d *Deps) handleAdminModuleToggle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}
	key := r.FormValue("module")
	enabled := r.FormValue("enabled") == "true"

	var info domain.ModuleInfo
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		info, txErr = d.ModuleService.SetEnabled(ctx, tx, key, enabled, staffActor(r))
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	// Refresh the cache only now the change is committed — the same rule
	// metrics follow. A refresh inside the transaction would publish a toggle
	// that a later failure rolled back, leaving the sidebar and the database
	// disagreeing until the next deploy.
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.ModuleService.Refresh(ctx, tx)
	}); err != nil {
		// The write succeeded; only the in-memory view is stale. Say so rather
		// than reporting a failure that did not happen — and log it, because
		// until this is refreshed the section stays hidden.
		d.Logger.ErrorContext(ctx, "refresh module cache after toggle", "error", err, "module", key)
		redirectFlashError(w, r, "/admin/settings/modules",
			info.Name+" was saved, but the change will not show until the next restart.")
		return
	}

	verb := "turned off"
	if enabled {
		verb = "turned on"
	}
	redirectFlash(w, r, "/admin/settings/modules", info.Name+" "+verb+".")
}
