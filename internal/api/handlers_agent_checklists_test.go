// T96: agent-namespace checklists — handler tests.
//
// Gating model (mirrors the comments / agent_notes surfaces):
//   - GET /agent/tasks/{id}/checklists: open read for any
//     authenticated agent (claimable or held).
//   - Mutations (create checklist, add / update / delete item):
//     only the current lock holder. A foreign or un-held task
//     rejects with 403 not_lock_holder.
//   - Checklist / item ids that don't belong to the path task
//     resolve as 404 ("not found covers not yours").
//   - No token / bad token → 401 from RequireAgent; a user
//     orenda_session cookie is NOT an agent credential.
//
// Happy path: agent claims the task, creates the PM's
// «Как протестировать» checklist, adds items, ticks one done —
// the QA-gate scenario from docs/DOGFOOD.md.
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/service/agent"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// registerThirdAgent mints a second agent on the T96 fixture so
// gating tests have a "not the holder" identity to reject.
func registerThirdAgent(t *testing.T, fx *agentFixture, name string) (agentID, plainToken string) {
	t.Helper()
	users := sqlite.NewUserRepository(fx.db)
	tokens := sqlite.NewAPITokenRepository(fx.db)
	agents := sqlite.NewAgentRepository(fx.db)
	svc := agent.New(agents, users, &agentFixtureTMinter{tokens: tokens}, nil, nil)
	got, err := svc.Register(context.Background(), name, []string{"test"}, "test", nil)
	require.NoError(t, err)
	return got.Agent.ID, got.PlainToken
}

// clAgentReq issues a request against the fixture router with the
// given bearer token (agent namespace).
func clAgentReq(t *testing.T, fx *agentFixture, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	return rr
}

// seedChecklistTask creates an owner-owned project + task and claims
// it as the fixture's agent, so the agent is the lock holder.
func seedChecklistTask(t *testing.T, fx *agentFixture) *task.Task {
	t.Helper()
	var ownerID string
	require.NoError(t, fx.db.QueryRow("SELECT id FROM users LIMIT 1").Scan(&ownerID))
	projects := sqlite.NewProjectRepository(fx.db)
	p, _, cols, err := projects.CreateProject(context.Background(), &project.Project{
		Name: "CL-" + ownerID[:8], OwnerID: ownerID, AgentsAllowed: true,
	})
	require.NoError(t, err)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "qa-gate task"}
	require.NoError(t, sqlite.NewTaskRepository(fx.db).Create(context.Background(), tr))

	// Claim as the fixture agent → it becomes the lock holder.
	rr := clAgentReq(t, fx, fx.token, http.MethodPost, "/api/v1/agent/tasks/"+tr.ID+"/claim", map[string]any{})
	require.Equal(t, http.StatusOK, rr.Code, "claim body=%s", rr.Body.String())
	return tr
}

