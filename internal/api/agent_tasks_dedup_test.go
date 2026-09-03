package api_test

// T151: GET /api/v1/agent/tasks duplicated every inbox task (each id
// twice). The handler merges two queries — the claimable surface
// (status=todo, assignee agent-or-NULL, all projects incl. none) and
// the inbox listing (project IS NULL, status=todo, any assignee).
// An inbox todo task with assignee NULL matched BOTH, and the merge
// appended raw. The fix dedups by id (first occurrence wins).
//
// Reproduction per the ticket: seed 2 inbox todo tasks (project_id
// IS NULL, assignee NULL) → before the fix count doubled with every
// id twice; after the fix each task appears exactly once.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
)

type listRow struct {
	Task struct {
		ID        string `json:"id"`
		ProjectID string `json:"project_id"`
		Title     string `json:"title"`
	} `json:"task"`
}

func fetchAgentListFor(t *testing.T, fx *refFixture, query string) ([]listRow, int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/tasks"+query, nil)
	req.Header.Set("Authorization", "Bearer "+fx.token)
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var resp struct {
		Tasks []listRow `json:"tasks"`
		Count int       `json:"count"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	return resp.Tasks, resp.Count
}

// TestAgentList_InboxTasksAppearOnce: the full list (no query) shows
// each inbox task exactly once (T151 regression).
func TestAgentList_InboxTasksAppearOnce(t *testing.T) {
	t.Parallel()
	fx := newListFixture(t)

	// Two inbox todo tasks, unassigned — both queries matched them
	// before the fix.
	require.NoError(t, seedInboxTodo(t, fx, "Inbox A"))
	require.NoError(t, seedInboxTodo(t, fx, "Inbox B"))

	tasks, count := fetchAgentListFor(t, fx, "")

	seen := map[string]int{}
	for _, row := range tasks {
		if row.Task.ProjectID == "" {
			seen[row.Task.ID]++
		}
	}
	// Two inbox tasks + the fixture's seeded project task.
	require.Len(t, tasks, 3, "body must carry three rows")
	assert.Equal(t, 3, count)
	for id, n := range seen {
		assert.Equal(t, 1, n, "inbox task %s appeared %d times", id, n)
	}
}

// TestAgentList_ReadyInboxTasksAppearOnce: same check under
// ?ready=true, the shape `orenda agent next` uses.
func TestAgentList_ReadyInboxTasksAppearOnce(t *testing.T) {
	t.Parallel()
	fx := newListFixture(t)
	require.NoError(t, seedInboxTodo(t, fx, "Inbox A"))
	require.NoError(t, seedInboxTodo(t, fx, "Inbox B"))

	tasks, count := fetchAgentListFor(t, fx, "?ready=true")

	seen := map[string]int{}
	for _, row := range tasks {
		if row.Task.ProjectID == "" {
			seen[row.Task.ID]++
		}
	}
	require.Len(t, tasks, 3)
	assert.Equal(t, 3, count)
	for id, n := range seen {
		assert.Equal(t, 1, n, "inbox task %s appeared %d times", id, n)
	}
}

// TestAgentList_ProjectTasksUnaffected: the fixture's project task
// stays on the list exactly once.
func TestAgentList_ProjectTasksUnaffected(t *testing.T) {
	t.Parallel()
	fx := newListFixture(t)

	tasks, count := fetchAgentListFor(t, fx, "")

	require.Len(t, tasks, 1, "only the seeded project task")
	assert.Equal(t, 1, count)
	assert.Equal(t, fx.taskID, tasks[0].Task.ID)
}

// newListFixture builds the standard ref fixture (project with default
// columns, agent token, one seeded project todo task).
func newListFixture(t *testing.T) *refFixture {
	t.Helper()
	return newRefFixture(t)
}

// seedInboxTodo inserts an inbox todo task (project_id IS NULL,
// assignee NULL, no column) the way the incident report reproduced
// the duplication.
func seedInboxTodo(t *testing.T, fx *refFixture, title string) error {
	t.Helper()
	return fx.tasks.Create(context.Background(), &task.Task{
		Title:     title,
		ProjectID: "",
		Status:    task.StatusTodo,
	})
}
