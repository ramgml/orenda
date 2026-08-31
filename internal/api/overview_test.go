// Task 107: tests for GET /api/v1/overview — the Dashboard payload.
//
// Pins the observable contract:
//   - 200 with entity counts (projects / tasks by status / wiki pages)
//   - activity series covers the last 30 days, oldest first, zero
//     days included, created/completed buckets counted
//   - task status counts reflect seeded data

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
)

// overviewPayload mirrors the wire shape we assert on.
type overviewPayload struct {
	Projects      int            `json:"projects"`
	TasksByStatus map[string]int `json:"tasks_by_status"`
	WikiPages     int            `json:"wiki_pages"`
	Events        int            `json:"events"`
	Activity      []struct {
		Date      string `json:"date"`
		Created   int    `json:"created"`
		Completed int    `json:"completed"`
	} `json:"activity"`
}

func getOverview(t *testing.T, f colFixtures) overviewPayload {
	t.Helper()
	rr := doReq(f.router, http.MethodGet, "/api/v1/overview", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var got overviewPayload
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	return got
}

func TestOverview_CountsTasksByStatus(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)

	now := time.Now().UTC()
	doneAt := now.Add(-time.Hour)
	for _, tc := range []struct {
		title  string
		status task.Status
	}{
		{"open one", task.StatusTodo},
		{"open two", task.StatusTodo},
		{"finished", task.StatusDone},
	} {
		tk := &task.Task{ProjectID: f.projectID, ColumnID: f.cols[1].ID, Title: tc.title, Status: tc.status}
		if tc.status == task.StatusDone {
			tk.CompletedAt = &doneAt
		}
		require.NoError(t, f.taskRepo.Create(context.Background(), tk))
	}

	got := getOverview(t, f)
	assert.Equal(t, 2, got.TasksByStatus["todo"])
	assert.Equal(t, 1, got.TasksByStatus["done"])
}

func TestOverview_ActivitySeriesShape(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)

	now := time.Now().UTC()
	require.NoError(t, f.taskRepo.Create(context.Background(), &task.Task{
		ProjectID: f.projectID, ColumnID: f.cols[1].ID, Title: "fresh", Status: task.StatusTodo,
		CreatedAt: now,
	}))
	doneAt := now.Add(-24 * time.Hour)
	require.NoError(t, f.taskRepo.Create(context.Background(), &task.Task{
		ProjectID: f.projectID, ColumnID: f.cols[1].ID, Title: "done yesterday", Status: task.StatusDone,
		CreatedAt: doneAt, CompletedAt: &doneAt,
	}))
	got := getOverview(t, f)
	// created_at/completed_at round-trip through the DB (created_at
	// is stamped by the storage layer), so assert bucket totals.
	createdTotal, completedTotal := 0, 0
	for _, day := range got.Activity {
		createdTotal += day.Created
		completedTotal += day.Completed
	}
	assert.Equal(t, 2, createdTotal, "both created tasks fall inside the 30-day window")
	assert.Equal(t, 1, completedTotal, "the done task counts once as completed")
	// 30 contiguous day buckets, oldest first.
	require.Len(t, got.Activity, 30)
	for i := 1; i < len(got.Activity); i++ {
		prev, _ := time.Parse("2006-01-02", got.Activity[i-1].Date)
		cur, _ := time.Parse("2006-01-02", got.Activity[i].Date)
		assert.Equal(t, 24*time.Hour, cur.Sub(prev), "days must be contiguous")
	}
}
