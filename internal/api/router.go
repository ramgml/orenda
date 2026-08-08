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
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/backup"
	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	eventservice "github.com/ramgml/orenda/internal/service/event"
	notifierservice "github.com/ramgml/orenda/internal/service/notifier"
	searchservice "github.com/ramgml/orenda/internal/service/search"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	timeentryservice "github.com/ramgml/orenda/internal/service/timeentry"
	wikiservice "github.com/ramgml/orenda/internal/service/wiki"
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
	Logger              *zap.Logger
	Signer              *auth.Signer
	Users               user.Repository
	Projects            project.Repository
	Tasks               task.Repository
	Tokens              APITokenLookup
	TaskService         *taskservice.Service
	Agents              agent.Repository
	AgentService        *agentservice.Service
	Comments            CommentService
	Attachments         AttachmentService
	Activities          ActivityService
	EventService        *eventservice.Service
	TimeService         *timeentryservice.Service
	WikiService         *wikiservice.Service
	SearchService       *searchservice.Service
	Notifier            *notifierservice.Service
	Backup              *backup.Service
	BackupEnabled       bool
	BackupRemoteURL     string
	BackupRemoteAuthSet bool
	SyncOps             SyncOpsStore
	WSHub               ws.Hub
	CookieName          string
	Capabilities        Capabilities
}

// SyncOpsStore is the small surface the sync endpoint needs for
// idempotency. The SQLite implementation records applied client_ids.
type SyncOpsStore interface {
	Seen(ctx context.Context, clientID string) (bool, string, error)
	Record(ctx context.Context, clientID, serverID string) error
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
	r.Use(securityHeaders())
	r.Use(rateLimit(rateLimitOptions{
		AnonBurst:  60,
		AnonPerSec: 20,
		AuthBurst:  300,
		AuthPerSec: 100,
		SkipPaths: map[string]bool{
			"/healthz":   true,
			"/api/v1/ws": true,
		},
	}))

	cfg := AuthConfig{
		Signer:     deps.Signer,
		Users:      deps.Users,
		Tokens:     deps.Tokens,
		Agents:     deps.Agents,
		CookieName: deps.CookieName,
	}

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
		// WebSocket: authenticates via ?token=<jwt> in query because the
		// browser WS API can't set headers. See internal/api/ws.Handler.
		if deps.Signer != nil && deps.WSHub != nil {
			r.Handle("/ws", ws.Handler(deps.WSHub, deps.Signer))
		}

		// Auth: public endpoints.
		r.Route("/auth", func(r chi.Router) {
			r.Post("/login", loginHandler(deps))
			r.Post("/logout", logoutHandler(deps))
		})

		// Authenticated routes.
		r.Group(func(r chi.Router) {
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
					r.Post("/move", moveTaskHandler(deps))
					r.Post("/claim", claimTaskHandler(deps))
					r.Post("/release", releaseTaskHandler(deps))
					r.Post("/submit", submitTaskHandler(deps))
					r.Post("/review", reviewTaskHandler(deps))
					r.Get("/subtasks", listSubtasksHandler(deps))
					r.Post("/subtasks", addSubtaskHandler(deps))
					r.Get("/comments", listTaskCommentsHandler(deps))
					r.Post("/comments", createTaskCommentHandler(deps))
					r.Post("/attachments", addTaskAttachmentHandler(deps))
					r.Get("/activity", listTaskActivityHandler(deps))
					r.Get("/context", getTaskContextHandler(deps))
					// Phase 4: timer endpoints
					r.Post("/timer/start", startTimerHandler(deps))
					r.Post("/timer/stop", stopTimerHandler(deps))
					r.Post("/time", addManualTimeHandler(deps))
				})
			})

			r.Route("/columns", func(r chi.Router) {
				r.Route("/{id}", func(r chi.Router) {
					r.Patch("/", patchColumnHandler(deps))
				})
			})

			// Long-poll for agents without WebSocket support. Subscribe
			// to one topic and return the first matching event.
			r.Post("/events/await", awaitHandler(deps))

			r.Route("/agents", func(r chi.Router) {
				r.Get("/", listAgentsHandler(deps))
				r.Post("/", createAgentHandler(deps))
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", getAgentHandler(deps))
					r.Delete("/", deleteAgentHandler(deps))
					r.Post("/heartbeat", heartbeatHandler(deps))
				})
			})

			// Phase 4: calendar + time tracking.
			r.Route("/events", func(r chi.Router) {
				r.Get("/", listEventsHandler(deps))
				r.Post("/", createEventHandler(deps))
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", getEventHandler(deps))
					r.Patch("/", updateEventHandler(deps))
					r.Delete("/", deleteEventHandler(deps))
				})
			})

			r.Get("/reports/time", reportTimeHandler(deps))

			// Phase 5: wiki + FTS5 search.
			r.Route("/pages", func(r chi.Router) {
				r.Get("/", listPagesHandler(deps))
				r.Post("/", savePageHandler(deps))
				r.Route("/{slug}", func(r chi.Router) {
					r.Get("/", getPageHandler(deps))
					r.Put("/", savePageHandler(deps))
					r.Get("/backlinks", getPageBacklinksHandler(deps))
				})
			})

			r.Get("/search", searchHandler(deps))

			// Phase 6: notifications inbox.
			r.Get("/notifications", listNotificationsHandler(deps))
			r.Post("/notifications/{id}/read", markNotificationReadHandler(deps))

			// Phase 8: offline sync.
			r.Post("/sync", syncHandler(deps))

			// Phase 7: backups.
			r.Route("/backups", func(r chi.Router) {
				r.Get("/settings", listBackupSettingsHandler(deps))
				r.Put("/settings", putBackupSettingsHandler(deps))
				r.Post("/test", testBackupPushHandler(deps))
				r.Post("/snapshot", backupSnapshotHandler(deps))
				r.Get("/snapshots", listBackupSnapshotsHandler(deps))
				r.Get("/log", listBackupLogHandler(deps))
			})
		})

		// Agent-authenticated routes: agents authenticate via
		// Authorization: Bearer <api-token>. Mounted under /api/v1/agent/*
		// to keep the auth model distinct from user-cookie routes.
		// This group is intentionally OUTSIDE the user RequireUser group
		// so the agent middleware isn't shadowed.
		if deps.Agents != nil {
			r.Group(func(r chi.Router) {
				r.Use(RequireAgent(cfg))
				r.Get("/agent/me", agentMeHandler(deps))
				r.Post("/agent/heartbeat", agentHeartbeatHandler(deps))
				r.Route("/agent/tasks", func(r chi.Router) {
					r.Route("/{id}", func(r chi.Router) {
						r.Post("/claim", agentClaimTaskHandler(deps))
						r.Post("/release", agentReleaseTaskHandler(deps))
						r.Post("/submit", agentSubmitTaskHandler(deps))
						r.Get("/context", agentTaskContextHandler(deps))
					})
				})
			})
		}
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
