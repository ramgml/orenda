package task_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/activity"
	agentdomain "github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
	"github.com/ramgml/orenda/internal/testutil"
)

// Phase 27.8: every write path that touches status must also update
// the column to the one carrying that status. Without that, the
// task's status label and its kanban position drift apart again
// — the very bug Phase 27.7 surfaced.

type p278Fixture struct {
	project     *project.Project
	cols        []*project.Column
	taskRepo    task.Repository
	projectRepo project.Repository
	db          *sql.DB
	rec         *recordingRecorder
}

func setupPhase278Project(t *testing.T) *p278Fixture {
	t.Helper()
	db, _ := testutil.TemplateDBOpen(t)

	users := sqlite.NewUserRepository(db)
	owner := &user.User{
		Email:        "p278-" + t.Name() + "@x.com",
		PasswordHash: "x",
		DisplayName:  "O",
	}
	require.NoError(t, users.Create(context.Background(), owner))

	projectRepo := sqlite.NewProjectRepository(db)
	pr, _, columns, err := projectRepo.CreateProject(context.Background(), &project.Project{
		Name: "Orenda", OwnerID: owner.ID,
	})
	require.NoError(t, err)
	return &p278Fixture{
		project:     pr,
		cols:        columns,
		taskRepo:    sqlite.NewTaskRepository(db),
		projectRepo: projectRepo,
		db:          db,
	}
}

func columnByStatus(t *testing.T, f *p278Fixture, status task.Status) *project.Column {
	t.Helper()
	for _, c := range f.cols {
		if c.Status == string(status) {
			return c
		}
	}
	t.Fatalf("no column with status %q in %d columns", status, len(f.cols))
	return nil
}

func newSvc(t *testing.T, f *p278Fixture) *taskservice.Service {
	t.Helper()
	f.rec = &recordingRecorder{}
	svc := taskservice.New(
		f.taskRepo,
		sqlite.NewTaskLockRepository(f.db),
		f.rec,
		nil,
		&recordingHub{},
	)
	svc.Columns = f.projectRepo
	return svc
}

func mustGetByID(t *testing.T, repo task.Repository, id string) *task.Task {
	t.Helper()
	tr, err := repo.GetByID(context.Background(), id)
	require.NoError(t, err)
	return tr
}

func seedP278Agent(t *testing.T, f *p278Fixture, label string) string {
	t.Helper()
	tokens := sqlite.NewAPITokenRepository(f.db)
	users := sqlite.NewUserRepository(f.db)
	u := &user.User{
		Email:        "p278seed-" + label + "@x.com",
		PasswordHash: "x",
		DisplayName:  "Seed",
	}
	require.NoError(t, users.Create(context.Background(), u))
	tok, err := tokens.Create(context.Background(), u.ID, "tok-"+label, "fake", "[]", nil)
	require.NoError(t, err)
	agents := sqlite.NewAgentRepository(f.db)
	a := &agentdomain.Agent{
		Name:    "p278seed-" + label,
		Type:    []string{"qwen"},
		TokenID: tok.ID,
	}
	require.NoError(t, agents.Create(context.Background(), a))
	return a.ID
}

func TestService_Claim_MovesCardToInProgress(t *testing.T) {
	f := setupPhase278Project(t)
	backlog := columnByStatus(t, f, task.StatusBacklog)
	inProgress := columnByStatus(t, f, task.StatusInProgress)
	svc := newSvc(t, f)
	agentID := seedP278Agent(t, f, "claim")

	tr := &task.Task{ProjectID: f.project.ID, ColumnID: backlog.ID, Title: "claim me"}
	require.NoError(t, f.taskRepo.Create(context.Background(), tr))

	got, err := svc.Claim(context.Background(), tr.ID, agentID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusInProgress, got.Status, "agent-flow Claim sets status")
	assert.Equal(t, inProgress.ID, got.ColumnID,
		"Phase 27.8: column must follow the new status, not stay on backlog")
}

func TestService_Release_DropsCardToTodo(t *testing.T) {
	f := setupPhase278Project(t)
	backlog := columnByStatus(t, f, task.StatusBacklog)
	todo := columnByStatus(t, f, task.StatusTodo)
	svc := newSvc(t, f)
	agentID := seedP278Agent(t, f, "release")

	tr := &task.Task{ProjectID: f.project.ID, ColumnID: backlog.ID, Title: "release me"}
	require.NoError(t, f.taskRepo.Create(context.Background(), tr))
	_, err := svc.Claim(context.Background(), tr.ID, agentID)
	require.NoError(t, err)

	_, err = svc.Release(context.Background(), tr.ID, agentID)
	require.NoError(t, err)
	got := mustGetByID(t, f.taskRepo, tr.ID)
	assert.Equal(t, task.StatusTodo, got.Status)
	assert.Equal(t, todo.ID, got.ColumnID)
}

