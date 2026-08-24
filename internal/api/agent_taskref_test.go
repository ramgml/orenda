package api_test

// Task numbers (Phase 33): the agent surface resolves "T<N>" in
// place of the task UUID on every task-id-taking route, and the task
// JSON carries the number on both the agent and the user side.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// refFixture wraps proposeFixture with a seeded todo task assigned to
// no one, so the agent can claim it by number.
type refFixture struct {
	*proposeFixture
	taskID     string
	taskNumber int
}

func newRefFixture(t *testing.T) *refFixture {
	t.Helper()
	fx := newProposeFixture(t)
	// Create through the user-side endpoint so the whole path
	// (handler → repo → number assignment) is exercised.
	rr := fx.doWithCookie(t, http.MethodPost, "/api/v1/projects/"+fx.projectID+"/tasks", map[string]any{
		"title":     "Resolve me",
		"column_id": fx.todoColID,
	})
	require.Equal(t, http.StatusCreated, rr.Code, "create body=%s", rr.Body.String())
	var created task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &created))
	require.Greater(t, created.Number, 0, "create response must carry the number")
	return &refFixture{proposeFixture: fx, taskID: created.ID, taskNumber: created.Number}
}

// agentDo issues a request under the agent bearer token. All current
// callers are body-less (claim/context/list), so the helper stays
// body-free; add a body parameter when a test needs one.
func (f *refFixture) agentDo(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

// TestAgentRef_ClaimContextByNumber: "T<N>" resolves on the claim and
// context routes; the responses carry the task's number.
func TestAgentRef_ClaimContextByNumber(t *testing.T) {
	t.Parallel()
	fx := newRefFixture(t)
	ref := fmt.Sprintf("T%d", fx.taskNumber)

	// Claim by "T<N>".
	rr := fx.agentDo(t, http.MethodPost, "/api/v1/agent/tasks/"+ref+"/claim")
	require.Equal(t, http.StatusOK, rr.Code, "claim by %q: body=%s", ref, rr.Body.String())
	var claimed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &claimed))
	assert.Equal(t, fx.taskID, claimed.ID, "T<N> must resolve to the same task")
	assert.Equal(t, fx.taskNumber, claimed.Number, "task JSON must carry number")

	// Context by "t<N>" (case-insensitive).
	rr = fx.agentDo(t, http.MethodGet,
		fmt.Sprintf("/api/v1/agent/tasks/t%d/context", fx.taskNumber))
	require.Equal(t, http.StatusOK, rr.Code, "context by T<N>: body=%s", rr.Body.String())
	var snap struct {
		Task task.Task `json:"task"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &snap))
	assert.Equal(t, fx.taskID, snap.Task.ID)
	assert.Equal(t, fx.taskNumber, snap.Task.Number)
}

// TestAgentRef_UnknownNumber404: an unknown T-ref is a 404 with the
// explicit "task TN not found" message (not a bare not_found).
// Legacy forms "#N" and bare "N" also 404 (cutover).
func TestAgentRef_UnknownNumber404(t *testing.T) {
	t.Parallel()
	fx := newRefFixture(t)

	cases := []struct{ name, ref string }{
		{"T form", "T999999"},
		{"t form", "t999999"},
		{"legacy hash", "#999999"},
		{"legacy bare", "999999"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := fx.agentDo(t, http.MethodPost, "/api/v1/agent/tasks/"+tc.ref+"/claim")
			require.Equal(t, http.StatusNotFound, rr.Code, "body=%s", rr.Body.String())
			// T-form error names the ref; legacy forms fall through to not_found
			// (they don't match T/N parsing and go to UUID lookup, which returns
			// generic not_found).
			if tc.name == "T form" || tc.name == "t form" {
				assert.Contains(t, rr.Body.String(), "task "+tc.ref+" not found")
			}
		})
	}

	// Context route too (resolves through the same helper).
	rr := fx.agentDo(t, http.MethodGet, "/api/v1/agent/tasks/T999999/context")
	require.Equal(t, http.StatusNotFound, rr.Code)
	assert.Contains(t, rr.Body.String(), "task T999999 not found")
}

// TestAgentRef_UUIDStillWorks: the UUID path is untouched — claim by
// id, and a UUID is never mistaken for a number.
func TestAgentRef_UUIDStillWorks(t *testing.T) {
	t.Parallel()
	fx := newRefFixture(t)

	rr := fx.agentDo(t, http.MethodPost, "/api/v1/agent/tasks/"+fx.taskID+"/claim")
	require.Equal(t, http.StatusOK, rr.Code, "claim by UUID: body=%s", rr.Body.String())
	var claimed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &claimed))
	assert.Equal(t, fx.taskID, claimed.ID)

	// Unknown UUID → plain 404 (no "task TN" message).
	rr = fx.agentDo(t, http.MethodPost,
		"/api/v1/agent/tasks/00000000-0000-0000-0000-000000000000/claim")
	require.Equal(t, http.StatusNotFound, rr.Code)
	assert.NotContains(t, rr.Body.String(), "task T")
}

// TestAgentRef_ListCarriesNumber: GET /api/v1/agent/tasks (the surface
// behind `orenda agent next` and MCP orenda_list_tasks) includes the
// number on every row.
func TestAgentRef_ListCarriesNumber(t *testing.T) {
	t.Parallel()
	fx := newRefFixture(t)

	rr := fx.agentDo(t, http.MethodGet, "/api/v1/agent/tasks?ready=true")
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var resp struct {
		Tasks []struct {
			Task task.Task `json:"task"`
		} `json:"tasks"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	found := false
	for _, row := range resp.Tasks {
		if row.Task.ID == fx.taskID {
			found = true
			assert.Equal(t, fx.taskNumber, row.Task.Number)
		}
	}
	assert.True(t, found, "seeded task must appear in the agent list")
}

