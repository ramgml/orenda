package api_test

// Task 416: time_estimate_s on the agent write-surface.
//
// The T120 estimate (int seconds, sentinel 0 = clear) existed only on
// the user surface; agents could not set it anywhere. These tests pin
// the restored contract:
//   - propose carries an explicit estimate (echo + DB round-trip);
//     absent and explicit 0 both store NULL (on create "unset" is the
//     only sensible zero);
//   - PATCH sets/updates the estimate on own un-triaged proposal and
//     no longer 400s no_patch_fields for an estimate-only patch;
//   - PATCH without the field leaves the stored value untouched;
//   - explicit 0 clears (NULL);
//   - the holder path rejects the field with 400 proposal_gated_field
//     (UpdateHeldFields writes title/description/notes only — without
//     the guard the patch would be silently dropped while the diff
//     claimed a change, the Task 115 F2 class);
//   - agent_notes mixed with the estimate stays 400
//     agent_notes_requires_holder_only;
//   - a foreign agent PATCHing the estimate stays 403
//     not_your_proposal (estimate joins the proposal-gated group, it
//     does not bypass the gate).

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
)

// dbEstimateOf reads tasks.time_estimate_s straight from the DB so
// the sentinel (NULL storage) is pinned, not just the JSON echo.
func dbEstimateOf(t *testing.T, f *proposeFixture, id string) *int {
	t.Helper()
	var est *int
	require.NoError(t, f.db.QueryRow(
		"SELECT time_estimate_s FROM tasks WHERE id = ?", id).Scan(&est))
	return est
}

// lastUpdatePayload returns the payload of the newest task.updated
// activity row for the task (the compact single-row field diff).
func lastUpdatePayload(t *testing.T, f *proposeFixture, id string) string {
	t.Helper()
	var payload *string
	require.NoError(t, f.db.QueryRow(
		"SELECT payload FROM task_activity WHERE task_id = ? AND action = 'task.updated' "+
			"ORDER BY created_at DESC, id DESC LIMIT 1", id).Scan(&payload))
	require.NotNil(t, payload, "task.updated activity row must exist")
	return *payload
}

func TestAgent_ProposeCarriesTimeEstimate(t *testing.T) {
	t.Parallel()
	f := newProposeFixture(t)

	body := validProposeBody(f.projectID)
	body["time_estimate_s"] = 1800
	rr := f.proposeAsAgent(t, body)
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))
	require.NotNil(t, proposed.TimeEstimateS)
	assert.Equal(t, 1800, *proposed.TimeEstimateS)
	require.NotNil(t, dbEstimateOf(t, f, proposed.ID))
	assert.Equal(t, 1800, *dbEstimateOf(t, f, proposed.ID))

	// Absent field → NULL (unset), no echo.
	rr = f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var plain task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &plain))
	assert.Nil(t, plain.TimeEstimateS)
	assert.Nil(t, dbEstimateOf(t, f, plain.ID))

	// Explicit 0 on CREATE is stored unset — a zero-second estimate
	// is meaningless (the T120 sentinel); on a fresh proposal there
	// is no meaningful difference from "absent".
	body = validProposeBody(f.projectID)
	body["time_estimate_s"] = 0
	rr = f.proposeAsAgent(t, body)
	require.Equal(t, http.StatusCreated, rr.Code)
	var zeroed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &zeroed))
	assert.Nil(t, zeroed.TimeEstimateS)
	assert.Nil(t, dbEstimateOf(t, f, zeroed.ID))
}

