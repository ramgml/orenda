package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/timeentry"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// T356 spent fallback on the task read surface: tasks that predate
// the Task 87 auto-timer have time_spent_s == 0 and no time_entries
// rows, yet their in_progress stays are recorded in the
// task.status_changed audit log. When the Spent seam is wired:
//
//	legacy task (no entries)   → derived seconds stamped (virtual)
//	task with entries          → stored counter wins, no stamp
//	task with manual counter   → stored counter wins, no stamp
//
// The stamp never persists: the repo still returns the raw values.
// newSpentFallbackDeps builds the columnDeps fixture with the T356
// Spent seam attached (same wiring as main.go) and the router
// rebuilt around it.
func newSpentFallbackDeps(t *testing.T) colFixtures {
	t.Helper()
	f := columnDeps(t)
	// columnDeps doesn't attach the Spent seam — wire it the same way
	// main.go does (adapter over the sqlite repos) and rebuild the
	// router so the handler sees the seam.
	taskSvc := taskservice.New(
		f.taskRepo,
		sqlite.NewTaskLockRepository(f.db),
		nil, nil, ws.NewHub(),
	)
	taskSvc.Spent = taskservice.SpentFallbackAdapter{
		Statuses: sqlite.NewActivityRepository(f.db).(*sqlite.ActivityRepo),
		Gate:     sqlite.NewTimeEntryRepository(f.db),
	}
	deps := api.Dependencies{
		Logger:      zap.NewNop(),
		Signer:      auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda"),
		Users:       sqlite.NewUserRepository(f.db),
		Projects:    sqlite.NewProjectRepository(f.db),
		Tasks:       f.taskRepo,
		Tokens:      sqlite.NewAPITokenRepository(f.db),
		TaskService: taskSvc,
		WSHub:       ws.NewHub(),
		CookieName:  "orenda_session",
	}
	f.router = api.NewRouter(&deps)
	t.Cleanup(deps.RateLimitClose)
	return f
}

// seedStatusTimeline writes a task.status_changed audit timeline for
// one task: in_progress at from, done at to. Direct SQL because
// activityRepo.Create stamps datetime('now') and the derivation
// needs controlled timestamps.
func seedStatusTimeline(t *testing.T, f colFixtures, taskID string, from, to time.Time) {
	t.Helper()
	ctx := context.Background()
	enter, _ := json.Marshal(map[string]string{"from": "todo", "to": "in_progress"})
	leave, _ := json.Marshal(map[string]string{"from": "in_progress", "to": "done"})
	const layout = "2006-01-02 15:04:05"
	for _, row := range []struct {
		payload string
		at      time.Time
	}{
		{string(enter), from},
		{string(leave), to},
	} {
		_, err := f.db.ExecContext(ctx,
			`INSERT INTO task_activity (id, task_id, actor_type, actor_id, action, payload, created_at)
			 VALUES (?, ?, 'user', ?, 'task.status_changed', ?, ?)`,
			uuid.NewString(), taskID, f.ownerID, row.payload, row.at.UTC().Format(layout))
		require.NoError(t, err)
	}
}

func TestTaskGet_SpentFallback_LegacyTask(t *testing.T) {
	t.Parallel()
	f := newSpentFallbackDeps(t)
	ctx := context.Background()

	tr := &task.Task{ProjectID: f.projectID, ColumnID: f.cols[0].ID, Title: "legacy"}
	require.NoError(t, f.taskRepo.Create(ctx, tr))
	// 1h inside the classic todo→in_progress→done window.
	seedStatusTimeline(t, f, tr.ID, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))

	rr := doReq(f.router, http.MethodGet, "/api/v1/tasks/"+tr.ID, f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var got task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Equal(t, 3600, got.TimeSpentS, "derived from status timeline")

	// Virtual stamp: the repo still returns the raw stored counter.
	raw, err := f.taskRepo.GetByID(ctx, tr.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, raw.TimeSpentS, "fallback must not persist")
}

func TestTaskGet_SpentFallback_StoredCounterWins(t *testing.T) {
	t.Parallel()
	f := newSpentFallbackDeps(t)
	ctx := context.Background()

	// Task WITH entries + timeline: the entry-derived counter wins.
	withEntries := &task.Task{ProjectID: f.projectID, ColumnID: f.cols[0].ID, Title: "tracked"}
	require.NoError(t, f.taskRepo.Create(ctx, withEntries))
	seedStatusTimeline(t, f, withEntries.ID, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
	now := time.Now().Truncate(time.Second)
	dur := int64(1800)
	_, err := sqlite.NewTimeEntryRepository(f.db).CreateAndAccrue(ctx, &timeentry.TimeEntry{
		TaskID: withEntries.ID, AgentID: f.ownerID, StartedAt: now.Add(-30 * time.Minute),
		EndedAt: &now, DurationS: &dur, Source: timeentry.SourceManual,
	})
	require.NoError(t, err)

	rr := doReq(f.router, http.MethodGet, "/api/v1/tasks/"+withEntries.ID, f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var got task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Equal(t, 1800, got.TimeSpentS, "stored counter wins over derived")
}

func TestTaskGet_SpentFallback_ManualCounterDisables(t *testing.T) {
	t.Parallel()
	f := newSpentFallbackDeps(t)
	ctx := context.Background()

	// Task with a manually set counter (>0) and a status timeline:
	// no stamp — the owner's explicit number is untouchable.
	manual := &task.Task{ProjectID: f.projectID, ColumnID: f.cols[0].ID, Title: "manual", TimeSpentS: 7200}
	require.NoError(t, f.taskRepo.Create(ctx, manual))
	seedStatusTimeline(t, f, manual.ID, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))

	rr := doReq(f.router, http.MethodGet, "/api/v1/tasks/"+manual.ID, f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var got task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Equal(t, 7200, got.TimeSpentS, "manual counter untouched")
}

func TestTaskList_SpentFallback_Batched(t *testing.T) {
	t.Parallel()
	f := newSpentFallbackDeps(t)
	ctx := context.Background()

	legacy := &task.Task{ProjectID: f.projectID, ColumnID: f.cols[0].ID, Title: "legacy-list"}
	tracked := &task.Task{ProjectID: f.projectID, ColumnID: f.cols[0].ID, Title: "tracked-list"}
	require.NoError(t, f.taskRepo.Create(ctx, legacy))
	require.NoError(t, f.taskRepo.Create(ctx, tracked))
	seedStatusTimeline(t, f, legacy.ID, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))

	rr := doReq(f.router, http.MethodGet, "/api/v1/projects/"+f.projectID+"/tasks", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var out struct {
		Tasks []*task.Task `json:"tasks"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&out))
	byID := map[string]int{}
	for _, tr := range out.Tasks {
		byID[tr.ID] = tr.TimeSpentS
	}
	assert.Equal(t, 3600, byID[legacy.ID], "legacy card shows derived seconds")
	assert.Equal(t, 0, byID[tracked.ID], "entry-less but no timeline → stays 0")
}
