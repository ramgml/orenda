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
//	GET    /api/v1/inbox/tasks
//	POST   /api/v1/inbox/tasks
//	GET    /*
//
// Authentication, REST resources, and WebSocket land in later phases.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/backup"
	"github.com/ramgml/orenda/internal/bot"
	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/course"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	coursesvc "github.com/ramgml/orenda/internal/service/course"
	eventservice "github.com/ramgml/orenda/internal/service/event"
	notifierservice "github.com/ramgml/orenda/internal/service/notifier"
	searchservice "github.com/ramgml/orenda/internal/service/search"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	timeentryservice "github.com/ramgml/orenda/internal/service/timeentry"
	wikiservice "github.com/ramgml/orenda/internal/service/wiki"
	"github.com/ramgml/orenda/internal/storage/sqlite"
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
	TaskService  *taskservice.Service
	Agents       agent.Repository
	AgentService *agentservice.Service
	Comments     CommentService
	Attachments  AttachmentService
	Activities   ActivityService
	// ActivityRecorder is the write side for task_activity rows.
	// nil-safe (handlers must guard). Phase 28.5: wired so
	// createTaskCommentHandler / addTaskAttachmentHandler can emit
	// task.commented / task.attachment_added.
	ActivityRecorder    ActivityRecorder
	EventService        *eventservice.Service
	TimeService         *timeentryservice.Service
	WikiService         *wikiservice.Service
	SearchService       *searchservice.Service
	Notifier            *notifierservice.Service
	Backup              *backup.Service
	BackupEnabled       bool
	BackupRemoteURL     string
	BackupRemoteAuthSet bool
	// BackupSettings is the UI-facing repo for the backup_settings
	// table (Phase 28.1 polish.1). Until Phase 7 it wasn't wired,
	// which is why PUT /backups/settings returned 501; the GET
	// path here can be nil-safe (only PUT + the GET merge needs
	// it; partial-router fixtures can leave it nil).
	BackupSettings sqlite.BackupSettingsRepository
	SyncOps        SyncOpsStore
	BotCallback    *bot.CallbackHandler
	// BotBindCodes is the (optional) bind-code store. Wired only when
	// the Telegram bot is running; nil-safe — the API returns 503
	// with a friendly hint when the user tries to bind while the
	// bot is offline.
	BotBindCodes   BindCodesSource
	VKSecret       string
	VKConfirmation string
	WSHub          ws.Hub
	CookieName     string
	// Phase 28.4: whether the session cookie should carry the
	// `Secure` attribute. Defaults to false so the cookie still
	// flows over plain HTTP on loopback dev installs. Operators
	// serving over HTTPS (reverse proxy or direct TLS) must set
	// `auth.cookie_secure: true` in config.yaml — the cookie would
	// otherwise leak over plain HTTP if the user later hits the
	// site on http://.
	CookieSecure bool
	// Phase 28.4: session-cookie lifetime. Mirrors `Auth.JWTTTL`
	// so the cookie's `Expires` matches the embedded JWT exp —
	// otherwise a cookie can outlive its token (or vice versa)
	// and RequireUser silently fails on otherwise-valid sessions.
	JWTTTL time.Duration
	// Phase 28.8: token-bucket rate-limit knobs. Source is
	// config.RateLimit (yaml + env override). Defaults to the
	// pre-28.8 inline values (anon 60/20, auth 300/100) when
	// all four are zero — defensive against a partially-filled
	// yaml section.
	RateLimitAnonBurst  int
	RateLimitAnonPerSec float64
	RateLimitAuthBurst  int
	RateLimitAuthPerSec float64
	Capabilities        Capabilities
	// Phase 24: absolute path to the SQLite file so /api/v1/stats
	// can report its size. Optional — left empty in tests that
	// don't run a real DB.
	DBPath string
	// Phase 18: course repository + service.
	Courses       course.Repository
	CourseService *coursesvc.Service
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
	// Expose to writeError for unexpected (500) errors. Tests can
	// override via SetAPILogger before calling NewRouter.
	apiLogger = logger
	if deps.CookieName == "" {
		deps.CookieName = "orenda_session"
	}

	r := chi.NewRouter()

	r.Use(requestIDHeader())
	r.Use(realIP())
	r.Use(requestLogger(logger))
	r.Use(recoverer())
	// Phase 22.3: maintenance mode is checked on every request; it
	// rejects non-safe methods when the operator has flipped the
	// flag on. The toggle endpoints (POST /api/v1/maintenance/on|off)
	// are mounted AFTER the middleware so the operator can always
	// turn maintenance off again.
	r.Use(maintenanceMiddleware)
	r.Use(corsLoopback())
	r.Use(securityHeaders())
	// Phase 28.8: rate-limit knobs now live in config.RateLimit
	// (yaml + env). Env vars set by Phase 26.E still win — the
	// config layer applied them before reaching us, so
	// anonBurst here already reflects the E2E's cranked value.
	anonBurst, anonPerSec := deps.RateLimitAnonBurst, deps.RateLimitAnonPerSec
	authBurst, authPerSec := deps.RateLimitAuthBurst, deps.RateLimitAuthPerSec
	// Defensive: a zero rate_limit section (operator wrote
	// `rate_limit:` with no children in YAML, then env didn't
	// override) would zero out the limiter. The router used
	// to inline the defaults; preserve that.
	if anonBurst == 0 {
		anonBurst = 60
	}
	if authBurst == 0 {
		authBurst = 300
	}
	if anonPerSec == 0 {
		anonPerSec = 20.0
	}
	if authPerSec == 0 {
		authPerSec = 100.0
	}
	r.Use(rateLimit(rateLimitOptions{
		AnonBurst:  anonBurst,
		AnonPerSec: anonPerSec,
		AuthBurst:  authBurst,
		AuthPerSec: authPerSec,
		SkipPaths: map[string]bool{
			"/healthz":           true,
			"/api/v1/ws":         true,
			"/api/v1/me":         true,
			"/api/v1/auth/login": true,
		},
	}))
	zap.L().Info("rate limit config",
		zap.Int("anon_burst", anonBurst),
		zap.Float64("anon_per_sec", anonPerSec),
		zap.Int("auth_burst", authBurst),
		zap.Float64("auth_per_sec", authPerSec),
	)

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

	// Phase 24: minimal observability. /api/v1/stats returns a
	// counter snapshot (no auth — the only sensitive bit is the DB
	// path, which is harmless). Health checks and external monitors
	// can hit it without a session.
	r.Get("/api/v1/stats", getStatsHandler(deps.WSHub, deps.DBPath))

	// Phase 24: machine-readable contract for external agents.
	// Public — the spec isn't secret, and matching docs/API.md
	// means "everything documented is reachable".
	r.Get("/api/v1/openapi.yaml", openAPIHandler())

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
		// WebSocket: Phase 27.2 — authenticates via the orenda_session
		// cookie (same-origin browser WS sends it automatically). The
		// handler still accepts ?token=<jwt> for curl / external clients.
		// See internal/api/ws.Handler for the precedence rules.
		if deps.Signer != nil && deps.WSHub != nil {
			r.Handle("/ws", ws.Handler(deps.WSHub, deps.Signer, deps.CookieName))
		}

		// Auth: public endpoints.
		r.Route("/auth", func(r chi.Router) {
			r.Post("/login", loginHandler(deps))
			r.Post("/logout", logoutHandler(deps))
		})

		// Phase 10: bot webhooks (no auth — verified via shared secret).
		r.Post("/webhooks/vk", vkWebhookHandler(deps))

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
					// Phase 12: user-managed columns (create + reorder +
					// rename). Updates use PATCH /columns/:id below.
					r.Post("/columns", createColumnHandler(deps))
					r.Get("/tasks", listProjectTasksHandler(deps))
					r.Post("/tasks", createTaskHandler(deps))
					// Phase 11: project page tabs.
					r.Get("/activity", listProjectActivityHandler(deps))
					r.Get("/attachments", listProjectAttachmentsHandler(deps))
					r.Post("/attachments", addProjectAttachmentHandler(deps))
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
					// Phase 14: subtasks → child tasks via parent_task_id.
					// Create a child through POST /api/v1/projects/:pid/tasks
					// with `parent_task_id` set; list with GET /children.
					r.Get("/children", listChildTasksHandler(deps))
					// Phase 15: dependencies (user-side, cookie auth).
					r.Get("/blockers", getTaskBlockersHandler(deps))
					r.Get("/dependents", getTaskDependentsHandler(deps))
					r.Put("/dependencies", putTaskDependenciesHandler(deps))
					// Phase 13: per-task tag assignment.
					r.Get("/tags", listTaskTagsHandler(deps))
					r.Put("/tags", setTaskTagsHandler(deps))
					r.Get("/checklists", listChecklistsHandler(deps))
					r.Post("/checklists", addChecklistHandler(deps))
					r.Route("/checklists/{clId}", func(r chi.Router) {
						r.Delete("/", deleteChecklistHandler(deps))
						r.Get("/items", listChecklistItemsHandler(deps))
						r.Post("/items", addChecklistItemHandler(deps))
						r.Route("/items/{itemId}", func(r chi.Router) {
							r.Patch("/", updateChecklistItemHandler(deps))
							r.Delete("/", deleteChecklistItemHandler(deps))
						})
					})
					r.Get("/comments", listTaskCommentsHandler(deps))
					r.Post("/comments", createTaskCommentHandler(deps))
					r.Get("/attachments", listTaskAttachmentsHandler(deps))
					r.Post("/attachments", addTaskAttachmentHandler(deps))
					r.Route("/attachments/{attId}", func(r chi.Router) {
						r.Get("/download", downloadAttachmentHandler(deps))
					})
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
					// Phase 12.6: delete a (must be empty) column.
					r.Delete("/", deleteColumnHandler(deps))
				})
			})

			// Phase 13: tag catalogue (global — not per-project).
			r.Route("/tags", func(r chi.Router) {
				r.Get("/", listTagsHandler(deps))
				r.Post("/", createTagHandler(deps))
				r.Route("/{id}", func(r chi.Router) {
					r.Patch("/", patchTagHandler(deps))
					r.Delete("/", deleteTagHandler(deps))
				})
			})

			// Global attachment download — works for any task attachment
			// regardless of which project/task it lives in.
			r.Route("/attachments/{attId}", func(r chi.Router) {
				r.Get("/download", downloadAttachmentHandler(deps))
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

			// Phase 16: Inbox — tasks with project_id IS NULL. Listed and
			// created via dedicated endpoints so the frontend /inbox
			// page has a flat-list surface that doesn't share the
			// project kanban query.
			r.Route("/inbox", func(r chi.Router) {
				r.Get("/tasks", listInboxTasksHandler(deps))
				r.Post("/tasks", createInboxTaskHandler(deps))
			})

			// Phase 19: review queue — tasks awaiting human action.
			// One screen with everything waiting on the owner's verdict;
			// actions are POST /api/v1/tasks/:id/review (Phase 3).
			r.Route("/review-queue", func(r chi.Router) {
				r.Get("/", listReviewQueueHandler(deps))
				r.Get("/count", reviewQueueCountHandler(deps))
			})

			// Phase 20: Today screen — single round-trip with overdue,
			// due-today, scheduled-today, awaiting count, active timer.
			r.Get("/today", getTodayHandler(deps))

			// Phase 18: courses (LMS). User side — full CRUD, approve,
			// request-changes, complete lesson. The agent side is
			// mounted under RequireAgent further below.
			r.Route("/courses", func(r chi.Router) {
				r.Get("/", listCoursesHandler(deps))
				r.Post("/", createCourseHandler(deps))
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", getCourseHandler(deps))
					r.Delete("/", deleteCourseHandler(deps))
					r.Post("/approve", approveCourseHandler(deps))
					r.Post("/request-changes", requestChangesCourseHandler(deps))
					// Phase 27.6: owner-side curriculum swap.
					// Same atomic swap the tutor uses; the service
					// retires the generator task when present so
					// a sleeping tutor cannot overwrite manual work.
					r.Put("/curriculum", submitCurriculumHandlerUser(deps))
				})
			})
			r.Post("/lessons/{id}/complete", completeLessonHandler(deps))
			// Phase 27.6: user-side quiz CRUD + lesson content edits.
			r.Post("/lessons/{id}/quizzes", addQuizHandler(deps))
			r.Put("/lessons/{id}/content", updateLessonContentHandlerUser(deps))
			// Phase 27.4: quiz answer (user-side).
			r.Post("/lessons/{id}/quizzes/{qid}/answer", answerQuizHandler(deps))

			r.Get("/reports/time", reportTimeHandler(deps))

			// Phase 5: wiki + FTS5 search.
			r.Route("/pages", func(r chi.Router) {
				r.Get("/", listPagesHandler(deps))
				r.Post("/", savePageHandler(deps))
				r.Route("/{slug}", func(r chi.Router) {
					r.Get("/", getPageHandler(deps))
					r.Put("/", savePageHandler(deps))
					r.Delete("/", deletePageHandler(deps))
					r.Patch("/move", movePageHandler(deps))
					r.Get("/backlinks", getPageBacklinksHandler(deps))
				})
			})

			r.Get("/search", searchHandler(deps))

			// Phase 6: notifications inbox.
			r.Get("/notifications", listNotificationsHandler(deps))
			r.Post("/notifications/{id}/read", markNotificationReadHandler(deps))

			// Phase 10: bot subscriptions.
			r.Get("/notifications/subscriptions", listSubscriptionsHandler(deps))
			r.Post("/notifications/subscriptions", createSubscriptionHandler(deps))
			r.Delete("/notifications/subscriptions/{id}", deleteSubscriptionHandler(deps))
			// Phase 22.3 follow-up: Telegram bind handshake.
			r.Route("/bots/telegram", func(r chi.Router) {
				r.Post("/bind", telegramBindHandler(deps))
			})

			// Phase 8: offline sync.
			r.Post("/sync", syncHandler(deps))

			// Phase 7: backups.
			r.Route("/backups", func(r chi.Router) {
				r.Get("/settings", listBackupSettingsHandler(deps))
				r.Put("/settings", putBackupSettingsHandler(deps))
				r.Post("/test", testBackupPushHandler(deps))
				r.Post("/snapshot", backupSnapshotHandler(deps))
				r.Get("/snapshots", listBackupSnapshotsHandler(deps))
				r.Post("/restore", restoreBackupHandler(deps))
				r.Get("/log", listBackupLogHandler(deps))
			})

			// Phase 22.3: maintenance mode toggle. The maintenance
			// middleware special-cases these paths so the operator
			// can always flip the flag (even from maintenance).
			r.Post("/maintenance/on", maintenanceToggleHandler("on"))
			r.Post("/maintenance/off", maintenanceToggleHandler("off"))
			r.Get("/maintenance", func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, http.StatusOK, map[string]any{
					"maintenance": IsMaintenanceOn(),
				})
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
					// Phase 15: list tasks the agent could act on.
					// Supports ?ready=true to filter out blocked or
					// already-claimed tasks — the agent's "inbox".
					r.Get("/", listAgentTasksHandler(deps))
					r.Route("/{id}", func(r chi.Router) {
						r.Post("/claim", agentClaimTaskHandler(deps))
						r.Post("/release", agentReleaseTaskHandler(deps))
						r.Post("/submit", agentSubmitTaskHandler(deps))
						r.Get("/context", agentTaskContextHandler(deps))
						// Phase 27.11: agent-author comments. Pre-27.11
						// the agent CLI posted to the user-cookie
						// route `/api/v1/tasks/{id}/comments` which
						// 401'd under RequireUser. Mirrors the user
						// handler but writes AuthorAgent + uses
						// Identity.AgentID as the author id.
						r.Post("/comments", agentCreateTaskCommentHandler(deps))
					})
				})
				// Phase 27.11: agent-namespace long-poll. The user-side
				// /events/await is gated by RequireUser (cookie/JWT),
				// so the agent CLI got 401 there. Mounted here under
				// RequireAgent so a bearer token resolves through to
				// the WS hub and the agent id is the filter key.
				r.Post("/agent/events/await", agentAwaitHandler(deps))
				// Phase 18: courses for the tutor agent.
				r.Route("/agent/courses", func(r chi.Router) {
					r.Get("/", listCoursesHandlerAgent(deps))
					r.Put("/{id}/curriculum", submitCurriculumHandlerAgent(deps))
				})
				// Phase 27.4: lesson materialization. The tutor writes
				// the lesson body and links an exercise task; the
				// lesson flips from locked → open.
				r.Route("/agent/lessons", func(r chi.Router) {
					r.Post("/{id}/materialize", materializeLessonHandlerAgent(deps))
					r.Put("/{id}/content", materializeLessonHandlerAgent(deps))
					// Phase 27.6: closes the Phase 18.6 debt —
					// tutors can add a single quiz to an existing
					// lesson without re-submitting the whole tree.
					r.Post("/{id}/quizzes", addQuizHandlerAgent(deps))
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
