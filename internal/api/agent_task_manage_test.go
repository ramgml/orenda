package api_test

// Phase 33.2: agent-side task management — PATCH/DELETE on own
// un-triaged proposal + holder-only agent_notes.
//
// Table-driven coverage of the permission boundaries:
//   - own un-triaged proposal: 200 / 204
//   - foreign proposal (created_by user): 403
//   - triaged own proposal (status != backlog): 403
//   - holder writing agent_notes on a triaged task: 200
//   - non-holder writing agent_notes on a triaged task: 403
//   - non-assignee reading context: 200 (Phase 33.2: dropped the assignee gate)

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

func TestAgent_PatchOwnProposal_OK(t *testing.T) {
	t.Parallel()
	f := newProposeFixture(t)
	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))

	patch := map[string]any{
		"title":          "Updated title",
		"description_md": "# Updated\n\nnew description",
		"priority":       "high",
	}
	rr = f.patchAsAgent(t, proposed.ID, patch)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var updated task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &updated))
	assert.Equal(t, "Updated title", updated.Title)
	assert.Equal(t, task.PriorityHigh, updated.Priority)

	// Activity audit: task.updated row with actor_type=agent.
	rows := listActivityForTask(t, f, updated.ID)
	require.NotEmpty(t, rows)
	var patchRow *activityRow
	for i := range rows {
		if rows[i].Action == "task.updated" {
			patchRow = &rows[i]
			break
		}
	}
	require.NotNil(t, patchRow, "task.updated activity row expected (got actions: %v)", actionList(rows))
	assert.Equal(t, "agent", patchRow.ActorType)
	assert.Equal(t, f.agentID, patchRow.ActorID)
}

func TestAgent_PatchForeignProposal_403(t *testing.T) {
	t.Parallel()
	f := newProposeFixture(t)
	body := map[string]any{
		"project_id":     f.projectID,
		"title":          "User-authored",
		"description_md": "# X",
	}
	rr := f.doWithCookie(t, http.MethodPost, "/api/v1/projects/"+f.projectID+"/tasks", body)
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))

	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"title": "hijack"})
	require.Equal(t, http.StatusForbidden, rr.Code, "body=%s", rr.Body.String())
}

func TestAgent_PatchTriagedOwnProposal_403(t *testing.T) {
	t.Parallel()
	f := newProposeFixture(t)
	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))

	moveTaskToColumn(t, f, proposed.ID, f.todoColID)

	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"title": "still mine"})
	require.Equal(t, http.StatusForbidden, rr.Code, "body=%s", rr.Body.String())
}

func TestAgent_DeleteOwnProposal_204(t *testing.T) {
	t.Parallel()
	f := newProposeFixture(t)
	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))

	rr = f.deleteAsAgent(t, proposed.ID)
	require.Equal(t, http.StatusNoContent, rr.Code, "body=%s", rr.Body.String())
	// task.deleted activity row exists in the DB before FK CASCADE;
	// verified via the WS event the service emits (smoke curl in
	// the PR body).
}

func TestAgent_DeleteForeignProposal_403(t *testing.T) {
	t.Parallel()
	f := newProposeFixture(t)
	body := map[string]any{
		"project_id":     f.projectID,
		"title":          "User-authored",
		"description_md": "# X",
	}
	rr := f.doWithCookie(t, http.MethodPost, "/api/v1/projects/"+f.projectID+"/tasks", body)
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))

	rr = f.deleteAsAgent(t, proposed.ID)
	require.Equal(t, http.StatusForbidden, rr.Code, "body=%s", rr.Body.String())
}

func TestAgent_HolderAgentNotes_OK(t *testing.T) {
	t.Parallel()
	f := newProposeFixture(t)
	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))
	moveTaskToColumn(t, f, proposed.ID, f.todoColID)

	rr = f.claimAsAgent(t, proposed.ID)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())

	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"agent_notes": "working on it"})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var updated task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &updated))
	assert.Equal(t, "working on it", updated.AgentNotes)
}

