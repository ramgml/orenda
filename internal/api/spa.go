// Package api — embedded SPA handler with client-side routing fallback.
package api

import (
	"io/fs"
	"net/http"
	"strings"

	webembed "github.com/ramgml/orenda/internal/embed/web"
)

// spaHandler serves files from the embedded React SPA.
//
// For any request whose path resolves to a real file in the FS, that file is
// returned with appropriate Content-Type. Otherwise index.html is served
// so that client-side routing (react-router) can take over.
//
// When the embed is empty (dev build before `make web-build`), a 404 is
// returned for everything except /healthz and /api/* which are routed
// separately by chi.
func spaHandler() http.HandlerFunc {
	sub, err := webembed.DistSubFS()
	if err != nil {
		return func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "internal: cannot load web assets", http.StatusInternalServerError)
		}
	}
	fileServer := http.FileServer(http.FS(sub))

	return func(w http.ResponseWriter, r *http.Request) {
		upath := r.URL.Path
		if upath == "" || upath == "/" {
			serveIndex(w, r, sub)
			return
		}

		// Strip leading slash to match fs.FS conventions.
		rel := strings.TrimPrefix(upath, "/")
		if rel == "" {
			serveIndex(w, r, sub)
			return
		}
		if info, err := fs.Stat(sub, rel); err == nil && !info.IsDir() {
			// Serve the file; http.FileServer uses URL.Path so wrap in a
			// synthetic request that matches the file's location.
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/" + rel
			w.Header().Set("Cache-Control", "public, max-age=300")
			fileServer.ServeHTTP(w, r2)
			return
		}

		// SPA fallback: any other path → index.html (assumes the bundler
		// emits hashed asset filenames under /assets/ which we already
		// matched above).
		if !strings.HasPrefix(upath, "/api/") && !strings.HasPrefix(upath, "/healthz") {
			serveIndex(w, r, sub)
			return
		}

		http.NotFound(w, r)
	}
}

// serveIndex writes web/dist/index.html with a short cache TTL.
//
// In dev (no embedded dist) it returns 404 with a helpful hint.
func serveIndex(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	body, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("Orenda web assets not built. Run `make web-build` or `make dev`.\n"))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
