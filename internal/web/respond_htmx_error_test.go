package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An htmx error response is a toast and nothing else. htmx lifts top-level
// hx-swap-oob elements out of the response before swapping, so without
// HX-Reswap the remaining empty fragment would be swapped into the target —
// deleting a self-targeting form (hx-target="this" hx-swap="outerHTML") off
// the page, or blanking the body of a boosted one.
func TestError_HTMX_CancelsTheMainSwap(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/catalog/x/variants/y/wholesale-moq", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	Error(rec, req, app.ErrInvalidWholesaleMOQ)

	assert.Equal(t, "none", rec.Header().Get("HX-Reswap"))
	assert.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	require.Contains(t, body, "hx-swap-oob", "the toast must still swap out of band")
	assert.NotContains(t, body, "<form", "the error response carries no replacement markup")
}

// The non-htmx path is unchanged: real status code, JSON body, no htmx headers.
func TestError_NonHTMX_UnchangedJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/catalog/x/variants/y/wholesale-moq", nil)
	rec := httptest.NewRecorder()

	Error(rec, req, app.ErrInvalidWholesaleMOQ)

	assert.Empty(t, rec.Header().Get("HX-Reswap"))
	assert.NotEqual(t, http.StatusOK, rec.Code)
	assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json"))
}

// The hand-rolled toast-only helpers carry the same header as Error — they
// have the same empty-fragment problem.
func TestRenderToastOnlyErrors_CancelTheMainSwap(t *testing.T) {
	for name, render := range map[string]func(http.ResponseWriter, *http.Request, string){
		"discount": renderDiscountError,
		"plan":     renderPlanError,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/"+name+"s", nil)
			req.Header.Set("HX-Request", "true")
			rec := httptest.NewRecorder()

			render(rec, req, "Nope.")

			assert.Equal(t, "none", rec.Header().Get("HX-Reswap"))
			assert.Contains(t, rec.Body.String(), "hx-swap-oob")
		})
	}
}
