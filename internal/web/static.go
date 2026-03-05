package web

import (
	"net/http"
	"path/filepath"
	"strings"
)

// staticHandler serves static assets from the given root directory.
// In production, this should be replaced with go:embed or a CDN.
func staticHandler(root string) http.Handler {
	fs := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set cache headers based on file type
		ext := strings.ToLower(filepath.Ext(r.URL.Path))
		switch ext {
		case ".css", ".js":
			w.Header().Set("Cache-Control", "public, max-age=3600")
		default:
			w.Header().Set("Cache-Control", "public, max-age=86400")
		}
		fs.ServeHTTP(w, r)
	})
}
