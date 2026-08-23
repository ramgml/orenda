package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// columnDeps wires a router with a user + project + columns. Returns the
// router, the auth cookie, the board, the columns and the project + task
// repos (so tests can seed rows for the WIP-too-small check).
type colFixtures struct {
	router    http.Handler
	cookie    string
	projectID string
	cols      []*project.Column
	tasks     interface {
		Create(ctx context.Context, t *task.Task) error
	}
	// taskRepo gives direct repo access (Phase 15 dependency tests
	// need to call Blockers/AddDependency that aren't on the narrow
	// interface above). nil when not wired — callers should prefer
	// the narrow `tasks` for seeding.
	taskRepo task.Repository
}

func columnDeps(t *testing.T) colFixtures {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "col.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	users := sqlite.NewUserRepository(db)
	u := &user.User{Email: "col@x.com", PasswordHash: mustHashFast(t), DisplayName: "C"}
	require.NoError(t, users.Create(context.Background(), u))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})
	repo := sqlite.NewTaskRepository(db)
	projRepo := sqlite.NewProjectRepository(db)
	taskSvc := taskservice.New(repo, sqlite.NewTaskLockRepository(db), nil, nil, hub)

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	deps := api.Dependencies{
		Logger:      zap.NewNop(),
		Signer:      signer,
		Users:       users,
		Projects:    projRepo,
		Tasks:       repo,
		Tokens:      sqlite.NewAPITokenRepository(db),
		TaskService: taskSvc,
		WSHub:       hub,
		CookieName:  "orenda_session",
	}
	router := api.NewRouter(&deps)
	t.Cleanup(deps.RateLimitClose)

	body, _ := json.Marshal(map[string]string{"email": "col@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value

	p, _, _, err := projRepo.CreateProject(context.Background(), &project.Project{
		Name: "Demo", OwnerID: u.ID, Color: "#3b82f6",
	})
	require.NoError(t, err)
	_, cols, err := projRepo.GetBoard(context.Background(), p.ID)
	require.NoError(t, err)
	return colFixtures{
		router:    router,
		cookie:    cookie,
		projectID: p.ID,
		cols:      cols,
		tasks:     repo,
		taskRepo:  repo,
	}
}

func patchColumn(router http.Handler, cookie, colID string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/columns/"+colID, bytes.NewReader(raw))
	req.Header.Set("Cookie", "orenda_session="+cookie)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// 1) Rename + recolor succeeds and persists.
func TestPatchColumn_RenameAndRecolor(t *testing.T) {
	f := columnDeps(t)
	rr := patchColumn(f.router, f.cookie, f.cols[1].ID, map[string]any{
		"name":  "TODO renamed",
		"color": "#ff0000",
	})
	require.Equal(t, http.StatusOK, rr.Code)
	var out project.Column
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&out))
	assert.Equal(t, "TODO renamed", out.Name)
	assert.Equal(t, "#ff0000", out.Color)
}

// 2) Setting wip_limit=0 clears the limit.
func TestPatchColumn_WIPLimitZeroClears(t *testing.T) {
	f := columnDeps(t)
	zero := 0
	rr := patchColumn(f.router, f.cookie, f.cols[1].ID, map[string]any{"wip_limit": &zero})
	require.Equal(t, http.StatusOK, rr.Code)
	var out project.Column
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&out))
	assert.Nil(t, out.WIPLimit, "wip_limit should be cleared")
}

// 3) Setting wip_limit=3 succeeds with empty column.
func TestPatchColumn_WIPLimitSet(t *testing.T) {
	f := columnDeps(t)
	three := 3
	rr := patchColumn(f.router, f.cookie, f.cols[1].ID, map[string]any{"wip_limit": &three})
	require.Equal(t, http.StatusOK, rr.Code)
	var out project.Column
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&out))
	require.NotNil(t, out.WIPLimit)
	assert.Equal(t, 3, *out.WIPLimit)
}

// 4) Setting wip_limit=2 when there are already 3 tasks → 422.
func TestPatchColumn_WIPLimitTooSmall(t *testing.T) {
	f := columnDeps(t)
	for i := 0; i < 3; i++ {
		require.NoError(t, f.tasks.Create(context.Background(), &task.Task{
			ProjectID: f.projectID,
			ColumnID:  f.cols[1].ID,
			Title:     "x",
		}))
	}
	two := 2
	rr := patchColumn(f.router, f.cookie, f.cols[1].ID, map[string]any{"wip_limit": &two})
	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code)
	body := rr.Body.String()
	assert.True(t, strings.Contains(body, "wip_limit_too_small"),
		"body should contain wip_limit_too_small: %s", body)
}

