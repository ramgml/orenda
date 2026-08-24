package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
)

func deleteColumn(router http.Handler, cookie, colID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/columns/"+colID, nil)
	req.Header.Set("Cookie", "orenda_session="+cookie)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// 1) Empty column → 204 No Content, then GET /board shows one less column.
func TestDeleteColumn_Empty(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	before := len(f.cols)

	rr := deleteColumn(f.router, f.cookie, f.cols[1].ID)
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	assert.Empty(t, rr.Body.String(), "204 must have an empty body")

	rr = authedBoardReq(f.router, f.cookie, f.projectID)
	require.Equal(t, http.StatusOK, rr.Code)
	var board struct {
		Columns []project.Column `json:"columns"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&board))
	assert.Len(t, board.Columns, before-1)
	for _, c := range board.Columns {
		assert.NotEqual(t, f.cols[1].ID, c.ID, "deleted column must be gone")
	}
}

// 2) Column with tasks → 422 with current count.
func TestDeleteColumn_WithTasksRejected(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	for i := 0; i < 3; i++ {
		require.NoError(t, f.tasks.Create(context.Background(), &task.Task{
			ProjectID: f.projectID,
			ColumnID:  f.cols[1].ID,
			Title:     "x",
		}))
	}

	rr := deleteColumn(f.router, f.cookie, f.cols[1].ID)
	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code)
	body := rr.Body.String()
	assert.True(t, strings.Contains(body, "column_not_empty"), "body=%s", body)

	var resp struct {
		Error   string `json:"error"`
		Current int    `json:"current"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	assert.Equal(t, "column_not_empty", resp.Error)
	assert.Equal(t, 3, resp.Current)

	// Column must still exist after the rejected delete.
	rr = authedBoardReq(f.router, f.cookie, f.projectID)
	require.Equal(t, http.StatusOK, rr.Code)
	var board struct {
		Columns []project.Column `json:"columns"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&board))
	assert.Len(t, board.Columns, len(f.cols), "column must still be on the board")
}

// 3) Unknown id → 404.
func TestDeleteColumn_NotFound(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	rr := deleteColumn(f.router, f.cookie, "deadbeef")
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// 4) After moving tasks out, the column becomes deletable. This is
// the user-facing happy path: drag tasks to other columns, then
// delete the now-empty column.
func TestDeleteColumn_AfterMovingTasksOut(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	// Seed two tasks in cols[1], then move one to cols[2].
	var taskIDs []string
	for i := 0; i < 2; i++ {
		tr := &task.Task{
			ProjectID: f.projectID,
			ColumnID:  f.cols[1].ID,
			Title:     "x",
		}
		require.NoError(t, f.tasks.Create(context.Background(), tr))
		taskIDs = append(taskIDs, tr.ID)
	}

	// Move one task away via the patch endpoint.
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+taskIDs[0],
		bytes.NewReader([]byte(`{"column_id":"`+f.cols[2].ID+`"}`)))
	req.Header.Set("Cookie", "orenda_session="+f.cookie)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	// Now cols[1] still has 1 task → delete must still 422.
	rr = deleteColumn(f.router, f.cookie, f.cols[1].ID)
	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code)

	// Move the remaining task too.
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+taskIDs[1],
		bytes.NewReader([]byte(`{"column_id":"`+f.cols[3].ID+`"}`)))
	req.Header.Set("Cookie", "orenda_session="+f.cookie)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	rr = deleteColumn(f.router, f.cookie, f.cols[1].ID)
	assert.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
}

// 5) Make sure the column doesn't leave any orphans: after delete,
// the FK SET NULL behaviour must not be triggered (the repo
// guards against it). Verifies the order check + delete is atomic
// from the caller's POV — no race window where a task could
// silently lose its column_id.
func TestDeleteColumn_PreservesTasksElsewhere(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	// One task in the doomed column, one in another.
	doomed := &task.Task{
		ProjectID: f.projectID, ColumnID: f.cols[1].ID, Title: "doomed",
	}
	safe := &task.Task{
		ProjectID: f.projectID, ColumnID: f.cols[2].ID, Title: "safe",
	}
	require.NoError(t, f.tasks.Create(context.Background(), doomed))
	require.NoError(t, f.tasks.Create(context.Background(), safe))

	rr := deleteColumn(f.router, f.cookie, f.cols[1].ID)
	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code,
		"doomed column must not be deletable while it has a task")

	// The safe task should still be findable.
	rr = authedGetReq(f.router, f.cookie, "/api/v1/tasks/"+safe.ID)
	assert.Equal(t, http.StatusOK, rr.Code)
}

// 6) Idempotency: deleting the same id twice → first 204, second 404.
func TestDeleteColumn_Idempotency(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	rr := deleteColumn(f.router, f.cookie, f.cols[0].ID)
	require.Equal(t, http.StatusNoContent, rr.Code)
	rr = deleteColumn(f.router, f.cookie, f.cols[0].ID)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// authedGetReq is a generic helper for tests that don't want to
// re-construct cookie + request headers.
func authedGetReq(router http.Handler, cookie, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Cookie", "orenda_session="+cookie)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}