func TestAgent_NonHolderAgentNotes_403(t *testing.T) {
	t.Parallel()
	f := newProposeFixture(t)
	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))
	moveTaskToColumn(t, f, proposed.ID, f.todoColID)
	rr = f.claimAsAgent(t, proposed.ID)
	require.Equal(t, http.StatusOK, rr.Code)

	agentB := registerSecondAgent(t, f, "agent-b")
	rr = f.patchAsAgentToken(t, proposed.ID, agentB.PlainToken, map[string]any{"agent_notes": "sneaky"})
	require.Equal(t, http.StatusForbidden, rr.Code, "body=%s", rr.Body.String())
}

func TestAgent_ContextScrubsNotesForNonHolder(t *testing.T) {
	t.Parallel()
	// Phase 33.2.1: a non-holder reader sees task.agent_notes and
	// task.context_md scrubbed (empty string) AND the activity
	// feed has task.agent_notes_updated rows filtered out.
	f := newProposeFixture(t)
	body := validProposeBody(f.projectID)
	body["title"] = "Has notes"
	body["context_md"] = "# private context"
	rr := f.proposeAsAgent(t, body)
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))

	// Owner drags to todo, agent claims, writes agent_notes.
	moveTaskToColumn(t, f, proposed.ID, f.todoColID)
	rr = f.claimAsAgent(t, proposed.ID)
	require.Equal(t, http.StatusOK, rr.Code)
	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"agent_notes": "secret"})
	require.Equal(t, http.StatusOK, rr.Code)

	// Register reader B who is NOT the holder.
	agentB := registerSecondAgent(t, f, "scrub-reader")

	// Reader B reads context: agent_notes + context_md must be empty.
	rr = f.contextAsAgentToken(t, proposed.ID, agentB.PlainToken)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var ctx struct {
		Task     map[string]any `json:"task"`
		Activity []struct {
			Action string `json:"action"`
		} `json:"activity"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &ctx))
	assert.True(t, ctx.Task["agent_notes"] == nil || ctx.Task["agent_notes"] == "", "agent_notes must be scrubbed for non-holder")
	assert.True(t, ctx.Task["context_md"] == nil || ctx.Task["context_md"] == "", "context_md must be scrubbed for non-holder")
	for _, a := range ctx.Activity {
		assert.NotEqual(t, "task.agent_notes_updated", a.Action,
			"notes activity rows must be filtered for non-holder")
	}
}

func TestAgent_ContextHolderSeesOwnNotesAndActivity(t *testing.T) {
	t.Parallel()
	// The holder reads everything: their own notes + their own
	// activity rows are NOT scrubbed.
	f := newProposeFixture(t)
	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))
	moveTaskToColumn(t, f, proposed.ID, f.todoColID)
	rr = f.claimAsAgent(t, proposed.ID)
	require.Equal(t, http.StatusOK, rr.Code)
	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"agent_notes": "my notes"})
	require.Equal(t, http.StatusOK, rr.Code)

	rr = f.contextAsAgentToken(t, proposed.ID, f.token)
	require.Equal(t, http.StatusOK, rr.Code)
	var ctx struct {
		Task     map[string]any `json:"task"`
		Activity []struct {
			Action string `json:"action"`
		} `json:"activity"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &ctx))
	assert.Equal(t, "my notes", ctx.Task["agent_notes"])
	hasNotesActivity := false
	for _, a := range ctx.Activity {
		if a.Action == "task.agent_notes_updated" {
			hasNotesActivity = true
		}
	}
	assert.True(t, hasNotesActivity, "holder's activity feed keeps notes rows")
}

