package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
)

// tagFixture is the minimal HTTP setup for tag-related API tests:
// a logged-in owner with a project and one task.
type tagFixture struct {
	router    http.Handler
	cookie    string
	projectID string
	taskID    string
}

func tagDeps(t *testing.T) tagFixture {
	t.Helper()
	f := columnDeps(t)
	// Create one task in the project's first column to attach tags to.
	// columnDeps exposes a narrow tasks interface (Create only), which
	// is exactly what we need here.
	tr := &task.Task{
		ProjectID: f.projectID,
		ColumnID:  f.cols[0].ID,
		Title:     "Tag target",
	}
	require.NoError(t, f.tasks.Create(t.Context(), tr))
	return tagFixture{
		router:    f.router,
		cookie:    f.cookie,
		projectID: f.projectID,
		taskID:    tr.ID,
	}
}

// doReq is a small helper: send an HTTP request with cookie+JSON
// body and return the response.
func doReq(router http.Handler, method, path, cookie string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Cookie", "orenda_session="+cookie)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// 1) Tag CRUD happy path.
func TestTags_CRUD(t *testing.T) {
	t.Parallel()
	f := tagDeps(t)

	// Create.
	rr := doReq(f.router, "POST", "/api/v1/tags", f.cookie, map[string]any{
		"name": "frontend", "color": "#22c55e",
	})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var created task.Tag
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&created))
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, "frontend", created.Name)
	assert.Equal(t, "#22c55e", created.Color)

	// List.
	rr = doReq(f.router, "GET", "/api/v1/tags", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var list struct {
		Tags []task.Tag `json:"tags"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&list))
	require.Len(t, list.Tags, 1)
	assert.Equal(t, "frontend", list.Tags[0].Name)

	// Patch (rename).
	rr = doReq(f.router, "PATCH", "/api/v1/tags/"+created.ID, f.cookie, map[string]any{
		"name": "frontend-2",
	})
	require.Equal(t, http.StatusOK, rr.Code)
	var patched task.Tag
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&patched))
	assert.Equal(t, "frontend-2", patched.Name)

	// Delete.
	rr = doReq(f.router, "DELETE", "/api/v1/tags/"+created.ID, f.cookie, nil)
	require.Equal(t, http.StatusNoContent, rr.Code)

	// List again → empty.
	rr = doReq(f.router, "GET", "/api/v1/tags", f.cookie, nil)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&list))
	assert.Empty(t, list.Tags)
}

// 2) Empty name → 400.
func TestTags_Create_EmptyName(t *testing.T) {
	t.Parallel()
	f := tagDeps(t)
	rr := doReq(f.router, "POST", "/api/v1/tags", f.cookie, map[string]any{
		"name": "",
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.True(t, strings.Contains(rr.Body.String(), "name_required"))
}

// 3) Duplicate name → 409.
func TestTags_Create_DuplicateName(t *testing.T) {
	t.Parallel()
	f := tagDeps(t)
	rr := doReq(f.router, "POST", "/api/v1/tags", f.cookie, map[string]any{"name": "dup"})
	require.Equal(t, http.StatusCreated, rr.Code)
	rr = doReq(f.router, "POST", "/api/v1/tags", f.cookie, map[string]any{"name": "dup"})
	assert.Equal(t, http.StatusConflict, rr.Code)
	assert.True(t, strings.Contains(rr.Body.String(), "tag_exists"))
}

// 4) Set tags on a task.
func TestTaskTags_SetReplace(t *testing.T) {
	t.Parallel()
	f := tagDeps(t)

	// Create three tags, pre-allocate the slice so prealloc stays
	// happy. The number is bounded by the loop below so a constant
	// is the right hint.
	names := []string{"a", "b", "c"}
	ids := make([]string, 0, len(names))
	for _, name := range names {
		rr := doReq(f.router, "POST", "/api/v1/tags", f.cookie, map[string]any{"name": name})
		require.Equal(t, http.StatusCreated, rr.Code)
		var tg task.Tag
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&tg))
		ids = append(ids, tg.ID)
	}

	// Attach [a, b].
	rr := doReq(f.router, "PUT", "/api/v1/tasks/"+f.taskID+"/tags", f.cookie,
		map[string]any{"tag_ids": []string{ids[0], ids[1]}})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	// GET back via the task detail endpoint.
	rr = doReq(f.router, "GET", "/api/v1/tasks/"+f.taskID+"/tags", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var got struct {
		Tags []task.Tag `json:"tags"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	require.Len(t, got.Tags, 2)
	tagNames := []string{got.Tags[0].Name, got.Tags[1].Name}
	assert.ElementsMatch(t, []string{"a", "b"}, tagNames)

	// Replace with [c].
	rr = doReq(f.router, "PUT", "/api/v1/tasks/"+f.taskID+"/tags", f.cookie,
		map[string]any{"tag_ids": []string{ids[2]}})
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	require.Len(t, got.Tags, 1)
	assert.Equal(t, "c", got.Tags[0].Name)

	// Clear with [].
	rr = doReq(f.router, "PUT", "/api/v1/tasks/"+f.taskID+"/tags", f.cookie,
		map[string]any{"tag_ids": []string{}})
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Empty(t, got.Tags)

	// Idempotent re-PATCH: same set, no error.
	rr = doReq(f.router, "PUT", "/api/v1/tasks/"+f.taskID+"/tags", f.cookie,
		map[string]any{"tag_ids": []string{ids[0]}})
	require.Equal(t, http.StatusOK, rr.Code)
	rr = doReq(f.router, "PUT", "/api/v1/tasks/"+f.taskID+"/tags", f.cookie,
		map[string]any{"tag_ids": []string{ids[0]}})
	assert.Equal(t, http.StatusOK, rr.Code)
}

// 5) Bad tag id → 404 with the offending id echoed back.
func TestTaskTags_Set_BadID(t *testing.T) {
	t.Parallel()
	f := tagDeps(t)
	rr := doReq(f.router, "POST", "/api/v1/tags", f.cookie, map[string]any{"name": "good"})
	require.Equal(t, http.StatusCreated, rr.Code)
	var good task.Tag
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&good))

	rr = doReq(f.router, "PUT", "/api/v1/tasks/"+f.taskID+"/tags", f.cookie,
		map[string]any{"tag_ids": []string{good.ID, "deadbeef-0000-0000-0000-000000000000"}})
	assert.Equal(t, http.StatusNotFound, rr.Code)
	assert.Contains(t, rr.Body.String(), "tag_not_found")
	assert.Contains(t, rr.Body.String(), "deadbeef")
}