func TestService_Submit_MovesCardToReview(t *testing.T) {
	f := setupPhase278Project(t)
	backlog := columnByStatus(t, f, task.StatusBacklog)
	review := columnByStatus(t, f, task.StatusReview)
	svc := newSvc(t, f)
	agentID := seedP278Agent(t, f, "submit")

	tr := &task.Task{ProjectID: f.project.ID, ColumnID: backlog.ID, Title: "submit me"}
	require.NoError(t, f.taskRepo.Create(context.Background(), tr))
	_, err := svc.Claim(context.Background(), tr.ID, agentID)
	require.NoError(t, err)

	_, err = svc.Submit(context.Background(), tr.ID, agentID, "ready for review")
	require.NoError(t, err)
	got := mustGetByID(t, f.taskRepo, tr.ID)
	assert.Equal(t, task.StatusReview, got.Status)
	assert.Equal(t, review.ID, got.ColumnID,
		"Phase 27.8: card moves to the review column on submit")
}

func TestService_ReviewApprove_MovesCardToDone(t *testing.T) {
	f := setupPhase278Project(t)
	review := columnByStatus(t, f, task.StatusReview)
	done := columnByStatus(t, f, task.StatusDone)
	svc := newSvc(t, f)

	tr := &task.Task{
		ProjectID: f.project.ID, ColumnID: review.ID, Title: "review me",
		Status: task.StatusReview,
	}
	require.NoError(t, f.taskRepo.Create(context.Background(), tr))

	_, err := svc.Review(context.Background(), tr.ID, "u-owner", taskservice.ReviewApprove, "")
	require.NoError(t, err)
	got := mustGetByID(t, f.taskRepo, tr.ID)
	assert.Equal(t, task.StatusDone, got.Status)
	assert.Equal(t, done.ID, got.ColumnID)
	assert.NotNil(t, got.CompletedAt, "approve sets completed_at")
}

func TestService_ReviewReject_MovesCardBackToInProgress(t *testing.T) {
	f := setupPhase278Project(t)
	review := columnByStatus(t, f, task.StatusReview)
	inProgress := columnByStatus(t, f, task.StatusInProgress)
	svc := newSvc(t, f)

	tr := &task.Task{
		ProjectID: f.project.ID, ColumnID: review.ID, Title: "reject me",
		Status: task.StatusReview,
	}
	require.NoError(t, f.taskRepo.Create(context.Background(), tr))

	_, err := svc.Review(context.Background(), tr.ID, "u-owner", taskservice.ReviewReject, "needs work")
	require.NoError(t, err)
	got := mustGetByID(t, f.taskRepo, tr.ID)
	assert.Equal(t, task.StatusInProgress, got.Status)
	assert.Equal(t, inProgress.ID, got.ColumnID)
}

func TestService_SyncStatusAndColumn_StatusDrivesColumn(t *testing.T) {
	f := setupPhase278Project(t)
	backlog := columnByStatus(t, f, task.StatusBacklog)
	done := columnByStatus(t, f, task.StatusDone)
	svc := newSvc(t, f)

	tr := &task.Task{ProjectID: f.project.ID, ColumnID: backlog.ID, Title: "manual"}
	require.NoError(t, f.taskRepo.Create(context.Background(), tr))

	tr.Status = task.StatusDone
	svc.SyncStatusAndColumn(context.Background(), tr)
	assert.Equal(t, done.ID, tr.ColumnID, "status=done → card lands on the done column")
}

// Phase 27.8.4: the DnD path picks a destination column by id and
// expects the card's status to follow. Without this lift, the
// invariant `task.status ≡ column.status` is broken every time the
// owner drags a card, which is the very axis-collapse 27.8 shipped
// to prevent. (Owner override: a manual drag onto `done` is allowed
// even when `awaiting=agent` — see PLAN §27.8 decisions.)
func TestService_Move_ColumnDrivesStatus(t *testing.T) {
	f := setupPhase278Project(t)
	todo := columnByStatus(t, f, task.StatusTodo)
	done := columnByStatus(t, f, task.StatusDone)
	svc := newSvc(t, f)

	tr := &task.Task{
		ProjectID: f.project.ID,
		ColumnID:  todo.ID,
		Title:     "drag me",
		Status:    task.StatusTodo,
	}
	require.NoError(t, f.taskRepo.Create(context.Background(), tr))

	got, err := svc.Move(context.Background(), tr.ID, taskservice.MoveOptions{
		TargetColumnID: done.ID,
		Position:       0,
	})
	require.NoError(t, err)
	assert.Equal(t, done.ID, got.ColumnID, "Move relocates the column")
	assert.Equal(t, task.StatusDone, got.Status,
		"Phase 27.8.4: status flips to the destination column's status")

	// Persisted, not just returned.
	persisted := mustGetByID(t, f.taskRepo, tr.ID)
	assert.Equal(t, task.StatusDone, persisted.Status)
	assert.Equal(t, done.ID, persisted.ColumnID)

	// Drag back: status follows the other way too.
	got, err = svc.Move(context.Background(), tr.ID, taskservice.MoveOptions{
		TargetColumnID: todo.ID,
		Position:       0,
	})
	require.NoError(t, err)
	assert.Equal(t, task.StatusTodo, got.Status)
	assert.Equal(t, todo.ID, got.ColumnID)
}

