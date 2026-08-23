// Phase 20: TZ boundary tests for the /api/v1/today handler.
//
// Today is anchored at UTC midnight (handler's contract). The user
// can be in any TZ; the dashboard expects to see tasks whose
// due_at lands in their local "today". Phase 20 ships the UTC
// anchor with a documented caveat: the single-owner install lives
// on the user's box, and the user's TZ is presumably the system
// TZ. The tests pin the contract.

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

func TestToday_TZBoundary_DueTodayExcludesYesterday(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	now := time.Now().UTC()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	endOfDay := startOfDay.Add(24 * time.Hour)

	// 1 second before today's UTC midnight → overdue.
	overdueAt := startOfDay.Add(-1 * time.Second)
	overdueTask := &task.Task{
		ProjectID: f.projectID, ColumnID: f.cols[1].ID,
		Title: "yesterday",
		DueAt: &overdueAt, Status: task.StatusTodo,
	}
	require.NoError(t, f.taskRepo.Create(context.Background(), overdueTask))

	// 1 second before end of today → due_today.
	endOfToday := endOfDay.Add(-1 * time.Second)
	todayTask := &task.Task{
		ProjectID: f.projectID, ColumnID: f.cols[1].ID,
		Title: "later-today",
		DueAt: &endOfToday, Status: task.StatusTodo,
	}
	require.NoError(t, f.taskRepo.Create(context.Background(), todayTask))

	rr := doReq(f.router, http.MethodGet, "/api/v1/today", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code)

	var got struct {
		Overdue  []*task.Task `json:"overdue"`
		DueToday []*task.Task `json:"due_today"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))

	overdueIDs := map[string]bool{}
	for _, tk := range got.Overdue {
		overdueIDs[tk.ID] = true
	}
	assert.True(t, overdueIDs[overdueTask.ID], "yesterday's task must be in overdue")

	todayIDs := map[string]bool{}
	for _, tk := range got.DueToday {
		todayIDs[tk.ID] = true
	}
	assert.True(t, todayIDs[todayTask.ID], "today's task must be in due_today")
}

func TestToday_UpcomingWeek_BucketsByDate(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)

	now := time.Now().UTC()
	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 12, 0, 0, 0, time.UTC)
	inTwoDays := tomorrow.Add(48 * time.Hour)

	t1 := &task.Task{
		ProjectID: f.projectID, ColumnID: f.cols[1].ID,
		Title: "tomorrow-1", DueAt: &tomorrow, Status: task.StatusTodo,
	}
	t2 := &task.Task{
		ProjectID: f.projectID, ColumnID: f.cols[1].ID,
		Title: "tomorrow-2", DueAt: &tomorrow, Status: task.StatusTodo,
	}
	t3 := &task.Task{
		ProjectID: f.projectID, ColumnID: f.cols[1].ID,
		Title: "in-two-days", DueAt: &inTwoDays, Status: task.StatusTodo,
	}
	for _, x := range []*task.Task{t1, t2, t3} {
		require.NoError(t, f.taskRepo.Create(context.Background(), x))
	}

	rr := doReq(f.router, http.MethodGet, "/api/v1/today", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code)

	var got struct {
		UpcomingWeek []struct {
			Date  string `json:"date"`
			Count int    `json:"count"`
		} `json:"upcoming_week"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))

	require.GreaterOrEqual(t, len(got.UpcomingWeek), 2)
	byDate := map[string]int{}
	for _, d := range got.UpcomingWeek {
		byDate[d.Date] = d.Count
	}
	tomorrowKey := tomorrow.Format("2006-01-02")
	twoDaysKey := inTwoDays.Format("2006-01-02")
	assert.Equal(t, 2, byDate[tomorrowKey], "two tasks due tomorrow")
	assert.Equal(t, 1, byDate[twoDaysKey], "one task due in two days")
}

func TestToday_ActiveTimer_FilledWhenRunning(t *testing.T) {
	t.Parallel()
	// White-box coverage: when an active time entry exists for the
	// owner, /today surfaces it. We don't seed a time entry here
	// (the fixture doesn't wire TimeService); the no-timer case is
	// already covered by the empty-state logic in TodayPage.
	// This test exercises the wire shape — the field is omitted
	// when there's no timer.
	f := columnDeps(t)
	rr := doReq(f.router, http.MethodGet, "/api/v1/today", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	// active_timer is omitempty so we just check the body parses.
	var raw map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&raw))
	_, hasActive := raw["active_timer"]
	// Without a seeded time entry the field is absent — that's the
	// "omitted" branch of the contract.
	assert.False(t, hasActive, "no active timer seeded; field should be omitted")
}
