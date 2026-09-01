package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
)

// T119: a task created into a column must carry that column's status.
// The web client sends only {title, column_id}; before the fix the
// handler left Status empty and the DB DEFAULT 'todo' won, so a card
// created in the Backlog column came back as todo. The fix mirrors the
// two column→status syncs that already existed: Move lifts
// column.Status onto the task (service/task/move.go), PATCH does
// column→status when column_id is set and status is empty
// (updateTaskHandler). These tests pin the create-path equivalent:
//
//	create with column_id of the backlog column → status=backlog
//	create with an explicit status               → respected (not overwritten)
//	create without a column (inbox)              → old behaviour, untouched
func TestCreateTask_StatusFromColumn(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)

	backlogCol := f.cols[0] // DefaultColumns[0] is "backlog" (name == status)
	require.Equal(t, "backlog", backlogCol.Status)

	// 1) Create into the backlog column with no explicit status:
	//    the task must come back with status=backlog.
	rr := doReq(f.router, http.MethodPost, "/api/v1/projects/"+f.projectID+"/tasks", f.cookie, map[string]any{
		"title":     "into backlog",
		"column_id": backlogCol.ID,
	})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var created task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&created))
	assert.Equal(t, "backlog", string(created.Status), "card created in the backlog column must be backlog, not todo")
	assert.Equal(t, backlogCol.ID, created.ColumnID)

	// 2) An explicit status wins — the column sync only fills the
	//    empty case, it never overwrites what the client sent.
	rr = doReq(f.router, http.MethodPost, "/api/v1/projects/"+f.projectID+"/tasks", f.cookie, map[string]any{
		"title":     "explicit todo",
		"column_id": backlogCol.ID,
		"status":    "todo",
	})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var explicit task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&explicit))
	assert.Equal(t, "todo", string(explicit.Status), "explicit status must be respected")

	// 3) Inbox create (no column) keeps the legacy default: the
	//    status/column pair is untouched by the sync.
	rr = doReq(f.router, http.MethodPost, "/api/v1/inbox/tasks", f.cookie, map[string]any{
		"title": "inbox capture",
	})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var inbox task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&inbox))
	assert.Equal(t, "", inbox.ColumnID)
	assert.Equal(t, "todo", string(inbox.Status), "inbox task keeps the legacy todo default")
}
