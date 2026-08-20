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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"

	agentservice "github.com/ramgml/orenda/internal/service/agent"
)

func TestAgent_PatchOwnProposal_OK(t *testing.T) {
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
	assert.Equal(t, "agent", string(patchRow.ActorType))
	assert.Equal(t, f.agentID, patchRow.ActorID)
}

func TestAgent_PatchForeignProposal_403(t *testing.T) {
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

func TestAgent_ContextAnyAgent_OK(t *testing.T) {
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
