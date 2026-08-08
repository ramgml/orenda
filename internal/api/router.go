// Package api wires the HTTP layer: chi router, middleware, and the Phase 0
// endpoints (health, info, embedded SPA).
//
// Phase 0 surface area is intentionally tiny:
//
//	GET /healthz         → liveness probe (200 OK)
//	GET /api/v1/info     → version and capability advertisement
//	GET /*               → embedded React SPA (or 404 placeholder)
//
// Authentication, REST resources, and WebSocket land in later phases.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

// Version is the build-time version string.
//
// Set via -ldflags "-X github.com/ramgml/orenda/internal/api.Version=..." at
// build time. The Makefile already injects this from `git describe`.
var Version = "0.1.0-dev"

// Capabilities describes which optional features are compiled in.
type Capabilities struct {
	Auth      bool `json:"auth"`
	RESTTasks bool `json:"rest_tasks"`
	WebSocket bool `json:"websocket"`
	Backup    bool `json:"backup"`
	Bots      bool `json:"bots"`
	FTS       bool `json:"fts"`
	PWA       bool `json:"pwa"`
}

// infoResponse is the payload returned by /api/v1/info.
type infoResponse struct {
	Version      string       `json:"version"`
	Name         string       `json:"name"`
	Capabilities Capabilities `json:"capabilities"`
}

// Options customises the router construction.
type Options struct {
	// Logger is the structured logger used by the request-logging middleware.
	// If nil, zap.NewNop() is used.
	Logger *zap.Logger

	// Capabilities controls the `capabilities` field of /api/v1/info.
	// All flags default to false in Phase 0.
	Capabilities Capabilities
}

// NewRouter constructs a chi router with Phase 0 endpoints wired.
//
// The returned router is ready to be wrapped by http.Server; it does not
// start listening on its own.
func NewRouter(opts Options) http.Handler {
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	r := chi.NewRouter()

	r.Use(requestIDHeader())
	r.Use(realIP())
	r.Use(requestLogger(logger))
	r.Use(recoverer())
	r.Use(corsLoopback())

	// Liveness probe — no DB ping in Phase 0 to keep dependencies minimal.
	// Phase 1+ will add a /readyz that pings the database.
	r.Get("/healthz", healthzHandler)

	// API surface (v1). All real resources land here in later phases.
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/info", infoHandler(Version, opts.Capabilities))
	})

	// Static SPA: serve embedded web/dist, with client-side fallback to
	// index.html so react-router can handle arbitrary paths.
	r.Get("/*", spaHandler())

	return r
}

// healthzHandler reports liveness.
//
// Always returns 200 with a small JSON body. Phase 1+ will add /readyz that
// pings the database.
func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": Version,
	})
}

// infoHandler returns the version + capability advertisement.
func infoHandler(version string, caps Capabilities) http.HandlerFunc {
	resp := infoResponse{
		Version:      version,
		Name:         "orenda",
		Capabilities: caps,
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, resp)
	}
}

// writeJSON marshals body as JSON and writes it with the given status.
//
// Errors during encoding are returned to the caller via http.Error with a
// 500 status; the response may already be partially written.
func writeJSON(w http.ResponseWriter, status int, body any) {
	buf, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "internal: marshal json", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf)
}

// requestIDHeader, realIP, recoverer are thin local wrappers around chi
// middleware so the package doesn't need a direct import of chi/middleware
// in router.go (and the import is in one place only: middleware.go).

func requestIDHeader() func(http.Handler) http.Handler {
	return requestIDMiddleware
}

func realIP() func(http.Handler) http.Handler {
	return realIPMiddleware
}

func recoverer() func(http.Handler) http.Handler {
	return recovererMiddleware
}
