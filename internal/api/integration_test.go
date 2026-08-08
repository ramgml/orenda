package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// integrationDeps wires the full Dependencies struct against a fresh in-memory
// SQLite database, applies migrations, and returns a router + a *sql.DB handle
// for cleanup.
func integrationDeps(t *testing.T) (api.Dependencies, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "orenda.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email:        "owner@x.com",
		PasswordHash: mustHash(t, "hunter2"),
		DisplayName:  "Owner",
	}))

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	return api.Dependencies{
		Logger:     zap.NewNop(),
		Signer:     signer,
		Users:      users,
		Projects:   sqlite.NewProjectRepository(db),
		Tasks:      sqlite.NewTaskRepository(db),
		Tokens:     sqlite.NewAPITokenRepository(db),
		CookieName: "orenda_session",
	}, db
}

func mustHash(t *testing.T, plain string) string {
	t.Helper()
	h, err := auth.HashPassword(plain, 4)
	require.NoError(t, err)
	return h
}

// loginAndCookie posts /auth/login and returns the Set-Cookie value.
func loginAndCookie(t *testing.T, router http.Handler, email, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "login failed: %s", rr.Body.String())

	cookies := rr.Result().Cookies()
	require.NotEmpty(t, cookies, "no Set-Cookie in login response")
	return cookies[0].Value
}

// authedGet sends a GET with the cookie.
func authedGet(t *testing.T, router http.Handler, path, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// authedJSON sends a JSON request with the cookie.
func authedJSON(t *testing.T, router http.Handler, method, path, cookie string, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

func TestIntegration_Login_Me_Project_Task(t *testing.T) {
	deps, _ := integrationDeps(t)
	router := api.NewRouter(deps)

	// Login.
	cookie := loginAndCookie(t, router, "owner@x.com", "hunter2")
	assert.NotEmpty(t, cookie)

	// /me.
	rr := authedGet(t, router, "/api/v1/me", cookie)
	require.Equal(t, http.StatusOK, rr.Code)
	var me struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &me))
	assert.Equal(t, "owner@x.com", me.Email)

	// Create a project.
	rr = authedJSON(t, router, http.MethodPost, "/api/v1/projects", cookie, map[string]any{
		"name":  "Orenda",
		"color": "#3b82f6",
	})
	require.Equal(t, http.StatusCreated, rr.Code, "create project: %s", rr.Body.String())
	var p project.Project
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &p))
	assert.NotEmpty(t, p.ID)

	// Get board (auto-created).
	rr = authedGet(t, router, fmt.Sprintf("/api/v1/projects/%s/board", p.ID), cookie)
	require.Equal(t, http.StatusOK, rr.Code)
	var board struct {
		Board   *project.Board    `json:"board"`
		Columns []*project.Column `json:"columns"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &board))
	require.Len(t, board.Columns, 5)

	// Create a task in the first column.
	rr = authedJSON(t, router, http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%s/tasks", p.ID), cookie,
		map[string]any{
			"title":     "Implement login",
			"column_id": board.Columns[0].ID,
		})
	require.Equal(t, http.StatusCreated, rr.Code, "create task: %s", rr.Body.String())
	var tr task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &tr))
	assert.NotEmpty(t, tr.ID)
	assert.Equal(t, "Implement login", tr.Title)

	// List tasks.
	rr = authedGet(t, router, fmt.Sprintf("/api/v1/projects/%s/tasks", p.ID), cookie)
	require.Equal(t, http.StatusOK, rr.Code)
	var list struct {
		Tasks []*task.Task `json:"tasks"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &list))
	assert.Len(t, list.Tasks, 1)

	// PATCH the task (rename).
	rr = authedJSON(t, router, http.MethodPatch,
		fmt.Sprintf("/api/v1/tasks/%s", tr.ID), cookie,
		map[string]any{"title": "Renamed"})
	require.Equal(t, http.StatusOK, rr.Code, "patch task: %s", rr.Body.String())

	// PUT (alias) also works.
	rr = authedJSON(t, router, http.MethodPut,
		fmt.Sprintf("/api/v1/tasks/%s", tr.ID), cookie,
		map[string]any{"priority": "high"})
	require.Equal(t, http.StatusOK, rr.Code, "put task: %s", rr.Body.String())

	// Delete the task.
	rr = authedJSON(t, router, http.MethodDelete,
		fmt.Sprintf("/api/v1/tasks/%s", tr.ID), cookie, nil)
	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestIntegration_BadLogin(t *testing.T) {
	deps, _ := integrationDeps(t)
	router := api.NewRouter(deps)

	body, _ := json.Marshal(map[string]string{"email": "owner@x.com", "password": "wrong"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestIntegration_RequiresAuth(t *testing.T) {
	deps, _ := integrationDeps(t)
	router := api.NewRouter(deps)

	// No cookie.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	// Bad cookie.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: "garbage"})
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestIntegration_Logout(t *testing.T) {
	deps, _ := integrationDeps(t)
	router := api.NewRouter(deps)
	cookie := loginAndCookie(t, router, "owner@x.com", "hunter2")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	// Cookie should be cleared (MaxAge < 0).
	var found bool
	for _, c := range rr.Result().Cookies() {
		if c.Name == "orenda_session" {
			found = true
			assert.Equal(t, -1, c.MaxAge, "logout should set MaxAge=-1")
		}
	}
	assert.True(t, found, "logout must set the session cookie with MaxAge=-1")
}
