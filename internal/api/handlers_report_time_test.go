package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// newReportFixture builds a router with the agent fixture's full wiring
// (users + agents + tasks + time service) and seeds one project/task so
// manual entries have a real task to point at. Returns the fixture plus
// the seeded task id.
func newReportFixture(t *testing.T) (*agentFixture, string) {
	t.Helper()
	fx := newAgentFixture(t)

	row := fx.db.QueryRow("SELECT id FROM users LIMIT 1")
	var ownerID string
	require.NoError(t, row.Scan(&ownerID))
	projects := sqlite.NewProjectRepository(fx.db)
	p, _, cols, err := projects.CreateProject(context.Background(), &project.Project{
		Name: "ReportTest", OwnerID: ownerID,
	})
	require.NoError(t, err)
	tasks := sqlite.NewTaskRepository(fx.db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "reported"}
	require.NoError(t, tasks.Create(context.Background(), tr))
	return fx, tr.ID
}

// reportSessionToken logs the fixture owner in and returns the session
// cookie value.
func reportSessionToken(t *testing.T, fx *agentFixture) string {
	t.Helper()
	row := fx.db.QueryRow("SELECT email FROM users LIMIT 1")
	var email string
	require.NoError(t, row.Scan(&email))
	body, _ := json.Marshal(map[string]string{"email": email, "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "login: %s", rr.Body.String())
	for _, c := range rr.Result().Cookies() {
		if c.Name == "orenda_session" {
			return c.Value
		}
	}
	t.Fatal("no orenda_session cookie")
	return ""
}

// seedManualEntry posts a manual interval attributed to agentID via the
// user-side endpoint (POST /tasks/:id/time). start/end are RFC3339.
func seedManualEntry(t *testing.T, fx *agentFixture, cookie, taskID, agentID, start, end string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"agent_id": agentID,
		"start_at": start,
		"end_at":   end,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+"/time", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, "seed entry: %s", rr.Body.String())
}

// reportGet issues an authenticated GET with an optional query string.
func reportGet(fx *agentFixture, cookie, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/reports/time"+query, nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	return rr
}

// reportRow mirrors the report JSON the handler emits.
type reportRow struct {
	AgentID  string `json:"agent_id"`
	TotalSec int64  `json:"total_sec"`
	Tasks    []struct {
		TaskID   string `json:"task_id"`
		TotalSec int64  `json:"total_sec"`
	} `json:"tasks"`
}

// Task 98: /reports/time without agent_id must include entries logged
// by agents (agent_id = agent UUID), not just the viewer's user id.
func TestReportTime_NoAgentID_ShowsAllActors(t *testing.T) {
	t.Parallel()
	fx, taskID := newReportFixture(t)
	cookie := reportSessionToken(t, fx)

	start := time.Now().Add(-time.Hour).Truncate(time.Second).UTC().Format(time.RFC3339)
	end := time.Now().Add(-30 * time.Minute).Truncate(time.Second).UTC().Format(time.RFC3339)
	seedManualEntry(t, fx, cookie, taskID, fx.agentID, start, end)

	rr := reportGet(fx, cookie, "?from="+start+"&to="+end)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())

	var rep reportRow
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &rep))
	assert.NotEmpty(t, rep.Tasks, "report must show agent-attributed entries without agent_id")
	assert.Equal(t, int64(1800), rep.TotalSec)
	assert.Equal(t, taskID, rep.Tasks[0].TaskID)
}

// Task 98: an explicit ?agent_id= keeps filtering to that one actor.
func TestReportTime_ExplicitAgentID_Filters(t *testing.T) {
	t.Parallel()
	fx, taskID := newReportFixture(t)
	cookie := reportSessionToken(t, fx)

	start := time.Now().Add(-time.Hour).Truncate(time.Second).UTC().Format(time.RFC3339)
	end := time.Now().Add(-30 * time.Minute).Truncate(time.Second).UTC().Format(time.RFC3339)
	seedManualEntry(t, fx, cookie, taskID, fx.agentID, start, end)
	// A second actor (user-id-shaped) the filter must exclude.
	other := "00000000-0000-7000-8000-0000000000aa"
	seedManualEntry(t, fx, cookie, taskID, other, start, end)

	rr := reportGet(fx, cookie, "?agent_id="+fx.agentID+"&from="+start+"&to="+end)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())

	var rep reportRow
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &rep))
	assert.Equal(t, fx.agentID, rep.AgentID)
	assert.Equal(t, int64(1800), rep.TotalSec, "only the requested actor's entry")
}

// Task 98: the handler no longer falls back to the viewer's user id,
// and the route stays behind auth — anonymous request is 401.
func TestReportTime_RequiresAuth(t *testing.T) {
	t.Parallel()
	fx, _ := newReportFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/reports/time", nil)
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}