// TestUserRef_GetTaskByNumber: the user-side lookup accepts T-prefixed
// refs too (GET /api/v1/tasks/{id}).
func TestUserRef_GetTaskByNumber(t *testing.T) {
	t.Parallel()
	fx := newRefFixture(t)

	rr := fx.doWithCookie(t, http.MethodGet,
		fmt.Sprintf("/api/v1/tasks/T%d", fx.taskNumber), nil)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var tr task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &tr))
	assert.Equal(t, fx.taskID, tr.ID)
	assert.Equal(t, fx.taskNumber, tr.Number)

	// Bare number no longer resolves — falls through to UUID lookup.
	rr = fx.doWithCookie(t, http.MethodGet, "/api/v1/tasks/424242", nil)
	require.Equal(t, http.StatusNotFound, rr.Code)
	assert.Contains(t, rr.Body.String(), "not_found")
}

// TestServiceResolve_RefShapes: the service-level resolver pins the
// domain parsing contract end-to-end against a real DB.
func TestServiceResolve_RefShapes(t *testing.T) {
	t.Parallel()
	fx := newRefFixture(t)
	svc := taskservice.New(sqlite.NewTaskRepository(fx.db), nil, nil, nil, nil)

	ctx := context.Background()
	byT, err := svc.Resolve(ctx, fmt.Sprintf("T%d", fx.taskNumber))
	require.NoError(t, err)
	assert.Equal(t, fx.taskID, byT.ID)

	byLowerT, err := svc.Resolve(ctx, fmt.Sprintf("t%d", fx.taskNumber))
	require.NoError(t, err)
	assert.Equal(t, fx.taskID, byLowerT.ID)

	byID, err := svc.Resolve(ctx, fx.taskID)
	require.NoError(t, err)
	assert.Equal(t, fx.taskNumber, byID.Number)

	_, err = svc.Resolve(ctx, "T424242")
	require.Error(t, err)
	var refErr *task.RefNotFoundError
	require.ErrorAs(t, err, &refErr)
	assert.Equal(t, "task T424242 not found", refErr.Error())
	assert.ErrorIs(t, err, task.ErrNotFound, "RefNotFoundError must match ErrNotFound")

	// Legacy forms are rejected (not T-prefixed).
	_, err = svc.Resolve(ctx, "#424242")
	require.Error(t, err)
	assert.NotErrorAs(t, err, &refErr, "#N should not produce RefNotFoundError")

	_, err = svc.Resolve(ctx, "424242")
	require.Error(t, err)
}
