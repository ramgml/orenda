package api_test

// Task 115: agent PATCH blocked_by semantics.
//
//   - own un-triaged proposal: blocked_by accepted (absent=untouched,
//     []=clear, ids=replace with cycle/self checks), full-replacement
//     semantics like PUT /dependencies.
//   - adding a blocker auto-flips status → blocked (prev=backlog).
//   - triaged own proposal → 403 (blocked_by never bypasses the gate).
//   - self-block → 422 invalid_dependency.
//   - unknown blocker ref → 404.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// jsonDecode is a small local alias so every decode gets a fresh
// target (absent JSON fields must not keep stale values).
func jsonDecode(t *testing.T, data []byte, v any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(data, v))
}

func TestAgent_PatchBlockedBy_Semantics(t *testing.T) {
	t.Parallel()
	f := newProposeFixture(t)
	tasks := sqlite.NewTaskRepository(f.db)
	ctx := context.Background()

	// Agent proposes A; seed two helper tasks B + C.
	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	jsonDecode(t, rr.Body.Bytes(), &proposed)

	b := &task.Task{ProjectID: f.projectID, ColumnID: f.todoColID, Title: "B"}
	c := &task.Task{ProjectID: f.projectID, ColumnID: f.todoColID, Title: "C"}
	require.NoError(t, tasks.Create(ctx, b))
	require.NoError(t, tasks.Create(ctx, c))

	// 1) Absent field → untouched.
	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"title": "renamed"})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var updated task.Task
	jsonDecode(t, rr.Body.Bytes(), &updated)
	assert.Equal(t, task.StatusBacklog, updated.Status)

	// 2) blocked_by: [B] → replaces set; auto-flip to blocked (prev=backlog).
	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"blocked_by": []string{b.ID}})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	jsonDecode(t, rr.Body.Bytes(), &updated)
	assert.Equal(t, task.StatusBlocked, updated.Status, "auto-block on add")
	assert.Equal(t, task.StatusBacklog, updated.BlockedPrevStatus)

	// 3) Auto-block took the task OUT of backlog, so the proposal
	// gate now (correctly) refuses further field edits: 403.
	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"title": "x"})
	assert.Equal(t, http.StatusForbidden, rr.Code, rr.Body.String())

	// 4) Replace-set semantics still verifiable through the service:
	// blocked_by via the OWNER surface (PUT /dependencies) replaces
	// the set; B dropped, C present.
	rr = f.doWithCookie(t, http.MethodPut, "/api/v1/tasks/"+proposed.ID+"/dependencies",
		map[string]any{"depends_on_ids": []string{c.ID}})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	rr2 := f.doWithCookie(t, http.MethodGet, "/api/v1/tasks/"+proposed.ID+"/blockers", nil)
	require.Equal(t, http.StatusOK, rr2.Code)
	var body struct {
		Blockers []task.BlockerRow `json:"blockers"`
	}
	jsonDecode(t, rr2.Body.Bytes(), &body)
	require.Len(t, body.Blockers, 1)
	assert.Equal(t, c.ID, body.Blockers[0].BlockerID)

	// 5) While blocked, the proposal gate keeps refusing (403) —
	// clearing goes through the OWNER surface (PUT /dependencies
	// with []), which auto-unblocks and restores backlog. Afterwards
	// the agent can edit again.
	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"blocked_by": []string{}})
	assert.Equal(t, http.StatusForbidden, rr.Code, rr.Body.String())

	rr = f.doWithCookie(t, http.MethodPut, "/api/v1/tasks/"+proposed.ID+"/dependencies",
		map[string]any{"depends_on_ids": []string{}})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	persisted, gerr := tasks.GetByID(ctx, proposed.ID)
	require.NoError(t, gerr)
	assert.Equal(t, task.StatusBacklog, persisted.Status, "prev restored on clear")
	assert.Equal(t, task.Status(""), persisted.BlockedPrevStatus)

	// 6) Self-block → 422.
	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"blocked_by": []string{proposed.ID}})
	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), "invalid_dependency")

	// 7) Unknown blocker T-ref → 404.
	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"blocked_by": []string{"T999999"}})
	assert.Equal(t, http.StatusNotFound, rr.Code, rr.Body.String())
}

func TestAgent_PatchBlockedBy_TriagedProposal_403(t *testing.T) {
	t.Parallel()
	f := newProposeFixture(t)

	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	jsonDecode(t, rr.Body.Bytes(), &proposed)

	// Owner triages: backlog → todo.
	rr = f.doWithCookie(t, http.MethodPatch, "/api/v1/tasks/"+proposed.ID, map[string]any{"status": "todo"})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	// Now the agent's blocked_by hits the proposal gate → 403.
	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"blocked_by": []string{proposed.ID}})
	assert.Equal(t, http.StatusForbidden, rr.Code, rr.Body.String())
}
