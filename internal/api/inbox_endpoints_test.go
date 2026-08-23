package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
)

// Phase 16.5: /api/v1/inbox/tasks — list + create inbox tasks.
func TestInbox_ListAndCreate(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)

	// Empty inbox.
	rr := doReq(f.router, http.MethodGet, "/api/v1/inbox/tasks", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var list struct {
		Tasks []task.Task `json:"tasks"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&list))
	assert.Empty(t, list.Tasks, "fresh inbox is empty")

	// Create one.
	rr = doReq(f.router, http.MethodPost, "/api/v1/inbox/tasks", f.cookie, map[string]any{
		"title": "first idea",
	})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var created task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&created))
	assert.Equal(t, "first idea", created.Title)
	assert.Equal(t, "", created.ProjectID, "inbox task has empty project_id")
	assert.Equal(t, "", created.ColumnID, "inbox task has no column")

	// List again — one entry.
	rr = doReq(f.router, http.MethodGet, "/api/v1/inbox/tasks", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&list))
	assert.Len(t, list.Tasks, 1)
	assert.Equal(t, "first idea", list.Tasks[0].Title)
}

// Phase 16.5: inbox endpoint rejects project_id / column_id in body.
// The inbox endpoint is exclusively for unfiled capture — clients
// that want a project must use POST /api/v1/projects/{id}/tasks.
func TestInbox_Create_RejectsProjectBody(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)

	pid := f.projectID
	rr := doReq(f.router, http.MethodPost, "/api/v1/inbox/tasks", f.cookie, map[string]any{
		"title":      "should fail",
		"project_id": pid,
		"column_id":  "col-x",
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "inbox_endpoint_does_not_accept_project_id")
}

// Phase 16.5: inbox endpoint requires title.
func TestInbox_Create_RequiresTitle(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	rr := doReq(f.router, http.MethodPost, "/api/v1/inbox/tasks", f.cookie,
		map[string]any{"title": ""})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "missing_title")
}

// Phase 16.5: auth required.
func TestInbox_RequiresAuth(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	for _, path := range []string{"/api/v1/inbox/tasks", "/api/v1/inbox/tasks"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		f.router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code, path)
	}
}

// Phase 16.5: PATCH a task to file it under a project (inbox → project).
// We create a real project via the project repo (not the test fixture)
// so we can verify the PATCH path end-to-end.
func TestInbox_FileToProject(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)

	// Create an inbox task.
	rr := doReq(f.router, http.MethodPost, "/api/v1/inbox/tasks", f.cookie,
		map[string]any{"title": "fileable"})
	require.Equal(t, http.StatusCreated, rr.Code)
	var t0 task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&t0))
	assert.Equal(t, "", t0.ProjectID)

	// File it under the test project, column 0.
	col0 := f.cols[0].ID
	rr = doReq(f.router, http.MethodPatch, "/api/v1/tasks/"+t0.ID, f.cookie, map[string]any{
		"project_id": f.projectID,
		// column_id is omitted on purpose — applyTaskPatch should
		// auto-assign the first column of the new project.
	})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var t1 task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&t1))
	assert.Equal(t, f.projectID, t1.ProjectID)
	assert.Equal(t, col0, t1.ColumnID, "missing column_id should resolve to first column of new project")

	// Inbox list is now empty.
	rr = doReq(f.router, http.MethodGet, "/api/v1/inbox/tasks", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var list struct {
		Tasks []task.Task `json:"tasks"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&list))
	assert.Empty(t, list.Tasks)
}

// Phase 16.5: invalid JSON returns 400 (catch-all for handlers).
func TestInbox_Create_InvalidJSON(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/inbox/tasks",
		bytes.NewReader([]byte("{not-json")))
	req.Header.Set("Cookie", "orenda_session="+f.cookie)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	// sanity: error envelope contains the expected key
	assert.True(t, strings.Contains(rr.Body.String(), "invalid_json"))
}

// Phase 30.10: quick capture carries an optional due date. The inbox
// endpoint must persist it (previously due_at was silently dropped —
// the field existed only in the project create path).
func TestInbox_Create_AcceptsDueAt(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)

	rr := doReq(f.router, http.MethodPost, "/api/v1/inbox/tasks", f.cookie, map[string]any{
		"title":  "with a deadline",
		"due_at": "2026-09-01T00:00:00Z",
	})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var created task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&created))
	require.NotNil(t, created.DueAt, "due_at must round-trip through create")
	assert.Equal(t, 2026, created.DueAt.Year())
	assert.Equal(t, time.September, created.DueAt.Month())
	assert.Equal(t, 1, created.DueAt.Day())

	// And it persists: the list endpoint returns the same value.
	rr = doReq(f.router, http.MethodGet, "/api/v1/inbox/tasks", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var list struct {
		Tasks []task.Task `json:"tasks"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&list))
	require.Len(t, list.Tasks, 1)
	require.NotNil(t, list.Tasks[0].DueAt, "due_at must survive a list round-trip")
	assert.Equal(t, 1, list.Tasks[0].DueAt.Day())

	// Omitting due_at stays legal — quick capture without a date.
	rr = doReq(f.router, http.MethodPost, "/api/v1/inbox/tasks", f.cookie, map[string]any{
		"title": "no deadline",
	})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var plain task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&plain))
	assert.Nil(t, plain.DueAt)
}
