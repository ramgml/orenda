package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/user"
	eventservice "github.com/ramgml/orenda/internal/service/event"
	wikiservice "github.com/ramgml/orenda/internal/service/wiki"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// p8PostSync posts a batch of ops through the cookie-authenticated router.
func p8PostSync(t *testing.T, router http.Handler, cookie string, ops []map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"ops": ops})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

type syncResultRow struct {
	ClientID string `json:"client_id"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	ID       string `json:"id,omitempty"`
}

func parseSyncResults(t *testing.T, rr *httptest.ResponseRecorder) []syncResultRow {
	t.Helper()
	var resp struct {
		Results []syncResultRow `json:"results"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	return resp.Results
}

func TestP8_Sync_CreateTask_Idempotent(t *testing.T) {
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)
	projID, colID := p3SeedProject(t, router, cookie, "P8")

	op := map[string]any{
		"op":         "create_task",
		"target":     projID,
		"payload":    map[string]any{"title": "offline task", "column_id": colID},
		"client_id":  "c-test-1",
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	rr := p8PostSync(t, router, cookie, []map[string]any{op})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	results := parseSyncResults(t, rr)
	require.Len(t, results, 1)
	assert.True(t, results[0].OK)
	assert.NotEmpty(t, results[0].ID)

	// Replay same client_id: no-op returning the same server id.
	rr2 := p8PostSync(t, router, cookie, []map[string]any{op})
	require.Equal(t, http.StatusOK, rr2.Code)
	results2 := parseSyncResults(t, rr2)
	require.Len(t, results2, 1)
	assert.True(t, results2[0].OK)
	assert.Equal(t, results[0].ID, results2[0].ID, "idempotent replay returns same id")
}

func TestP8_Sync_MoveTask(t *testing.T) {
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)
	projID, colID := p3SeedProject(t, router, cookie, "P8m")
	taskID := p3SeedTask(t, router, cookie, projID, colID, "x")

	op := map[string]any{
		"op":         "move_task",
		"target":     taskID,
		"payload":    map[string]any{"column_id": colID},
		"client_id":  "c-move-1",
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	rr := p8PostSync(t, router, cookie, []map[string]any{op})
	require.Equal(t, http.StatusOK, rr.Code)
	results := parseSyncResults(t, rr)
	require.Len(t, results, 1)
	assert.True(t, results[0].OK, "err=%s", results[0].Error)
}

func TestP8_Sync_UnsupportedOp(t *testing.T) {
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)

	op := map[string]any{
		"op":         "explode",
		"target":     "x",
		"payload":    map[string]any{},
		"client_id":  "c-x",
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	rr := p8PostSync(t, router, cookie, []map[string]any{op})
	require.Equal(t, http.StatusOK, rr.Code)
	results := parseSyncResults(t, rr)
	require.Len(t, results, 1)
	assert.False(t, results[0].OK)
	assert.Equal(t, "unsupported_op", results[0].Error)
}

func TestP8_Sync_EmptyBatch(t *testing.T) {
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)
	rr := p8PostSync(t, router, cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code)
}

// syncWithEWRouter wires a router with EventService + WikiService so we
// can exercise create_event / create_page through /sync. Returns the
// router and the auth cookie.
func syncWithEWRouter(t *testing.T) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir+"/ew.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email: "ew@x.com", PasswordHash: mustHashFast(t), DisplayName: "EW",
	}))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	eventSvc := eventservice.New(sqlite.NewTaskRepository(db), hub, nil)
	deps := api.Dependencies{
		Logger:       zap.NewNop(),
		Signer:       signer,
		Users:        users,
		Projects:     sqlite.NewProjectRepository(db),
		Tasks:        sqlite.NewTaskRepository(db),
		Tokens:       sqlite.NewAPITokenRepository(db),
		Agents:       sqlite.NewAgentRepository(db),
		Activities:   sqlite.NewActivityRepository(db),
		SyncOps:      sqlite.NewSyncOpsRepository(db),
		WSHub:        hub,
		CookieName:   "orenda_session",
		EventService: eventSvc,
		WikiService:  wikiservice.New(sqlite.NewWikiRepository(db), hub),
	}
	router := api.NewRouter(&deps)

	body, _ := json.Marshal(map[string]string{"email": "ew@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value
	return router, cookie
}

func TestP8_Sync_CreateEvent(t *testing.T) {
	router, cookie := syncWithEWRouter(t)
	now := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	soon := time.Now().UTC().Add(30 * time.Minute).Truncate(time.Second).Format(time.RFC3339)

	op := map[string]any{
		"op":     "create_event",
		"target": "",
		"payload": map[string]any{
			"title":    "offline event",
			"start_at": now,
			"end_at":   soon,
		},
		"client_id":  "c-evt-1",
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	rr := p8PostSync(t, router, cookie, []map[string]any{op})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	results := parseSyncResults(t, rr)
	require.Len(t, results, 1)
	assert.True(t, results[0].OK, "err=%s", results[0].Error)
	assert.NotEmpty(t, results[0].ID)
}

func TestP8_Sync_CreatePage(t *testing.T) {
	router, cookie := syncWithEWRouter(t)
	op := map[string]any{
		"op":     "create_page",
		"target": "",
		"payload": map[string]any{
			"slug":       "offline-page",
			"title":      "Offline Page",
			"content_md": "# Drafted while offline",
		},
		"client_id":  "c-pg-1",
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	rr := p8PostSync(t, router, cookie, []map[string]any{op})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	results := parseSyncResults(t, rr)
	require.Len(t, results, 1)
	assert.True(t, results[0].OK, "err=%s", results[0].Error)
	assert.NotEmpty(t, results[0].ID)
}

// Replay a create_page with the same client_id: idempotent.
func TestP8_Sync_CreatePage_Idempotent(t *testing.T) {
	router, cookie := syncWithEWRouter(t)
	op := map[string]any{
		"op":         "create_page",
		"target":     "",
		"payload":    map[string]any{"slug": "idem", "title": "Idem", "content_md": "x"},
		"client_id":  "c-pg-idem",
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	rr1 := p8PostSync(t, router, cookie, []map[string]any{op})
	require.Equal(t, http.StatusOK, rr1.Code)
	r1 := parseSyncResults(t, rr1)
	require.True(t, r1[0].OK)

	rr2 := p8PostSync(t, router, cookie, []map[string]any{op})
	require.Equal(t, http.StatusOK, rr2.Code)
	r2 := parseSyncResults(t, rr2)
	require.True(t, r2[0].OK)
	assert.Equal(t, r1[0].ID, r2[0].ID)
}