// 6) PATCH /tasks/{id} with color clears when sent as "".
func TestTask_PatchColor_ClearAndSet(t *testing.T) {
	t.Parallel()
	f := tagDeps(t)

	// Set.
	rr := doReq(f.router, "PATCH", "/api/v1/tasks/"+f.taskID, f.cookie,
		map[string]any{"color": "#0ea5e9"})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var tr task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&tr))
	assert.Equal(t, "#0ea5e9", tr.Color)

	// Clear with explicit "".
	empty := ""
	rr = doReq(f.router, "PATCH", "/api/v1/tasks/"+f.taskID, f.cookie,
		map[string]any{"color": empty})
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&tr))
	assert.Equal(t, "", tr.Color)

	// Omitting colour leaves it alone: set, then PATCH title only.
	rr = doReq(f.router, "PATCH", "/api/v1/tasks/"+f.taskID, f.cookie,
		map[string]any{"color": "#22c55e"})
	require.Equal(t, http.StatusOK, rr.Code)
	rr = doReq(f.router, "PATCH", "/api/v1/tasks/"+f.taskID, f.cookie,
		map[string]any{"title": "renamed"})
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&tr))
	assert.Equal(t, "renamed", tr.Title)
	assert.Equal(t, "#22c55e", tr.Color, "colour must survive a colour-less PATCH")
}

// 7) GET /tags and GET /tasks/{id}/tags require auth.
func TestTags_RequiresAuth(t *testing.T) {
	t.Parallel()
	f := tagDeps(t)

	req := httptest.NewRequest("GET", "/api/v1/tags", nil)
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	req = httptest.NewRequest("GET", "/api/v1/tasks/"+f.taskID+"/tags", nil)
	rr = httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// 8) `tags` field on PATCH /tasks/{id} replaces the tag set.
func TestTask_PatchTags_Replace(t *testing.T) {
	t.Parallel()
	f := tagDeps(t)

	// Create two tags.
	tagIDs := make([]string, 0, 2)
	for _, name := range []string{"x", "y"} {
		rr := doReq(f.router, "POST", "/api/v1/tags", f.cookie, map[string]any{"name": name})
		require.Equal(t, http.StatusCreated, rr.Code)
		var tg task.Tag
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&tg))
		tagIDs = append(tagIDs, tg.ID)
	}

	// PATCH attaches via the tasks endpoint.
	rr := doReq(f.router, "PATCH", "/api/v1/tasks/"+f.taskID, f.cookie,
		map[string]any{"tags": tagIDs})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	// Verify.
	rr = doReq(f.router, "GET", "/api/v1/tasks/"+f.taskID+"/tags", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var got struct {
		Tags []task.Tag `json:"tags"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	require.Len(t, got.Tags, 2)
	gotNames := []string{got.Tags[0].Name, got.Tags[1].Name}
	assert.ElementsMatch(t, []string{"x", "y"}, gotNames)

	// PATCH with [] clears.
	rr = doReq(f.router, "PATCH", "/api/v1/tasks/"+f.taskID, f.cookie,
		map[string]any{"tags": []string{}})
	require.Equal(t, http.StatusOK, rr.Code)
	rr = doReq(f.router, "GET", "/api/v1/tasks/"+f.taskID+"/tags", f.cookie, nil)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Empty(t, got.Tags)
}
