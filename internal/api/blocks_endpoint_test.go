package api_test

// Task 115: single-edge blocker endpoints — POST /tasks/{id}/blocks
// and DELETE /tasks/{id}/blocks/{blockedBy}.
//
// Required cases (DoD):
//   - POST success → edge exists + status auto-flips to blocked
//     (+ blocked_prev_status remembers todo).
//   - POST cycle → 422 invalid_dependency.
//   - POST unknown blocker → 404.
//   - POST self-block → 422.
//   - POST idempotent: same edge twice → second POST is 200, one edge.
//   - DELETE removes the edge + restores status from prev.
//   - DELETE unknown edge → 404.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
)

// blocksBody mirrors the blocksResponse shape of handlers_blocks.go.
type blocksBody struct {
	Blockers []task.BlockerRow `json:"blockers"`
	Task     *task.Task        `json:"task"`
}

func TestTaskBlocks_HTTP(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	ctx := context.Background()

	a := &task.Task{ProjectID: f.projectID, ColumnID: f.cols[1].ID, Title: "A"}
	b := &task.Task{ProjectID: f.projectID, ColumnID: f.cols[1].ID, Title: "B"}
	require.NoError(t, f.taskRepo.Create(ctx, a))
	require.NoError(t, f.taskRepo.Create(ctx, b))

	// --- POST success: edge exists, status → blocked, prev saved.
	rr := doReq(f.router, http.MethodPost, "/api/v1/tasks/"+a.ID+"/blocks",
		f.cookie, map[string]any{"blocked_by": b.ID})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var got blocksBody
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	require.Len(t, got.Blockers, 1)
	assert.Equal(t, b.ID, got.Blockers[0].BlockerID)
	require.NotNil(t, got.Task)
	assert.Equal(t, task.StatusBlocked, got.Task.Status)
	assert.Equal(t, task.StatusTodo, got.Task.BlockedPrevStatus)

	// Persisted: re-read shows the same state.
	persisted, err := f.taskRepo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusBlocked, persisted.Status)
	assert.Equal(t, task.StatusTodo, persisted.BlockedPrevStatus)

	// --- Idempotent: POSTing the same edge again is a 200 no-op.
	rr = doReq(f.router, http.MethodPost, "/api/v1/tasks/"+a.ID+"/blocks",
		f.cookie, map[string]any{"blocked_by": b.ID})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	require.Len(t, got.Blockers, 1, "no duplicate edge")

	// --- Self-block → 422.
	rr = doReq(f.router, http.MethodPost, "/api/v1/tasks/"+a.ID+"/blocks",
		f.cookie, map[string]any{"blocked_by": a.ID})
	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), "invalid_dependency")

	// --- Unknown blocker → 404.
	rr = doReq(f.router, http.MethodPost, "/api/v1/tasks/"+a.ID+"/blocks",
		f.cookie, map[string]any{"blocked_by": "no-such-task"})
	assert.Equal(t, http.StatusNotFound, rr.Code, rr.Body.String())

	// --- Unknown target → 404.
	rr = doReq(f.router, http.MethodPost, "/api/v1/tasks/no-such-task/blocks",
		f.cookie, map[string]any{"blocked_by": b.ID})
	assert.Equal(t, http.StatusNotFound, rr.Code, rr.Body.String())

	// --- Cycle: B depends on A → 422 (A→B already exists).
	rr = doReq(f.router, http.MethodPost, "/api/v1/tasks/"+b.ID+"/blocks",
		f.cookie, map[string]any{"blocked_by": a.ID})
	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), "invalid_dependency")

	// --- DELETE removes the edge and restores prev status.
	rr = doReq(f.router, http.MethodDelete, "/api/v1/tasks/"+a.ID+"/blocks/"+b.ID,
		f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	// Fresh decode target every time: absent JSON fields (e.g. the
	// omitted empty blocked_prev_status) would otherwise keep values
	// from a previous decode into the reused struct.
	got = blocksBody{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Empty(t, got.Blockers)
	require.NotNil(t, got.Task)
	assert.Equal(t, task.StatusTodo, got.Task.Status, "prev status restored")
	assert.Empty(t, got.Task.BlockedPrevStatus, "prev memory cleared")

	persisted, err = f.taskRepo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusTodo, persisted.Status)

	// --- DELETE unknown edge → 404.
	rr = doReq(f.router, http.MethodDelete, "/api/v1/tasks/"+a.ID+"/blocks/"+b.ID,
		f.cookie, nil)
	assert.Equal(t, http.StatusNotFound, rr.Code, rr.Body.String())
}

// T-ref acceptance: both task arguments accept "T<N>" refs.
func TestTaskBlocks_TRefs(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	ctx := context.Background()

	a := &task.Task{ProjectID: f.projectID, ColumnID: f.cols[1].ID, Title: "A"}
	b := &task.Task{ProjectID: f.projectID, ColumnID: f.cols[1].ID, Title: "B"}
	require.NoError(t, f.taskRepo.Create(ctx, a))
	require.NoError(t, f.taskRepo.Create(ctx, b))

	rr := doReq(f.router, http.MethodPost, "/api/v1/tasks/T"+itoaTask(a.Number)+"/blocks",
		f.cookie, map[string]any{"blocked_by": "T" + itoaTask(b.Number)})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var got blocksBody
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	require.Len(t, got.Blockers, 1)
	assert.Equal(t, b.ID, got.Blockers[0].BlockerID)
}

// itoaTask avoids importing strconv for two call sites.
func itoaTask(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
