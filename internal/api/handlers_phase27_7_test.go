package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	activitysvc "github.com/ramgml/orenda/internal/service/activity"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// Phase 27.7: status / priority / assignee are editable from the
// task sidebar. Backend side-effects:
//
//   - PATCH status=done auto-fills completed_at (unless the caller
//     sets completed_at explicitly) and normalises awaiting to none.
//   - PATCH status=review normalises awaiting to human.
//   - PATCH any other status normalises awaiting to none.
//   - status / priority / assignee changes emit activity rows with
//     typed JSON payloads so the feed shows both sides.
//
// These tests run through the real router (SQLite-backed) so the
// side-effects go through every layer — handler → applyTaskPatch →
// update → taskSvc.RecordActivity → ListByTask. Pure handler tests
// would miss the repo write and the activity insert.

func p27_7_makeTask(t *testing.T, f p27_7Fixtures, title string) *task.Task {
	t.Helper()
	tr := &task.Task{
		ProjectID: f.projectID,
		ColumnID:  f.cols[0].ID,
		Title:     title,
		Status:    task.StatusTodo,
		Priority:  task.PriorityMedium,
	}
	require.NoError(t, f.tasks.Create(context.Background(), tr))
	return tr
}

// p27_7Deps is the same fixture as columnDeps but also wires the
// ActivityRecorder so patchTaskHandler's activity rows actually
// persist. We duplicate the setup rather than touch columnDeps
// (most tests don't need the activity side-channel). The returned
// fixtures struct is augmented with the activity repo so tests
// can read rows back directly without relying on
// GET /api/v1/tasks/:id/activity (which would require an extra
// service wiring we don't have here).
type p27_7Fixtures struct {
	colFixtures
	activityRepo activity.Repository
}

