// Package build holds version metadata stamped into the binary at link time.
//
// The Version and Commit vars are overridden at build time via -ldflags, e.g.:
//
//	go build -ldflags "\
//	  -X github.com/dukerupert/hiri/internal/platform/build.Version=v1.73.1 \
//	  -X github.com/dukerupert/hiri/internal/platform/build.Commit=163e99a" \
//	  ./cmd/server
//
// Unset (local `go run`/`go build`), they fall back to the placeholders below so
// GET /version still answers — it just reports "dev".
package build

import "runtime"

var (
	// Version is the release tag the binary was built from (e.g. "v1.73.1").
	Version = "dev"
	// Commit is the git SHA the binary was built from.
	Commit = "unknown"
)

// Info is the structured build metadata served by GET /version.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Go      string `json:"go"`
}

// Current returns the build metadata for this binary.
func Current() Info {
	return Info{Version: Version, Commit: Commit, Go: runtime.Version()}
}
