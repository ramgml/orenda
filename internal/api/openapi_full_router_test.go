// Phase 27.11: full-router route-coverage fixture.
//
// The previous TestOpenAPI_RouteCoverage used the columnDeps
// fixture, which only mounts a subset of routes (no agent
// namespace, no courses, no inbox, no wiki, no backups). Routes
// that are gated by `if deps.Agents != nil` and similar were
// silently skipped, leaving OpenAPI coverage blind to the agent
// namespace and the courses / inbox / backups endpoints that
// run there.
//
// This file builds a router that's as close to production as
// possible: every repository is wired through a real SQLite DB,
// every service is constructed with sensible stubs, and the WS
// hub is real (otherwise the `if deps.Signer != nil && deps.WSHub
// != nil` branch in NewRouter skips the WS route). The router
// then walks every route via chi.Walk and asserts each appears
// in docs/openapi.yaml.
//
// Services that depend on file system paths (backup, attachment)
// are left nil — the corresponding routes mount anyway via
// NewRouter's unconditional group registration; the
// handler-level tests (test files for each service) cover
// actual behaviour.

package api_test

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/bot"
	"github.com/ramgml/orenda/internal/domain/user"
	commentservice "github.com/ramgml/orenda/internal/service/comment"
	courseservice "github.com/ramgml/orenda/internal/service/course"
	eventservice "github.com/ramgml/orenda/internal/service/event"
	notifierservice "github.com/ramgml/orenda/internal/service/notifier"
	searchservice "github.com/ramgml/orenda/internal/service/search"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	timeentryservice "github.com/ramgml/orenda/internal/service/timeentry"
	wikiservice "github.com/ramgml/orenda/internal/service/wiki"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// fullRouterDeps builds a router with every dependency wired so
// that all gated routes mount: agents (the Phase 27.11.1
// additions), courses, inbox, wiki, search, time entries, etc.
//
// The fixture is heavy: it spins up a temp SQLite DB, every
// repository, every service. The test that uses it
// (TestOpenAPI_RouteCoverage_FullRouter) only walks the route
// tree, so the cost is bounded — we don't drive any of these
// handlers, we just need them mountable.
func fullRouterDeps(t *testing.T) http.Handler {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir+"/full.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	users := sqlite.NewUserRepository(db)
	projects := sqlite.NewProjectRepository(db)
	tasks := sqlite.NewTaskRepository(db)
	tokens := sqlite.NewAPITokenRepository(db)
	agents := sqlite.NewAgentRepository(db)
	activities := sqlite.NewActivityRepository(db)
	commentRepo := sqlite.NewCommentRepository(db)
	wikiRepo := sqlite.NewWikiRepository(db)
	searchRepo := sqlite.NewSearchRepository(db)
	timeRepo := sqlite.NewTimeEntryRepository(db)
	coursesRepo := sqlite.NewCourseRepository(db)

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email:        "full-router@x.com",
		PasswordHash: "x",
		DisplayName:  "Full",
	}))

	commentSvc := commentservice.New(commentRepo, hub, nil)
	taskSvc := taskservice.New(tasks, sqlite.NewTaskLockRepository(db), nil, nil, hub)
	eventSvc := eventservice.New(tasks, hub, nil)
	timeSvc := timeentryservice.New(timeRepo, hub, nil)
	wikiSvc := wikiservice.New(wikiRepo, hub)
	searchSvc := searchservice.New(searchRepo, hub)
	courseSvc := courseservice.New(coursesRepo)

	// Bots: a console bot is enough to mount the registry. The
	// notifier constructor takes a registry, not the bots map.
	botRegistry := bot.NewRegistry()
	botRegistry.Register(bot.Console{})

	notifierSvc := notifierservice.New(nil, nil, botRegistry, hub)

	deps := api.Dependencies{
		Logger:        zap.NewNop(),
		Signer:        signer,
		Users:         users,
		Projects:      projects,
		Tasks:         tasks,
		Tokens:        tokens,
		TaskService:   taskSvc,
		Agents:        agents,
		Comments:      commentSvc,
		Activities:    activities,
		EventService:  eventSvc,
		TimeService:   timeSvc,
		WikiService:   wikiSvc,
		SearchService: searchSvc,
		Notifier:      notifierSvc,
		WSHub:         hub,
		CookieName:    "orenda_session",
		Courses:       coursesRepo,
		CourseService: courseSvc,
		// Phase 28.1 polish.1: backup settings repo is a pure
		// SQLite repo (no filesystem), so it's wired even in tests.
		BackupSettings: sqlite.NewBackupSettingsRepository(db),
		// Attachments and Backup deliberately nil — they require
		// filesystem paths (config + uploads dir) and aren't needed
		// for route mount. Their routes mount unconditionally.
	}
	return api.NewRouter(deps)
}

// TestOpenAPI_RouteCoverage_FullRouter (Phase 27.11) walks the
// router built from fullRouterDeps — every dependency wired so all
// gated namespaces mount — and asserts every (method, path) lands
// in docs/openapi.yaml. The previous TestOpenAPI_RouteCoverage used
// a partial fixture (columnDeps) and missed the agent namespace,
// the courses namespace, and the inbox endpoint; this test is
// the production-shape coverage run.
func TestOpenAPI_RouteCoverage_FullRouter(t *testing.T) {
	spec := readOpenAPISpec(t)
	require.NotEmpty(t, spec, "openapi.yaml must be readable")

	router := fullRouterDeps(t)
	routes := walkRoutes(router)
	require.NotEmpty(t, routes, "full router must declare routes")

	missing := 0
	for _, r := range routes {
		// Path with trailing slash (chi normalizes both — accept either).
		if !pathInSpec(spec, r.method, r.path) && !pathInSpec(spec, r.method, strings.TrimSuffix(r.path, "/")) {
			t.Errorf("route %s %s is missing from docs/openapi.yaml", r.method, r.path)
			missing++
		}
	}
	if missing > 0 {
		t.Fatalf("openapi.yaml is missing %d routes from the full router", missing)
	}
}
