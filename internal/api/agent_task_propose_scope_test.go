package api_test

// Task 419: agent-project scope gate on propose — POST /api/v1/agent/tasks.
//
// Task 140 made closed projects (agents_allowed = 0, no grant row)
// invisible to agents and refused claim with 422 not_in_scope, but
// propose kept the old behavior: any authenticated agent could drop a
// task into a project it cannot even see. These tests pin the
// symmetric gate:
//
//   - closed project without a grant  → 422 {"error":"not_in_scope"},
//   - closed project WITH a grant     → 201 created,
//   - open project (agents_allowed=1) → 201 created,
//   - closed project addressed by its "P<N>" ref → 422: the gate runs
//     on the RESOLVED UUID (validateAgentProposeTask resolves the
//     P-ref before the scope check), so the check looks at the same
//     row claim would.
//
// Unknown projects keep the existing 404 (TestAgent_ProposeTask_UnknownProject)
// — resolution fails before the gate runs. project_id is mandatory on
// propose, so there is no inbox path through this handler; the
// empty-project_id validation shape is pinned unchanged in the last
// subtest. No master bypass: the gate is symmetric with claim.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
)

// createScopeProject seeds a project owned by the fixture owner with
// an explicit agents_allowed value (fresh projects default to closed,
// but the tests pin the flag explicitly).
func (f *proposeFixture) createScopeProject(t *testing.T, name string, agentsAllowed bool) *project.Project {
	t.Helper()
	p, _, _, err := f.projects.CreateProject(context.Background(), &project.Project{
		Name:          name,
		OwnerID:       f.ownerID,
		AgentsAllowed: agentsAllowed,
	})
	require.NoError(t, err)
	return p
}

// grantScopeAgent inserts a project_agents row directly — the same
// seeding shape accessFixture.grantAgent uses for the claim-side
// scope tests.
func (f *proposeFixture) grantScopeAgent(t *testing.T, projectID, agentID string) {
	t.Helper()
	_, err := f.db.ExecContext(context.Background(),
		`INSERT INTO project_agents (project_id, agent_id, added_by) VALUES (?, ?, ?)`,
		projectID, agentID, f.ownerID)
	require.NoError(t, err)
}

// TestAgent_ProposeTask_ScopeGate pins the Task 140 project-scope gate
// on the propose path (Task 419), symmetric with claim.
func TestAgent_ProposeTask_ScopeGate(t *testing.T) {
	t.Parallel()

	t.Run("closed project without grant refused", func(t *testing.T) {
		t.Parallel()
		f := newProposeFixture(t)
		closed := f.createScopeProject(t, "scope-closed", false)

		rr := f.proposeAsAgent(t, validProposeBody(closed.ID))
		require.Equal(t, http.StatusUnprocessableEntity, rr.Code, "body=%s", rr.Body.String())
		assert.JSONEq(t, `{"error":"not_in_scope"}`, rr.Body.String())
	})

	t.Run("closed project with grant accepted", func(t *testing.T) {
		t.Parallel()
		f := newProposeFixture(t)
		granted := f.createScopeProject(t, "scope-granted", false)
		f.grantScopeAgent(t, granted.ID, f.agentID)

		rr := f.proposeAsAgent(t, validProposeBody(granted.ID))
		require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())

		var got task.Task
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
		assert.Equal(t, granted.ID, got.ProjectID)
		assert.Equal(t, task.StatusBacklog, got.Status, "propose must still land in backlog")
	})

	t.Run("open project accepted", func(t *testing.T) {
		t.Parallel()
		f := newProposeFixture(t)
		open := f.createScopeProject(t, "scope-open", true)

		rr := f.proposeAsAgent(t, validProposeBody(open.ID))
		require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())

		var got task.Task
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
		assert.Equal(t, open.ID, got.ProjectID)
	})

	t.Run("closed project via P-ref refused after resolution", func(t *testing.T) {
		t.Parallel()
		f := newProposeFixture(t)
		closed := f.createScopeProject(t, "scope-closed-pref", false)

		// The gate must fire on the resolved UUID — validateAgentProposeTask
		// resolves "P<N>" before the scope check — so a closed project
		// addressed by its P-ref is refused exactly like by UUID.
		rr := f.proposeAsAgent(t, validProposeBody(fmt.Sprintf("P%d", closed.Number)))
		require.Equal(t, http.StatusUnprocessableEntity, rr.Code, "body=%s", rr.Body.String())
		assert.JSONEq(t, `{"error":"not_in_scope"}`, rr.Body.String())
	})

	t.Run("missing project_id still invalid_input", func(t *testing.T) {
		t.Parallel()
		// project_id is mandatory on propose — there is no inbox path
		// through this handler. The empty-project_id validation shape
		// stays untouched by the gate.
		f := newProposeFixture(t)
		rr := f.proposeAsAgent(t, map[string]any{"title": "t", "description_md": "d"})
		assert.Equal(t, http.StatusBadRequest, rr.Code, "body=%s", rr.Body.String())
	})
}
