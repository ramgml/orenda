package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/timeentry"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// T339: the single-task GET carries counters.timer_running — the
// same open-entry signal the kanban list aggregation produces — so
// the task view's time badge sees live timer state without a
// follow-up call.
//
//	GET with an open time entry    → counters.timer_running = true
//	GET with only a closed entry   → false
//	GET with no entries            → counters zero-valued, false
func TestTaskGet_TimerRunning(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	ctx := context.Background()
	require.NotNil(t, f.taskRepo, "fixture must expose the task repo")

	// Create three tasks in the fixture project.
	ids := map[string]string{}
	for _, name := range []string{"open", "closed", "none"} {
		tr := &task.Task{ProjectID: f.projectID, ColumnID: f.cols[0].ID, Title: name}
		require.NoError(t, f.taskRepo.Create(ctx, tr))
		ids[name] = tr.ID
	}

	// An agent row + one open and one closed time entry. The agent
	// is seeded directly through the repo — the timer flag doesn't
	// care how the row got there, only that agent_id references it.
	tokens := sqlite.NewAPITokenRepository(f.db)
	tok, err := tokens.Create(ctx, f.ownerID, "t339", "fake", "[]", nil)
	require.NoError(t, err)
	a := &agent.Agent{Name: "t339-http-" + tok.ID[:6], TokenID: tok.ID}
	require.NoError(t, sqlite.NewAgentRepository(f.db).Create(ctx, a))

	timeRepo := sqlite.NewTimeEntryRepository(f.db)
	now := time.Now().Truncate(time.Second)
	_, err = timeRepo.Create(ctx, &timeentry.TimeEntry{
		TaskID: ids["open"], AgentID: a.ID, StartedAt: now, Source: timeentry.SourceTimer,
	})
	require.NoError(t, err)
	done := now.Add(-time.Hour)
	_, err = timeRepo.Create(ctx, &timeentry.TimeEntry{
		TaskID: ids["closed"], AgentID: a.ID, StartedAt: done, EndedAt: &now, DurationS: &[]int64{3600}[0],
	})
	require.NoError(t, err)

	rr := doReq(f.router, http.MethodGet, "/api/v1/tasks/"+ids["open"], f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var got task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	require.NotNil(t, got.Counters, "single GET must carry the counters bundle (T339)")
	assert.True(t, got.Counters.TimerRunning, "open entry ⇒ timer_running")

	rr = doReq(f.router, http.MethodGet, "/api/v1/tasks/"+ids["closed"], f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	require.NotNil(t, got.Counters)
	assert.False(t, got.Counters.TimerRunning, "closed entry ⇒ no pulse")

	rr = doReq(f.router, http.MethodGet, "/api/v1/tasks/"+ids["none"], f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	require.NotNil(t, got.Counters)
	assert.False(t, got.Counters.TimerRunning)
	assert.Equal(t, 0, got.Counters.Comments)
}
