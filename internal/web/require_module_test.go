package web

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
)

// A module that is switched off must not serve its routes. This is the
// direction worth a test: an inverted condition here would leave a whole
// section reachable by URL on every shop that never asked for it, with nothing
// in the sidebar to reveal it.
//
// The enabled direction needs a populated cache, which needs a database — it is
// covered by the app-layer tests in internal/app/modules_test.go.
func TestRequireModuleBlocksDisabledModule(t *testing.T) {
	// A service that has never been refreshed reports every module disabled,
	// which is the same state a failed refresh leaves behind.
	deps := &Deps{
		Logger:        slog.New(slog.DiscardHandler),
		ModuleService: app.NewModuleService(store.NewModuleStore(), nil),
	}

	served := false
	guarded := deps.requireModule(domain.ModuleEquipmentService, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		served = true
	}))

	rec := httptest.NewRecorder()
	guarded.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/service", nil))

	assert.False(t, served, "a disabled module must not reach its handler")
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"a disabled module looks like a URL that was never built, not a permission problem")
}