// T46: every status/column change must write a task_activity row.
// Claim sets status=in_progress — verify the activity row exists.
func TestService_Claim_RecordsActivity(t *testing.T) {
	f := setupPhase278Project(t)
	backlog := columnByStatus(t, f, task.StatusBacklog)
	svc := newSvc(t, f)
	agentID := seedP278Agent(t, f, "claim-act")

	tr := &task.Task{ProjectID: f.project.ID, ColumnID: backlog.ID, Title: "claim act"}
	require.NoError(t, f.taskRepo.Create(context.Background(), tr))

	_, err := svc.Claim(context.Background(), tr.ID, agentID)
	require.NoError(t, err)

	// recordingRecorder stores "taskID:action:payload" strings.
	found := false
	for _, call := range f.rec.calls {
		if strings.Contains(call, tr.ID) && strings.Contains(call, string(activity.ActionClaimed)) {
			found = true
			break
		}
	}
	assert.True(t, found, "Claim must write a task_activity row; got calls: %v", f.rec.calls)
}

// T46: Release sets status=todo — verify activity row.
func TestService_Release_RecordsActivity(t *testing.T) {
	f := setupPhase278Project(t)
	backlog := columnByStatus(t, f, task.StatusBacklog)
	svc := newSvc(t, f)
	agentID := seedP278Agent(t, f, "release-act")

	tr := &task.Task{ProjectID: f.project.ID, ColumnID: backlog.ID, Title: "release act"}
	require.NoError(t, f.taskRepo.Create(context.Background(), tr))
	_, err := svc.Claim(context.Background(), tr.ID, agentID)
	require.NoError(t, err)

	_, err = svc.Release(context.Background(), tr.ID, agentID)
	require.NoError(t, err)

	found := false
	for _, call := range f.rec.calls {
		if strings.Contains(call, tr.ID) && strings.Contains(call, string(activity.ActionReleased)) {
			found = true
			break
		}
	}
	assert.True(t, found, "Release must write a task_activity row; got calls: %v", f.rec.calls)
}

// T46: Review (approve) sets status=done — verify activity row.
func TestService_Review_RecordsActivity(t *testing.T) {
	f := setupPhase278Project(t)
	review := columnByStatus(t, f, task.StatusReview)
	svc := newSvc(t, f)

	tr := &task.Task{
		ProjectID: f.project.ID, ColumnID: review.ID, Title: "review act",
		Status: task.StatusReview,
	}
	require.NoError(t, f.taskRepo.Create(context.Background(), tr))

	_, err := svc.Review(context.Background(), tr.ID, "u-owner", taskservice.ReviewApprove, "")
	require.NoError(t, err)

	found := false
	for _, call := range f.rec.calls {
		if strings.Contains(call, tr.ID) && strings.Contains(call, string(activity.ActionReviewed)) {
			found = true
			break
		}
	}
	assert.True(t, found, "Review must write a task_activity row; got calls: %v", f.rec.calls)
}

// T46: SyncAndSave is the canonical save point — it must maintain the
// invariant status(column_id) == task.status.
func TestService_SyncAndSave_StatusDrivesColumn(t *testing.T) {
	f := setupPhase278Project(t)
	backlog := columnByStatus(t, f, task.StatusBacklog)
	done := columnByStatus(t, f, task.StatusDone)
	svc := newSvc(t, f)

	tr := &task.Task{ProjectID: f.project.ID, ColumnID: backlog.ID, Title: "sync-save"}
	require.NoError(t, f.taskRepo.Create(context.Background(), tr))

	// status → column direction
	prevStatus := tr.Status
	tr.Status = task.StatusDone
	err := svc.SyncAndSave(context.Background(), tr, "user-1", activity.ActorUser, prevStatus)
	require.NoError(t, err)
	assert.Equal(t, done.ID, tr.ColumnID, "status=done → card on done column")
	assert.Equal(t, task.StatusDone, tr.Status)

	persisted := mustGetByID(t, f.taskRepo, tr.ID)
	assert.Equal(t, done.ID, persisted.ColumnID, "persisted: status=done → done column")
	assert.Equal(t, task.StatusDone, persisted.Status)
}

