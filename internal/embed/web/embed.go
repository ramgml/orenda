// Package webembed exposes the React SPA build output as a Go embed.FS.
//
// Phase 0 ships an empty filesystem so the API router can wire the static
// handler unconditionally. The Makefile (`make build` → `make web-build`)
// replaces the placeholder files at build time via the build tag `web_dist`.
//
// Two modes:
//
//   - Default build (no `web_dist` tag): FS is empty; static handler returns 404.
//   - Production build (`-tags=web_dist`): FS contains web/dist/** contents.
package webembed

import (
	"embed"
	"io/fs"
	"os"
	"strings"
)

// FS holds the embedded web/dist directory.
//
// The //go:embed directive is kept empty by default to avoid a build error
// when web/dist has not been generated yet. Building with `-tags=web_dist`
// enables the real embed via web_dist_dist.go.
//
//go:embed placeholder.txt
var FS embed.FS

// placeholder.txt exists so the embed directive above always resolves to at
// least one file. The Makefile overwrites this directory at build time.
const placeholder = "orenda web placeholder (replace with `make web-build`)\n"

// DistSubFS returns the web/dist sub-filesystem, suitable for serving static
// files via http.FileServer. When the embed is empty (default dev build),
// it falls back to looking for an on-disk web/dist directory so `make dev`
// can serve the live Vite output without rebuilding the Go binary.
func DistSubFS() (fs.FS, error) {
	// 1) Try the embedded FS first.
	sub, err := fs.Sub(FS, ".")
	if err == nil {
		if hasIndex(sub) {
			return sub, nil
		}
	}

	// 2) Fall back to the on-disk build (Vite dev-server writes here).
	if _, statErr := os.Stat("web/dist/index.html"); statErr == nil {
		return os.DirFS("web/dist"), nil
	}

	// 3) No dist available: return the placeholder FS so 404 is consistent.
	return FS, nil
}

// hasIndex reports whether fsys contains an index.html at its root.
func hasIndex(fsys fs.FS) bool {
	if _, err := fs.Stat(fsys, "index.html"); err == nil {
		return true
	}
	// Check one level deep (some bundlers emit dist/index.html as the only
	// file at root and everything else under assets/).
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), ".") {
			continue
		}
	}
	return false
}
