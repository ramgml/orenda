// Package api wires the HTTP layer: chi router, middleware, and the Phase 1
// endpoints (auth, projects, tasks, plus the static SPA from Phase 0).
//
// Phase 1 surface area:
//
//	GET    /healthz
//	GET    /api/v1/info
//	POST   /api/v1/auth/login
//	POST   /api/v1/auth/logout
//	GET    /api/v1/me
//	GET    /api/v1/projects
//	POST   /api/v1/projects
//	GET    /api/v1/projects/{id}
//	PATCH  /api/v1/projects/{id}
//	DELETE /api/v1/projects/{id}
//	GET    /api/v1/projects/{id}/board
//	GET    /api/v1/projects/{id}/tasks
//	POST   /api/v1/projects/{id}/tasks
//	GET    /api/v1/tasks/{id}
//	PATCH  /api/v1/tasks/{id}
//	PUT    /api/v1/tasks/{id}            (alias for PATCH)
//	DELETE /api/v1/tasks/{id}
//	GET    /api/v1/tasks/{id}/subtasks
//	POST   /api/v1/tasks/{id}/subtasks
//	GET    /*
//
// Authentication, REST resources, and WebSocket land in later phases.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
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

// Dependencies wires every repository and the JWT signer into the router.
//
// Constructing this struct lives in cmd/orenda so the api package stays
// independent of the storage layer.
type Dependencies struct {
	Logger       *zap.Logger
	Signer       *auth.Signer
	Users        user.Repository
	Projects     project.Repository
	Tasks        task.Repository
	Tokens       APITokenLookup
	CookieName   string
	Capabilities Capabilities
}

// NewRouter constructs a chi router with the Phase 1 endpoints wired.
//
// The returned router is ready to be wrapped by http.Server; it does not
// start listening on its own.
func NewRouter(deps Dependencies) http.Handler {
	logger := deps.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	if deps.CookieName == "" {
		deps.CookieName = "orenda_session"
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

	// API surface (v1).
	r.Route("/api/v1", func(r chi.Router) {
		caps := deps.Capabilities
		if !caps.Auth && deps.Signer != nil {
			// Phase 1 default: advertise auth + REST tasks whenever the
			// server actually has a signer wired in.
			caps.Auth = true
			caps.RESTTasks = true
		}
		r.Get("/info", infoHandler(Version, caps))

		// Auth: public endpoints.
		r.Route("/auth", func(r chi.Router) {
			r.Post("/login", loginHandler(deps))
			r.Post("/logout", logoutHandler(deps))
		})

		// Authenticated routes.
		r.Group(func(r chi.Router) {
			cfg := AuthConfig{
				Signer:     deps.Signer,
				Users:      deps.Users,
				Tokens:     deps.Tokens,
				CookieName: deps.CookieName,
			}
			r.Use(RequireUser(cfg))

			r.Get("/me", meHandler())

			r.Route("/projects", func(r chi.Router) {
				r.Get("/", listProjectsHandler(deps))
				r.Post("/", createProjectHandler(deps))
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", getProjectHandler(deps))
					r.Patch("/", patchProjectHandler(deps))
					r.Delete("/", deleteProjectHandler(deps))
					r.Get("/board", getProjectBoardHandler(deps))
					r.Get("/tasks", listProjectTasksHandler(deps))
					r.Post("/tasks", createTaskHandler(deps))
				})
			})

			r.Route("/tasks", func(r chi.Router) {
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", getTaskHandler(deps))
					r.Patch("/", patchTaskHandler(deps))
					r.Put("/", patchTaskHandler(deps)) // alias
					r.Delete("/", deleteTaskHandler(deps))
					r.Get("/subtasks", listSubtasksHandler(deps))
					r.Post("/subtasks", addSubtaskHandler(deps))
				})
			})
		})
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