func TestAgent_PatchEstimate_RoundTrip(t *testing.T) {
	t.Parallel()
	f := newProposeFixture(t)
	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))

	// Set on the un-triaged proposal.
	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"time_estimate_s": 3600})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var updated task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &updated))
	require.NotNil(t, updated.TimeEstimateS)
	assert.Equal(t, 3600, *updated.TimeEstimateS)
	require.NotNil(t, dbEstimateOf(t, f, proposed.ID))
	assert.Equal(t, 3600, *dbEstimateOf(t, f, proposed.ID))

	// Activity audit records the field diff (single compact row).
	assert.Contains(t, lastUpdatePayload(t, f, proposed.ID), "time_estimate_s")

	// Absent field leaves the stored value untouched.
	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"title": "retitled"})
	require.Equal(t, http.StatusOK, rr.Code)
	var retitled task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &retitled))
	assert.Equal(t, "retitled", retitled.Title)
	require.NotNil(t, retitled.TimeEstimateS)
	assert.Equal(t, 3600, *retitled.TimeEstimateS)

	// The T120 sentinel: explicit 0 clears → NULL, absent from echo.
	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"time_estimate_s": 0})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var cleared task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &cleared))
	assert.Nil(t, cleared.TimeEstimateS)
	assert.Nil(t, dbEstimateOf(t, f, proposed.ID))

	// Clearing an already-empty estimate is a no-op 200, not 400
	// (nothing changed; the write is skipped like any no-op patch).
	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"time_estimate_s": 0})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	assert.Nil(t, dbEstimateOf(t, f, proposed.ID))
}

// The #416 complaint itself: an estimate-only PATCH must not bounce
// with 400 no_patch_fields — the field is recognised and writable.
func TestAgent_PatchEstimateOnly_NoNoPatchFields(t *testing.T) {
	t.Parallel()
	f := newProposeFixture(t)
	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))

	rr = f.patchAsAgent(t, proposed.ID, map[string]any{"time_estimate_s": 600})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	require.NotContains(t, rr.Body.String(), "no_patch_fields")
	assert.NotNil(t, dbEstimateOf(t, f, proposed.ID))
}

// The holder path must refuse the field with a dedicated 400:
// UpdateHeldFields cannot write it, so a silent drop would lie in the
// returned diff (Task 115 F2 class).
func TestAgent_PatchEstimate_AsHolder_400ProposalGated(t *testing.T) {
	t.Parallel()
	f := newProposeFixture(t)
	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))

	moveTaskToColumn(t, f, proposed.ID, f.todoColID)
	rr = f.claimAsAgent(t, proposed.ID)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())

	rr = f.patchAsAgent(t, proposed.ID, map[string]any{
		"title":           "held edit",
		"time_estimate_s": 900,
	})
	require.Equal(t, http.StatusBadRequest, rr.Code, "body=%s", rr.Body.String())
	assert.Contains(t, rr.Body.String(), "proposal_gated_field")
	// Nothing leaked: title unchanged too (the whole patch refused).
	rr2 := f.getAsAgentToken(t, proposed.ID, f.token)
	require.Equal(t, http.StatusOK, rr2.Code)
	var after task.Task
	require.NoError(t, json.Unmarshal(rr2.Body.Bytes(), &after))
	assert.Equal(t, proposed.Title, after.Title)
	assert.Nil(t, after.TimeEstimateS)
}

// agent_notes stays holder-only: mixing it with the estimate is a
// caller bug → 400 (estimate joined the owner-scoped exclusion set).
func TestAgent_PatchNotesMixedWithEstimate_400(t *testing.T) {
	t.Parallel()
	f := newProposeFixture(t)
	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var proposed task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proposed))

	rr = f.patchAsAgent(t, proposed.ID, map[string]any{
		"agent_notes":     "notes",
		"time_estimate_s": 600,
	})
	require.Equal(t, http.StatusBadRequest, rr.Code, "body=%s", rr.Body.String())
	assert.Contains(t, rr.Body.String(), "agent_notes_requires_holder_only")
}

// The estimate joins the proposal-gated group — it does not weaken
// the Phase 33.2 permission contract: a foreign agent PATCHing it on
// someone else's task stays 403 not_your_proposal.
func TestAgent_PatchEstimate_ForeignProposal_403(t *testing.T) {
	t.Parallel()
	f := newProposeFixture(t)
	body := map[string]any{
		"project_id":     f.projectID,
		"title":          "User-authored",
		"description_md": "# X",
	}
	rr := f.doWithCookie(t, http.MethodPost, "/api/v1/projects/"+f.projectID+"/tasks", body)
	require.Equal(t, http.StatusCreated, rr.Code)
	var userTask task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &userTask))

	rr = f.patchAsAgent(t, userTask.ID, map[string]any{"time_estimate_s": 600})
	require.Equal(t, http.StatusForbidden, rr.Code, "body=%s", rr.Body.String())
	assert.Contains(t, rr.Body.String(), "not_your_proposal")
}