// 5) Unknown id → 404.
func TestPatchColumn_NotFound(t *testing.T) {
	f := columnDeps(t)
	rr := patchColumn(f.router, f.cookie, "deadbeef", map[string]any{"name": "x"})
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// 6) Empty name is rejected (handler ignores it, name stays non-empty).
func TestPatchColumn_EmptyNameRejected(t *testing.T) {
	f := columnDeps(t)
	rr := patchColumn(f.router, f.cookie, f.cols[1].ID, map[string]any{"name": ""})
	assert.Equal(t, http.StatusOK, rr.Code, "empty name should be ignored, not rejected")
	var out project.Column
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&out))
	assert.NotEqual(t, "", out.Name)
}

// 7) Negative wip_limit → 400.
func TestPatchColumn_NegativeWIPRejected(t *testing.T) {
	f := columnDeps(t)
	neg := -1
	rr := patchColumn(f.router, f.cookie, f.cols[1].ID, map[string]any{"wip_limit": &neg})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// 8) Phase 27.10: PATCH broadcasts column.updated on the "tasks" topic
// so a second tab refetches the board and renders the new colour
// without a manual reload. Pre-27.10 the broadcast was missing —
// rename/refetch worked, colour/rename-in-place did not propagate.
//
// We build the router by hand here (rather than reusing columnDeps)
// because the fixtures don't expose the hub — we need to subscribe
// to it before the PATCH to assert the broadcast lands.
func TestPatchColumn_BroadcastsColumnUpdated(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir+"/col2.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	users := sqlite.NewUserRepository(db)
	u := &user.User{Email: "col2@x.com", PasswordHash: mustHashFast(t), DisplayName: "C2"}
	require.NoError(t, users.Create(context.Background(), u))

	hub := ws.NewHub()
	t.Cleanup(func() { hub.Close() })
	repo := sqlite.NewTaskRepository(db)
	projRepo := sqlite.NewProjectRepository(db)
	taskSvc := taskservice.New(repo, sqlite.NewTaskLockRepository(db), nil, nil, hub)

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	deps2 := api.Dependencies{
		Logger:      zap.NewNop(),
		Signer:      signer,
		Users:       users,
		Projects:    projRepo,
		Tasks:       repo,
		Tokens:      sqlite.NewAPITokenRepository(db),
		TaskService: taskSvc,
		WSHub:       hub,
		CookieName:  "orenda_session",
	}
	router := api.NewRouter(&deps2)
	t.Cleanup(deps2.RateLimitClose)

	body, _ := json.Marshal(map[string]string{"email": "col2@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value

	p, _, _, err := projRepo.CreateProject(context.Background(), &project.Project{
		Name: "Demo2", OwnerID: u.ID, Color: "#3b82f6",
	})
	require.NoError(t, err)
	_, cols, err := projRepo.GetBoard(context.Background(), p.ID)
	require.NoError(t, err)

	// Subscribe before the PATCH — the fan-out goroutine runs
	// during the handler call.
	events, unsub := hub.Subscribe(u.ID, "tasks")
	defer unsub()

	// Patch the column: rename + recolor.
	raw, _ := json.Marshal(map[string]any{"name": "Doing", "color": "#22c55e"})
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/v1/columns/"+cols[1].ID, bytes.NewReader(raw))
	patchReq.Header.Set("Cookie", "orenda_session="+cookie)
	patchReq.Header.Set("Content-Type", "application/json")
	patchRR := httptest.NewRecorder()
	router.ServeHTTP(patchRR, patchReq)
	require.Equal(t, http.StatusOK, patchRR.Code)

	// The WS event must arrive within a short timeout (the hub is
	// in-process, no race).
	select {
	case ev := <-events:
		body, ok := ev.Body.(map[string]any)
		require.True(t, ok, "WS event body should be a map")
		assert.Equal(t, "column.updated", body["type"], "expected column.updated event, got %#v", body["type"])
		col, ok := body["column"].(*project.Column)
		require.True(t, ok, "column payload should be *project.Column")
		assert.Equal(t, "Doing", col.Name)
		assert.Equal(t, "#22c55e", col.Color)
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive column.updated WS event within 2s")
	}
}
