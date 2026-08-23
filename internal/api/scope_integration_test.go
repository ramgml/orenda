package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/user"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// fullDeps wires the full Dependencies for scope integration tests.
func fullDeps(t *testing.T) (api.Dependencies, string) {
	t.Helper()
	db, _ := copyTemplateDB(t)

	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email:        "scope@x.com",
		PasswordHash: mustHashFast(t),
		DisplayName:  "Scope Tester",
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
	}, "hunter2!"
}

// mustHashFast hashes the shared test password at cost 4 to keep tests fast.
func mustHashFast(t *testing.T) string {
	t.Helper()
	h, err := auth.HashPassword("hunter2!", 4)
	require.NoError(t, err)
	return h
}

func TestOwnerScopesIncludeAllExpected(t *testing.T) {
	t.Parallel()
	// Just verifies the mapping in scopesForRole indirectly: the /me handler
	// returns the scopes computed for the role, so after login we expect
	// the full set.
	deps, plain := fullDeps(t)
	router := api.NewRouter(&deps)

	body, _ := json.Marshal(map[string]string{"email": "scope@x.com", "password": plain})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req2.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, req2)
	require.Equal(t, http.StatusOK, rr2.Code)

	var me struct {
		Scopes []string `json:"scopes"`
	}
	require.NoError(t, json.Unmarshal(rr2.Body.Bytes(), &me))
	assert.Contains(t, me.Scopes, "tasks:read")
	assert.Contains(t, me.Scopes, "tasks:write")
	assert.Contains(t, me.Scopes, "projects:read")
	assert.Contains(t, me.Scopes, "projects:write")
}

func TestIntegration_ChildTasksCRUD(t *testing.T) {
	t.Parallel()
	deps, _ := fullDeps(t)
	router := api.NewRouter(&deps)

	// Login.
	body, _ := json.Marshal(map[string]string{"email": "scope@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value

	authed := func(method, path string, body any) *httptest.ResponseRecorder {
		buf, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(buf))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}

	// Create a project.
	rr = authed(http.MethodPost, "/api/v1/projects", map[string]any{"name": "Sub"})
	require.Equal(t, http.StatusCreated, rr.Code)
	var p struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &p))

	// Get board → first column.
	rr = authed(http.MethodGet, "/api/v1/projects/"+p.ID+"/board", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var b struct {
		Columns []struct {
			ID string `json:"id"`
		} `json:"columns"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &b))
	colID := b.Columns[0].ID

	// Create a task.
	rr = authed(http.MethodPost, "/api/v1/projects/"+p.ID+"/tasks", map[string]any{
		"title":     "Parent",
		"column_id": colID,
	})
	require.Equal(t, http.StatusCreated, rr.Code)
	var t1 struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &t1))

	// Add child tasks via parent_task_id (Phase 14 — subtasks were
	// promoted to first-class tasks).
	rr = authed(http.MethodPost, "/api/v1/projects/"+p.ID+"/tasks", map[string]any{
		"title":          "first",
		"parent_task_id": t1.ID,
	})
	require.Equal(t, http.StatusCreated, rr.Code)
	rr = authed(http.MethodPost, "/api/v1/projects/"+p.ID+"/tasks", map[string]any{
		"title":          "second",
		"parent_task_id": t1.ID,
	})
	require.Equal(t, http.StatusCreated, rr.Code)

	// List child tasks.
	rr = authed(http.MethodGet, "/api/v1/tasks/"+t1.ID+"/children", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var children struct {
		Tasks []struct {
			Title string `json:"title"`
		} `json:"tasks"`
		Progress struct {
			Total int `json:"total"`
			Done  int `json:"done"`
		} `json:"progress"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &children))
	assert.Len(t, children.Tasks, 2)
	assert.Equal(t, "first", children.Tasks[0].Title)
	assert.Equal(t, 2, children.Progress.Total)
	assert.Equal(t, 0, children.Progress.Done)
}

func TestIntegration_ProjectCRUD(t *testing.T) {
	t.Parallel()
	deps, _ := fullDeps(t)
	router := api.NewRouter(&deps)

	// Login.
	body, _ := json.Marshal(map[string]string{"email": "scope@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value

	authed := func(method, path string, body any) *httptest.ResponseRecorder {
		var buf []byte
		if body != nil {
			buf, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(buf))
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		r.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}

	// Create.
	rr = authed(http.MethodPost, "/api/v1/projects", map[string]any{"name": "X", "color": "#ff0000"})
	require.Equal(t, http.StatusCreated, rr.Code)
	var p struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &p))
	assert.Equal(t, "X", p.Name)
	assert.Equal(t, "#ff0000", p.Color)

	// List. The system-created Inbox project also lives in the table
	// since Phase 11, so we just check our project is there.
	rr = authed(http.MethodGet, "/api/v1/projects", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var list struct {
		Projects []struct {
			ID string `json:"id"`
		} `json:"projects"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &list))
	var seen bool
	for _, pr := range list.Projects {
		if pr.ID == p.ID {
			seen = true
			break
		}
	}
	assert.True(t, seen, "created project not in list")

	// Patch.
	rr = authed(http.MethodPatch, "/api/v1/projects/"+p.ID, map[string]any{
		"name":     "X renamed",
		"archived": true,
	})
	require.Equal(t, http.StatusOK, rr.Code)

	// Get.
	rr = authed(http.MethodGet, "/api/v1/projects/"+p.ID, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var got struct {
		Name     string `json:"name"`
		Archived bool   `json:"archived"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, "X renamed", got.Name)
	assert.True(t, got.Archived)

	// Delete.
	rr = authed(http.MethodDelete, "/api/v1/projects/"+p.ID, nil)
	require.Equal(t, http.StatusNoContent, rr.Code)

	// Get → 404.
	rr = authed(http.MethodGet, "/api/v1/projects/"+p.ID, nil)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestIntegration_NotFoundReturns404(t *testing.T) {
	t.Parallel()
	deps, _ := fullDeps(t)
	router := api.NewRouter(&deps)

	body, _ := json.Marshal(map[string]string{"email": "scope@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value

	r := httptest.NewRequest(http.MethodGet, "/api/v1/projects/no-such-id", nil)
	r.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
