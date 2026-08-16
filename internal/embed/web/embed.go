// Package webembed exposes the React SPA build output as a Go embed.FS
// so the production binary is self-contained (single-file deploy).
//
// Build mechanics:
//
//   - `internal/embed/web/dist/` is the embed root. The directory always
//     exists (it ships with an empty `.gitkeep`) so `//go:embed all:dist`
//     compiles unconditionally. In a clean checkout or running under `go
//     test`, the directory is empty and the FS holds only vite chunks if
//     the Makefile has just copied `web/dist/` into it before `go build`.
//   - `make build` runs `make web-build` (npm run build → web/dist/), then
//     copies the contents into `internal/embed/web/dist/` right before the
//     Go compile. The final binary therefore contains the SPA verbatim.
//   - Dev workflows (`make dev`, `npm run dev`, plain `go test ./...`) skip
//     the copy step. DistSubFS falls back to the on-disk `web/dist/`
//     directory so the live Vite output is served without rebuilding Go.
//
// File precedence inside DistSubFS:
//
//  1. Embedded `dist/` (post-`make build`): self-contained, no disk dependency.
//  2. On-disk `web/dist/index.html`: dev / `go test` fallback — lets the
//     Vite dev-server output be served without touching the Go binary.
//  3. Placeholder FS (empty): returns 404 for every static request, which
//     the API router translates into a clean "API only" surface.
package webembed

import (
	"embed"
	"io/fs"
	"os"
)

// FS holds the embedded web/dist directory plus the legacy placeholder
// file. The placeholder is kept so the embed directive always resolves to
// at least one file (the pre-Phase-27 behaviour was the same — the binary
// shipped with `placeholder.txt` and no SPA).
//
// The //go:embed directive is a compile-time lookup; if `dist/` is missing
// at build time the build fails. The Makefile is the only consumer that
// populates it; do not delete the directory.
//
//go:embed all:dist
//go:embed placeholder.txt
var FS embed.FS

// DistSubFS returns the web/dist sub-filesystem, suitable for serving
// static files via http.FileServer. When the embed is empty (default dev
// build), it falls back to looking for an on-disk web/dist directory so
// `make dev` and `go test` can serve the live Vite output without
// rebuilding the Go binary.
func DistSubFS() (fs.FS, error) {
	// 1) Try the embedded dist/ sub-FS first. After `make build` it
	// contains the full SPA.
	if dist, err := fs.Sub(FS, "dist"); err == nil {
		if hasIndex(dist) {
			return dist, nil
		}
	}

	// 2) Fall back to the on-disk build (Vite dev-server writes here).
	// This lets `go test` and `make dev` serve the live Vite output
	// without rebuilding the Go binary.
	if _, statErr := os.Stat("web/dist/index.html"); statErr == nil {
		return os.DirFS("web/dist"), nil
	}

	// 3) No dist available: return the placeholder FS so the API still
	// mounts (handlers return 404 for static requests).
	return FS, nil
}

// hasIndex reports whether fsys contains an index.html at its root.
func hasIndex(fsys fs.FS) bool {
	if _, err := fs.Stat(fsys, "index.html"); err == nil {
		return true
	}
	return false
}