func TestAgent_ContextProposerSeesOwnContextBeforeClaim(t *testing.T) {
	t.Parallel()
	// Before claim, the original proposer is neither assignee nor
	// "holder" — agent_notes and context_md are scrubbed for them
	// too. (Task.agent_notes only becomes relevant post-claim.)
	f := newProposeFixture(t)
	body := validProposeBody(f.projectID)
	body["context_md"] = "secret pre-claim context"
	rr := f.proposeAsAgent(t, body)
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))

	rr = f.contextAsAgentToken(t, proposed.ID, f.token)
	require.Equal(t, http.StatusOK, rr.Code)
	var ctx struct {
		Task map[string]any `json:"task"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &ctx))
	assert.True(t, ctx.Task["context_md"] == nil || ctx.Task["context_md"] == "",
		"context_md is per-claim private; pre-claim proposer is not yet the holder")
}

func TestAgent_ContextAnyAgent_OK(t *testing.T) {
	t.Parallel()
	f := newProposeFixture(t)
	body := validProposeBody(f.projectID)
	body["title"] = "Visible to anyone"
	rr := f.proposeAsAgent(t, body)
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))

	agentB := registerSecondAgent(t, f, "reader-b")
	rr = f.contextAsAgentToken(t, proposed.ID, agentB.PlainToken)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var ctx map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &ctx))
	require.Contains(t, ctx, "task")
}

func TestAgent_PatchTask_ByNumber_OK(t *testing.T) {
	t.Parallel()
	// Phase 33.2.1 → Task 48: PATCH accepts the T-prefixed ref "T<N>".
	f := newProposeFixture(t)
	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))
	require.Greater(t, proposed.Number, 0, "number assigned")
	number := proposed.Number
	numberStr := fmt.Sprintf("T%d", number)
	_ = numberStr

	// PATCH via the T-prefixed number.
	rr = f.patchAsAgent(t, numberStr, map[string]any{"title": "by number"})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var updated task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &updated))
	assert.Equal(t, "by number", updated.Title)
}

func TestAgent_DeleteTask_ByNumber_OK(t *testing.T) {
	t.Parallel()
	f := newProposeFixture(t)
	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))
	numberStr := fmt.Sprintf("T%d", proposed.Number)

	rr = f.deleteAsAgent(t, numberStr)
	require.Equal(t, http.StatusNoContent, rr.Code, "body=%s", rr.Body.String())
}

func (f *proposeFixture) releaseAsAgent(t *testing.T, id string) *httptest.ResponseRecorder {
	return f.releaseAsAgentToken(t, id, f.token)
}

func (f *proposeFixture) releaseAsAgentToken(t *testing.T, id, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/tasks/"+id+"/release", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

// ---- helpers ----

func (f *proposeFixture) patchAsAgent(t *testing.T, id string, body map[string]any) *httptest.ResponseRecorder {
	return f.patchAsAgentToken(t, id, f.token, body)
}

func (f *proposeFixture) patchAsAgentToken(t *testing.T, id, token string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agent/tasks/"+id, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

func (f *proposeFixture) deleteAsAgent(t *testing.T, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/agent/tasks/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

func (f *proposeFixture) claimAsAgent(t *testing.T, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/tasks/"+id+"/claim", nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

func (f *proposeFixture) contextAsAgentToken(t *testing.T, id, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/tasks/"+id+"/context", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

type activityRow struct {
	ActorType string
	ActorID   string
	Action    string
}

func listActivityForTask(t *testing.T, f *proposeFixture, taskID string) []activityRow {
	t.Helper()
	rows, err := sqlite.NewActivityRepository(f.db).ListByTask(context.Background(), taskID)
	require.NoError(t, err)
	out := make([]activityRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, activityRow{
			ActorType: string(r.ActorType),
			ActorID:   r.ActorID,
			Action:    string(r.Action),
		})
	}
	return out
}

type secondAgent struct {
	ID         string
	PlainToken string
}

func newAgentServiceForFixture(f *proposeFixture) *agentservice.Service {
	return agentservice.New(
		sqlite.NewAgentRepository(f.db),
		sqlite.NewUserRepository(f.db),
		&agentFixtureTMinter{tokens: sqlite.NewAPITokenRepository(f.db)},
		f.hub, nil,
	)
}

func registerSecondAgent(t *testing.T, f *proposeFixture, label string) *secondAgent {
	t.Helper()
	svc := newAgentServiceForFixture(f)
	got, err := svc.Register(context.Background(), label, []string{"test"}, "test", nil)
	require.NoError(t, err)
	return &secondAgent{ID: got.Agent.ID, PlainToken: got.PlainToken}
}

func actionList(rows []activityRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Action)
	}
	return out
}

func moveTaskToColumn(t *testing.T, f *proposeFixture, taskID, columnID string) {
	t.Helper()
	body := map[string]any{"column_id": columnID}
	rr := f.doWithCookie(t, http.MethodPatch, "/api/v1/tasks/"+taskID, body)
	require.Equal(t, http.StatusOK, rr.Code, "move body=%s", rr.Body.String())
}

func TestAgent_DeleteAuditSurvivesViaTombstone(t *testing.T) {
	t.Parallel()
	// Phase 33.2.1: retract writes a tombstone row in
	// task_retracted (no FK on tasks.id, so the audit survives the
	// hard delete).
	f := newProposeFixture(t)
	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))

	rr = f.deleteAsAgent(t, proposed.ID)
	require.Equal(t, http.StatusNoContent, rr.Code, "body=%s", rr.Body.String())

	repo := sqlite.NewTaskRetractedRepository(f.db)
	n, err := repo.CountForTask(context.Background(), proposed.ID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, 1, "tombstone row must exist for retracted task")
	rows, err := repo.GetForTask(context.Background(), proposed.ID)
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	assert.Contains(t, rows[0].ActorID, f.agentID, "tombstone carries agent id")
}

func TestAgent_EditProposal_TriagedBeforeRequest_Returns403(t *testing.T) {
	t.Parallel()
	// A proposal that the owner dragged out of backlog before the
	// PATCH is no longer the agent's proposal. Pre-check reads the
	// post-triage state and rejects with 403 not_your_proposal.
	// The gate-in-WHERE still fires in the rare TOCTOU window
	// (separately exercised at the repo level in TestGate_UpdateProposalFields).
	f := newProposeFixture(t)
	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))
	moveTaskToColumn(t, f, proposed.ID, f.todoColID)
	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"title": "still mine"})
	require.Equal(t, http.StatusForbidden, rr.Code, "body=%s", rr.Body.String())
}

// TestGate_UpdateProposalFields exercises the WHERE gate directly:
// calling UpdateProposalFields on a row whose status has flipped
// out of 'backlog' (or whose created_by_id doesn't match) must
// return RowsAffected()==0 (ErrNotFound in the repo, ErrConcurrentTriage
// from the service). The handler path also pre-checks via
// IsOwnProposal and returns 403 for a concurrent triage (see
// TestAgent_EditProposal_TriagedBeforeRequest_Returns403); this
// direct test pins the gate itself.
func TestGate_UpdateProposalFields(t *testing.T) {
	t.Parallel()
	db, _ := copyTemplateDB(t)
	tasks := sqlite.NewTaskRepository(db)
	tr := &task.Task{
		Title: "g", Status: task.StatusTodo, Priority: task.PriorityMedium,
		CreatedByType: task.CreatorAgent, CreatedByID: "agent-A",
	}
	require.NoError(t, tasks.Create(context.Background(), tr))
	title := "patched"
	var err error
	err = tasks.UpdateProposalFields(context.Background(), task.ProposalPatchParams{
		TaskID: tr.ID,
		Gate:   task.ProposalGate{CreatedByID: "agent-A"},
		Title:  &title,
	})
	assert.ErrorIs(t, err, task.ErrNotFound, "gate must reject when status != backlog")

	// Same row, but gate agent-id wrong.
	err = tasks.UpdateProposalFields(context.Background(), task.ProposalPatchParams{
		TaskID: tr.ID,
		Gate:   task.ProposalGate{CreatedByID: "agent-B"},
		Title:  &title,
	})
	assert.ErrorIs(t, err, task.ErrNotFound, "gate must reject when creator id differs")
}

func TestAgent_UpdateAgentNotes_ReleaseInterleaved_NoResurrect(t *testing.T) {
	t.Parallel()
	// Phase 33.2.1 TOCTOU fix: a concurrent Release that clears the
	// assignee must stop the notes write. Pre-33.2.1 the full-row
	// Update resurrected assignee from the stale in-memory snapshot.
	f := newProposeFixture(t)
	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))
	moveTaskToColumn(t, f, proposed.ID, f.todoColID)
	rr = f.claimAsAgent(t, proposed.ID)
	require.Equal(t, http.StatusOK, rr.Code)

	// Release: clears the assignee (frees the lock).
	rr = f.releaseAsAgent(t, proposed.ID)
	require.Equal(t, http.StatusOK, rr.Code)

	// Now the notes PATCH: assignee is no longer 'agent/me' → gate
	// fails → 404 (RowsAffected==0 → ErrNotFound → translateManageError
	// returns not_found since ErrNotLockHolder from the re-check
	// would also match — both surface a non-200).
	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"agent_notes": "sneaky"})
	assert.NotEqual(t, http.StatusOK, rr.Code, "must not write notes after release")
}