func p27_7Deps(t *testing.T) p27_7Fixtures {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "p277.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	users := sqlite.NewUserRepository(db)
	u := &user.User{Email: "p277@x.com", PasswordHash: mustHashFast(t), DisplayName: "P"}
	require.NoError(t, users.Create(context.Background(), u))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})
	repo := sqlite.NewTaskRepository(db)
	projRepo := sqlite.NewProjectRepository(db)
	activityRepo := sqlite.NewActivityRepository(db)
	// task.Service expects a Recorder with method Record(...).
	// activity.Recorder exposes RecordTask. Bridge them.
	taskRecorder := &taskRecorderBridge{r: activitysvc.New(activityRepo)}
	taskSvc := taskservice.New(repo, sqlite.NewTaskLockRepository(db), taskRecorder, nil, hub)

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	deps := api.Dependencies{
		Logger:      zap.NewNop(),
		Signer:      signer,
		Users:       users,
		Projects:    projRepo,
		Tasks:       repo,
		Tokens:      sqlite.NewAPITokenRepository(db),
		TaskService: taskSvc,
		WSHub:       hub,
		CookieName:  "orenda_session",
	}
	router := api.NewRouter(&deps)

	body, _ := json.Marshal(map[string]string{"email": "p277@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value

	p, _, _, err := projRepo.CreateProject(context.Background(), &project.Project{
		Name: "Demo", OwnerID: u.ID, Color: "#3b82f6",
	})
	require.NoError(t, err)
	_, cols, err := projRepo.GetBoard(context.Background(), p.ID)
	require.NoError(t, err)
	return p27_7Fixtures{
		colFixtures: colFixtures{
			router:    router,
			cookie:    cookie,
			projectID: p.ID,
			cols:      cols,
			tasks:     repo,
			taskRepo:  repo,
		},
		activityRepo: activityRepo,
	}
}

func p27_7_fetchActivity(t *testing.T, f p27_7Fixtures, taskID string) []activity.Activity {
	t.Helper()
	rows, err := f.activityRepo.ListByTask(context.Background(), taskID)
	require.NoError(t, err)
	out := make([]activity.Activity, 0, len(rows))
	for _, r := range rows {
		if r != nil {
			out = append(out, *r)
		}
	}
	return out
}

// taskRecorderBridge adapts activity.Recorder (RecordTask) to
// the task.Service Recorder interface (Record).
type taskRecorderBridge struct {
	r *activitysvc.Recorder
}

func (b *taskRecorderBridge) Record(
	ctx context.Context, taskID string,
	actorType activity.ActorType, actorID string,
	action activity.Action, payload string,
) error {
	return b.r.RecordTask(ctx, taskID, actorType, actorID, action, payload)
}

func p27_7_fetchTask(t *testing.T, router http.Handler, cookie, id string) *task.Task {
	t.Helper()
	rr := doReq(router, "GET", "/api/v1/tasks/"+id, cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var got task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	return &got
}

func p27_7_latestAction(t *testing.T, items []activity.Activity, want activity.Action) activity.Activity {
	t.Helper()
	for _, a := range items {
		if a.Action == want {
			return a
		}
	}
	t.Fatalf("no activity row with action %q in %d entries", want, len(items))
	return activity.Activity{}
}

// 1) PATCH status=done auto-fills completed_at and normalises awaiting.
func TestPatchTask_StatusDone_AutoCompletesAndNormalisesAwaiting(t *testing.T) {
	f := p27_7Deps(t)
	tr := p27_7_makeTask(t, f, "Done it")

	rr := doReq(f.router, "PATCH", "/api/v1/tasks/"+tr.ID, f.cookie, map[string]any{
		"status": "done",
	})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	got := p27_7_fetchTask(t, f.router, f.cookie, tr.ID)
	assert.Equal(t, task.StatusDone, got.Status)
	assert.Equal(t, task.AwaitingNone, got.Awaiting, "manual done → awaiting=none")
	require.NotNil(t, got.CompletedAt, "completed_at must be filled in")
	assert.WithinDuration(t, time.Now().UTC(), *got.CompletedAt, 5*time.Second)

	row := p27_7_latestAction(t, p27_7_fetchActivity(t, f, tr.ID), activity.ActionStatusChanged)
	assert.Contains(t, row.Payload, `"from":"todo"`)
	assert.Contains(t, row.Payload, `"to":"done"`)
}

// 2) PATCH status=review normalises awaiting to human.
func TestPatchTask_StatusReview_NormalisesAwaitingHuman(t *testing.T) {
	f := p27_7Deps(t)
	tr := p27_7_makeTask(t, f, "Ready for review")

	rr := doReq(f.router, "PATCH", "/api/v1/tasks/"+tr.ID, f.cookie, map[string]any{
		"status": "review",
	})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	got := p27_7_fetchTask(t, f.router, f.cookie, tr.ID)
	assert.Equal(t, task.StatusReview, got.Status)
	assert.Equal(t, task.AwaitingHuman, got.Awaiting)
}

// 3) PATCH status (any non-done/review) clears awaiting=agent. We
// start the task as in_progress with awaiting=agent (the agent-flow
// shape) and have the owner drag it back to todo to take the wheel.
func TestPatchTask_ManualStatusOverride_ClearsAwaitingAgent(t *testing.T) {
	f := p27_7Deps(t)
	tr := p27_7_makeTask(t, f, "Owner takes over")
	tr.Status = task.StatusInProgress
	tr.Awaiting = task.AwaitingAgent
	require.NoError(t, f.taskRepo.Update(context.Background(), tr))

	rr := doReq(f.router, "PATCH", "/api/v1/tasks/"+tr.ID, f.cookie, map[string]any{
		"status": "todo",
	})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	got := p27_7_fetchTask(t, f.router, f.cookie, tr.ID)
	assert.Equal(t, task.StatusTodo, got.Status)
	assert.Equal(t, task.AwaitingNone, got.Awaiting,
		"manual override must not preserve awaiting=agent (the owner took the wheel)")
}

// 4) PATCH priority emits activity row, no other side-effects.
func TestPatchTask_PriorityChange_EmitsActivity(t *testing.T) {
	f := p27_7Deps(t)
	tr := p27_7_makeTask(t, f, "Priority flip")

	rr := doReq(f.router, "PATCH", "/api/v1/tasks/"+tr.ID, f.cookie, map[string]any{
		"priority": "urgent",
	})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	got := p27_7_fetchTask(t, f.router, f.cookie, tr.ID)
	assert.Equal(t, task.PriorityUrgent, got.Priority)

	row := p27_7_latestAction(t, p27_7_fetchActivity(t, f, tr.ID), activity.ActionPriorityChanged)
	assert.Contains(t, row.Payload, `"from":"medium"`)
	assert.Contains(t, row.Payload, `"to":"urgent"`)
}

// 5) PATCH assignee (user → another user-style value) emits activity.
func TestPatchTask_AssigneeChange_EmitsActivity(t *testing.T) {
	f := p27_7Deps(t)
	tr := p27_7_makeTask(t, f, "Assign me")

	rr := doReq(f.router, "PATCH", "/api/v1/tasks/"+tr.ID, f.cookie, map[string]any{
		"assignee_type": "user",
		"assignee_id":   "u-other",
	})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	row := p27_7_latestAction(t, p27_7_fetchActivity(t, f, tr.ID), activity.ActionAssigned)
	assert.Contains(t, row.Payload, `"from":`)
	assert.Contains(t, row.Payload, `"to":{"type":"user","id":"u-other"}`)
}

// 6) PATCH with the same values emits no activity row (no spam).
func TestPatchTask_NoOpChange_EmitsNoActivity(t *testing.T) {
	f := p27_7Deps(t)
	tr := p27_7_makeTask(t, f, "No change")

	rr := doReq(f.router, "PATCH", "/api/v1/tasks/"+tr.ID, f.cookie, map[string]any{
		"priority": "medium", // same as default
	})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	items := p27_7_fetchActivity(t, f, tr.ID)
	for _, a := range items {
		assert.NotEqual(t, activity.ActionPriorityChanged, a.Action,
			"no-op PATCH must not emit priority_changed")
		assert.NotEqual(t, activity.ActionStatusChanged, a.Action,
			"no-op PATCH must not emit status_changed")
	}
}

// 7) PATCH done with an explicit completed_at preserves the caller value.
func TestPatchTask_StatusDoneWithExplicitCompletedAt_RespectsCaller(t *testing.T) {
	f := p27_7Deps(t)
	tr := p27_7_makeTask(t, f, "Done long ago")

	explicit := "2026-01-01T00:00:00Z"
	rr := doReq(f.router, "PATCH", "/api/v1/tasks/"+tr.ID, f.cookie, map[string]any{
		"status":       "done",
		"completed_at": explicit,
	})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	got := p27_7_fetchTask(t, f.router, f.cookie, tr.ID)
	require.NotNil(t, got.CompletedAt)
	assert.Equal(t, explicit, got.CompletedAt.UTC().Format(time.RFC3339))
}

func TestBulkPatchTasks_AppliesSharedSideEffectsAndReportsMissingIDs(t *testing.T) {
	f := p27_7Deps(t)
	a := p27_7_makeTask(t, f, "Bulk A")
	b := p27_7_makeTask(t, f, "Bulk B")

	rr := doReq(f.router, http.MethodPost, "/api/v1/tasks/bulk-edit", f.cookie, map[string]any{
		"task_ids": []string{a.ID, b.ID, "missing-task"},
		"patch":    map[string]any{"status": "done", "priority": "high"},
	})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var body struct {
		Tasks  []task.Task       `json:"tasks"`
		Errors map[string]string `json:"errors"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Len(t, body.Tasks, 2)
	assert.Contains(t, body.Errors, "missing-task")

	for _, id := range []string{a.ID, b.ID} {
		got := p27_7_fetchTask(t, f.router, f.cookie, id)
		assert.Equal(t, task.StatusDone, got.Status)
		assert.Equal(t, task.PriorityHigh, got.Priority)
		assert.NotNil(t, got.CompletedAt)
		assert.Equal(t, task.AwaitingNone, got.Awaiting)
	}
}
