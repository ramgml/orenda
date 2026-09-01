package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
)

// T120: the time estimate round-trips through the task API. The
// column has existed since 001_init.sql (no migration), so these
// tests pin the request-side contract instead:
//
//	create with time_estimate_s       → 201, estimate echoed back
//	PATCH with time_estimate_s        → estimate updated
//	PATCH with time_estimate_s: 0     → estimate cleared (null in
//	                                    the response; 0 is the clear
//	                                    sentinel per the due_at
//	                                    empty-string convention —
//	                                    a zero-second estimate is
//	                                    meaningless)
//	PATCH without the field           → estimate untouched
func TestTaskTimeEstimate(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)

	// 1) Create with an estimate: 201 and the value comes back.
	rr := doReq(f.router, http.MethodPost, "/api/v1/projects/"+f.projectID+"/tasks", f.cookie, map[string]any{
		"title":           "estimated",
		"time_estimate_s": 5400,
	})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var created task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&created))
	require.NotNil(t, created.TimeEstimateS, "created task must carry the estimate")
	assert.Equal(t, 5400, *created.TimeEstimateS)
	taskID := created.ID

	// 2) PATCH a new value: updated in the response.
	rr = doReq(f.router, http.MethodPatch, "/api/v1/tasks/"+taskID, f.cookie, map[string]any{
		"time_estimate_s": 90 * 60,
	})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var patched task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&patched))
	require.NotNil(t, patched.TimeEstimateS, "PATCH must update the estimate")
	assert.Equal(t, 5400, *patched.TimeEstimateS)

	// 3) PATCH time_estimate_s: 0 → cleared (nil in the response).
	rr = doReq(f.router, http.MethodPatch, "/api/v1/tasks/"+taskID, f.cookie, map[string]any{
		"time_estimate_s": 0,
	})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var cleared task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&cleared))
	assert.Nil(t, cleared.TimeEstimateS, "estimate 0 must clear the field, not store 0")

	// 4) PATCH without the field: the stored value stays cleared
	//    (absent = no intent). Re-set first so the noop is provable.
	rr = doReq(f.router, http.MethodPatch, "/api/v1/tasks/"+taskID, f.cookie, map[string]any{
		"time_estimate_s": 1800,
	})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	rr = doReq(f.router, http.MethodPatch, "/api/v1/tasks/"+taskID, f.cookie, map[string]any{
		"title": "renamed only",
	})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var untouched task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&untouched))
	require.NotNil(t, untouched.TimeEstimateS, "PATCH without time_estimate_s must not touch the estimate")
	assert.Equal(t, 1800, *untouched.TimeEstimateS)
}
