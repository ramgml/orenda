package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
)

// Phase 15: HTTP surface for task dependencies.
//
// Cases:
//   - PUT /tasks/{id}/dependencies replaces the set; GET /blockers
//     returns the resulting edges.
//   - PUT with a self-loop → 422.
//   - PUT with a cycle → 422.
//   - Empty PUT clears every blocker.
//   - Unknown task → 404.
//   - missing depends_on_ids → 400.

func TestTaskDependencies_HTTP(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	ctx := context.Background()

	a := &task.Task{ProjectID: f.projectID, ColumnID: f.cols[1].ID, Title: "A"}
	b := &task.Task{ProjectID: f.projectID, ColumnID: f.cols[1].ID, Title: "B"}
	require.NoError(t, f.taskRepo.Create(ctx, a))
	require.NoError(t, f.taskRepo.Create(ctx, b))

	// Empty by default.
	rr := doReq(f.router, http.MethodGet, "/api/v1/tasks/"+a.ID+"/blockers",
		f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var got struct {
		Blockers []task.BlockerRow `json:"blockers"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Empty(t, got.Blockers)

	// Replace: A blocked by B.
	rr = doReq(f.router, http.MethodPut, "/api/v1/tasks/"+a.ID+"/dependencies",
		f.cookie, map[string]any{"depends_on_ids": []string{b.ID}})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	require.Len(t, got.Blockers, 1)
	assert.Equal(t, b.ID, got.Blockers[0].BlockerID)

	// Cycle: now B depends on A → 422.
	rr = doReq(f.router, http.MethodPut, "/api/v1/tasks/"+b.ID+"/dependencies",
		f.cookie, map[string]any{"depends_on_ids": []string{a.ID}})
	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), "invalid_dependency")

	// Self-dependency → 422.
	rr = doReq(f.router, http.MethodPut, "/api/v1/tasks/"+a.ID+"/dependencies",
		f.cookie, map[string]any{"depends_on_ids": []string{a.ID}})
	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code, rr.Body.String())

	// Clear with empty list.
	rr = doReq(f.router, http.MethodPut, "/api/v1/tasks/"+a.ID+"/dependencies",
		f.cookie, map[string]any{"depends_on_ids": []string{}})
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Empty(t, got.Blockers)

	// Missing field → 400.
	rr = doReq(f.router, http.MethodPut, "/api/v1/tasks/"+a.ID+"/dependencies",
		f.cookie, map[string]any{})
	assert.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())

	// Unknown task → 404.
	rr = doReq(f.router, http.MethodPut, "/api/v1/tasks/no-such-task-id/dependencies",
		f.cookie, map[string]any{"depends_on_ids": []string{b.ID}})
	assert.Equal(t, http.StatusNotFound, rr.Code, rr.Body.String())
}

// depFixtures placeholder was removed — the type was reserved for a
// future dep-only test case that hasn't materialised. Phase 15 tests
// reuse columnDeps and read taskRepo directly.