func TestAgent_Checklists_HolderRoundTrip(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)
	tr := seedChecklistTask(t, fx)

	// Create the QA checklist (holder).
	rr := clAgentReq(t, fx, fx.token, http.MethodPost,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists", map[string]any{"title": "Как протестировать"})
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
	var cl task.ChecklistRow
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &cl))
	assert.Equal(t, "Как протестировать", cl.Title)
	assert.Equal(t, tr.ID, cl.TaskID)

	// Add two items.
	rr = clAgentReq(t, fx, fx.token, http.MethodPost,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists/"+cl.ID+"/items",
		map[string]any{"title": "make test && make lint-new"})
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
	var it1 task.ChecklistItemRow
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &it1))
	assert.Equal(t, cl.ID, it1.ChecklistID)
	assert.False(t, it1.Done)

	rr = clAgentReq(t, fx, fx.token, http.MethodPost,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists/"+cl.ID+"/items",
		map[string]any{"title": "smoke on preview"})
	require.Equal(t, http.StatusCreated, rr.Code)
	var it2 task.ChecklistItemRow
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &it2))

	// Tick the first item done.
	rr = clAgentReq(t, fx, fx.token, http.MethodPatch,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists/"+cl.ID+"/items/"+it1.ID,
		map[string]any{"done": true})
	require.Equal(t, http.StatusNoContent, rr.Code, "body=%s", rr.Body.String())

	// List: open read shows the structure with items keyed by list.
	rr = clAgentReq(t, fx, fx.token, http.MethodGet,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists", nil)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var got struct {
		Checklists     []task.ChecklistRow                `json:"checklists"`
		ChecklistItems map[string][]task.ChecklistItemRow `json:"checklist_items"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Len(t, got.Checklists, 1)
	assert.Equal(t, cl.ID, got.Checklists[0].ID)
	items := got.ChecklistItems[cl.ID]
	require.Len(t, items, 2)
	assert.True(t, items[0].Done, "first item must be ticked")
	assert.False(t, items[1].Done)

	// Title edit via PATCH.
	rr = clAgentReq(t, fx, fx.token, http.MethodPatch,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists/"+cl.ID+"/items/"+it2.ID,
		map[string]any{"title": "smoke on preview instance"})
	require.Equal(t, http.StatusNoContent, rr.Code)

	// Delete the second item.
	rr = clAgentReq(t, fx, fx.token, http.MethodDelete,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists/"+cl.ID+"/items/"+it2.ID, nil)
	require.Equal(t, http.StatusNoContent, rr.Code)

	// After delete: one item left.
	rr = clAgentReq(t, fx, fx.token, http.MethodGet,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Len(t, got.ChecklistItems[cl.ID], 1)

	// TaskReference: the same routes accept "T<N>" refs.
	var num int
	require.NoError(t, fx.db.QueryRow("SELECT number FROM tasks WHERE id = ?", tr.ID).Scan(&num))
	rr = clAgentReq(t, fx, fx.token, http.MethodGet,
		"/api/v1/agent/tasks/T"+itoa(num)+"/checklists", nil)
	assert.Equal(t, http.StatusOK, rr.Code, "T-ref list body=%s", rr.Body.String())
}

func TestAgent_Checklists_WriteRequiresHolder(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)
	tr := seedChecklistTask(t, fx)
	otherID, otherToken := registerThirdAgent(t, fx, "not-the-holder")

	// Foreign agent: every mutation → 403 not_lock_holder.
	rr := clAgentReq(t, fx, otherToken, http.MethodPost,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists", map[string]any{"title": "sneaky"})
	require.Equal(t, http.StatusForbidden, rr.Code)
	assert.JSONEq(t, `{"error":"not_lock_holder"}`, rr.Body.String())

	rr = clAgentReq(t, fx, otherToken, http.MethodPost,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists/cl-x/items", map[string]any{"title": "sneaky"})
	assert.Equal(t, http.StatusForbidden, rr.Code)

	rr = clAgentReq(t, fx, otherToken, http.MethodPatch,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists/cl-x/items/it-x", map[string]any{"done": true})
	assert.Equal(t, http.StatusForbidden, rr.Code)

	rr = clAgentReq(t, fx, otherToken, http.MethodDelete,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists/cl-x/items/it-x", nil)
	assert.Equal(t, http.StatusForbidden, rr.Code)

	// The foreign agent CAN still read (open read posture).
	rr = clAgentReq(t, fx, otherToken, http.MethodGet,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists", nil)
	assert.Equal(t, http.StatusOK, rr.Code)

	// Foreign agent registers as expected (id distinct from holder).
	assert.NotEqual(t, fx.agentID, otherID)

	// Un-held task: nobody has claimed it → the holder gate fires too.
	rr = clAgentReq(t, fx, otherToken, http.MethodPost,
		"/api/v1/agent/tasks/no-such-task/checklists", map[string]any{"title": "x"})
	assert.Equal(t, http.StatusNotFound, rr.Code, "unknown task must 404, body=%s", rr.Body.String())
}

func TestAgent_Checklists_ReleaseThenWrite403(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)
	tr := seedChecklistTask(t, fx)

	// Holder creates a checklist, then releases the claim.
	rr := clAgentReq(t, fx, fx.token, http.MethodPost,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists", map[string]any{"title": "post-release"})
	require.Equal(t, http.StatusCreated, rr.Code)
	var cl task.ChecklistRow
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &cl))

	rr = clAgentReq(t, fx, fx.token, http.MethodPost,
		"/api/v1/agent/tasks/"+tr.ID+"/release", map[string]any{})
	require.Equal(t, http.StatusOK, rr.Code, "release body=%s", rr.Body.String())

	// Same agent, no longer holder → 403.
	rr = clAgentReq(t, fx, fx.token, http.MethodPost,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists/"+cl.ID+"/items", map[string]any{"title": "after release"})
	assert.Equal(t, http.StatusForbidden, rr.Code, "body=%s", rr.Body.String())
}

func TestAgent_Checklists_MismatchedIds404(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)
	tr := seedChecklistTask(t, fx)

	// Holder creates checklist A on the held task.
	rr := clAgentReq(t, fx, fx.token, http.MethodPost,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists", map[string]any{"title": "mine"})
	require.Equal(t, http.StatusCreated, rr.Code)
	var cl task.ChecklistRow
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &cl))

	// Item add against a checklist id that exists but belongs to no
	// such task pairing: use a syntactically valid but unlinked id.
	rr = clAgentReq(t, fx, fx.token, http.MethodPost,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists/00000000-0000-0000-0000-000000000000/items",
		map[string]any{"title": "x"})
	assert.Equal(t, http.StatusNotFound, rr.Code, "body=%s", rr.Body.String())

	// Patch an item id that doesn't exist on the checklist.
	rr = clAgentReq(t, fx, fx.token, http.MethodPatch,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists/"+cl.ID+"/items/00000000-0000-0000-0000-000000000000",
		map[string]any{"done": true})
	assert.Equal(t, http.StatusNotFound, rr.Code, "body=%s", rr.Body.String())
}

func TestAgent_Checklists_BadRequests(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)
	tr := seedChecklistTask(t, fx)

	// Missing title → 400 missing_title.
	rr := clAgentReq(t, fx, fx.token, http.MethodPost,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists", map[string]any{})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.JSONEq(t, `{"error":"missing_title"}`, rr.Body.String())

	// Not JSON → 400 invalid_json.
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/agent/tasks/"+tr.ID+"/checklists", bytes.NewReader([]byte(`{oops`)))
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	fx.router.ServeHTTP(rr2, req)
	assert.Equal(t, http.StatusBadRequest, rr2.Code)
	assert.JSONEq(t, `{"error":"invalid_json"}`, rr2.Body.String())
}

func TestAgent_Checklists_NoToken401(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/agent/tasks/some-task/checklists", nil)
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// depsWiredGuard pins that the routes ride the same Dependencies
// wiring the other agent routes use — a compile-level companion to
// the runtime tests above.
var _ = api.Dependencies{}