// T46: column→status is done by the handler (applyTaskPatch) before
// SyncAndSave. This test verifies the invariant end-to-end: when the
// handler sets column_id, the status follows.
func TestService_SyncAndSave_ColumnDrivesStatus(t *testing.T) {
	f := setupPhase278Project(t)
	todo := columnByStatus(t, f, task.StatusTodo)
	done := columnByStatus(t, f, task.StatusDone)
	svc := newSvc(t, f)

	tr := &task.Task{ProjectID: f.project.ID, ColumnID: todo.ID, Title: "sync-col"}
	require.NoError(t, f.taskRepo.Create(context.Background(), tr))

	// Simulate handler setting column_id (column→status) then SyncAndSave.
	prevStatus := tr.Status
	tr.ColumnID = done.ID
	tr.Status = task.Status(done.Status) // Handler lifts column status before SyncAndSave.
	err := svc.SyncAndSave(context.Background(), tr, "user-1", activity.ActorUser, prevStatus)
	require.NoError(t, err)
	assert.Equal(t, task.StatusDone, tr.Status, "done column → status=done")

	persisted := mustGetByID(t, f.taskRepo, tr.ID)
	assert.Equal(t, task.StatusDone, persisted.Status, "persisted: done column → status=done")
	assert.Equal(t, done.ID, persisted.ColumnID)
}

// T46: SyncAndSave records activity when status changes — exactly once.
func TestService_SyncAndSave_RecordsActivityOnStatusChange(t *testing.T) {
	f := setupPhase278Project(t)
	backlog := columnByStatus(t, f, task.StatusBacklog)
	svc := newSvc(t, f)

	tr := &task.Task{ProjectID: f.project.ID, ColumnID: backlog.ID, Title: "act-save", Status: task.StatusBacklog}
	require.NoError(t, f.taskRepo.Create(context.Background(), tr))

	// Reload to get the DB-persisted task.
	tr = mustGetByID(t, f.taskRepo, tr.ID)
	assert.Equal(t, task.StatusBacklog, tr.Status, "precondition: status starts as backlog")

	// Simulate a status change (status→column will run in SyncAndSave).
	prevStatus := tr.Status
	tr.Status = task.StatusDone
	err := svc.SyncAndSave(context.Background(), tr, "user-act", activity.ActorUser, prevStatus)
	require.NoError(t, err)

	count := 0
	for _, call := range f.rec.calls {
		if strings.Contains(call, tr.ID) && strings.Contains(call, string(activity.ActionStatusChanged)) {
			count++
		}
	}
	assert.Equal(t, 1, count, "SyncAndSave must record status_changed exactly once; got calls: %v", f.rec.calls)
}

// T46: column-only PATCH on a statusless column must persist the chosen
// column without the card jumping — SyncAndSave must NOT re-sync when
// status hasn't changed. This is the reviewer's point 3+4 regression.
func TestService_SyncAndSave_ColumnOnlyPatch_PersistsColumn(t *testing.T) {
	f := setupPhase278Project(t)
	backlog := columnByStatus(t, f, task.StatusBacklog)
	done := columnByStatus(t, f, task.StatusDone)
	svc := newSvc(t, f)

	// Task starts in backlog with status=backlog.
	tr := &task.Task{ProjectID: f.project.ID, ColumnID: backlog.ID, Title: "col-only",
		Status: task.StatusBacklog}
	require.NoError(t, f.taskRepo.Create(context.Background(), tr))

	// Simulate column-only PATCH: caller moves card to done column and
	// lifts status to done. prevStatus == new status → SyncAndSave must
	// NOT re-sync column (status didn't change from caller's perspective).
	prevStatus := task.StatusDone // Same as new status — "no status change"
	tr.ColumnID = done.ID
	tr.Status = task.StatusDone
	err := svc.SyncAndSave(context.Background(), tr, "u1", activity.ActorUser, prevStatus)
	require.NoError(t, err)

	persisted := mustGetByID(t, f.taskRepo, tr.ID)
	assert.Equal(t, done.ID, persisted.ColumnID, "column-only PATCH must persist chosen column")
	assert.Equal(t, task.StatusDone, persisted.Status)

	// Verify no status_changed activity was recorded (status didn't change).
	count := 0
	for _, call := range f.rec.calls {
		if strings.Contains(call, tr.ID) && strings.Contains(call, string(activity.ActionStatusChanged)) {
			count++
		}
	}
	assert.Equal(t, 0, count, "no status_changed activity when status unchanged; got: %v", f.rec.calls)
}
