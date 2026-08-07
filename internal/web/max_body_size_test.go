package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readAllHandler drains the body and reports whether it hit the cap. Mirrors what
// ParseMultipartForm does to an oversized upload.
func readAllHandler(t *testing.T, gotErr *error, gotLen *int) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		*gotErr = err
		*gotLen = len(b)
	})
}

func TestMaxBodySizeMiddleware(t *testing.T) {
	const defaultLimit = 1 << 20

	post := func(path string, size int) *http.Request {
		return httptest.NewRequest(http.MethodPost, path, strings.NewReader(strings.Repeat("x", size)))
	}

	t.Run("default limit applies to ordinary routes", func(t *testing.T) {
		var err error
		var n int
		h := maxBodySizeMiddleware(readAllHandler(t, &err, &n), defaultLimit)
		h.ServeHTTP(httptest.NewRecorder(), post("/wholesale/apply", defaultLimit+1))

		require.Error(t, err)
		var tooLarge *http.MaxBytesError
		assert.ErrorAs(t, err, &tooLarge)
	})

	t.Run("webhooks are exempt entirely", func(t *testing.T) {
		var err error
		var n int
		h := maxBodySizeMiddleware(readAllHandler(t, &err, &n), defaultLimit)
		h.ServeHTTP(httptest.NewRecorder(), post("/webhooks/stripe", defaultLimit+512))

		require.NoError(t, err)
		assert.Equal(t, defaultLimit+512, n)
	})

	// The regression that broke white-label uploads: label art is routinely over
	// 1 MB, and the global cap truncated it before the handler's own 10 MB check
	// could produce a friendly error.
	t.Run("white-label accepts an upload above the default cap", func(t *testing.T) {
		var err error
		var n int
		h := maxBodySizeMiddleware(readAllHandler(t, &err, &n), defaultLimit)
		body := 4 << 20 // a normal high-res label
		h.ServeHTTP(httptest.NewRecorder(), post("/wholesale/white-label", body))

		require.NoError(t, err)
		assert.Equal(t, body, n)
	})

	t.Run("white-label override still has a ceiling", func(t *testing.T) {
		var err error
		var n int
		h := maxBodySizeMiddleware(readAllHandler(t, &err, &n), defaultLimit)
		over := int(bodyLimitOverrides["/wholesale/white-label"]) + 1
		h.ServeHTTP(httptest.NewRecorder(), post("/wholesale/white-label", over))

		require.Error(t, err)
		var tooLarge *http.MaxBytesError
		assert.ErrorAs(t, err, &tooLarge)
	})

	// The override has to clear the handler's own limit, or the middleware wins
	// first and maxLabelImageBytes becomes unreachable.
	t.Run("override leaves headroom above the handler limit", func(t *testing.T) {
		assert.Greater(t, bodyLimitOverrides["/wholesale/white-label"], int64(maxLabelImageBytes))
	})
}
