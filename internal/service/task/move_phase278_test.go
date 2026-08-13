package task_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentdomain "github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
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
}

func setupPhase278Project(t *testing.T) *p278Fixture {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "p278.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

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
	svc := taskservice.New(
		f.taskRepo,
		sqlite.NewTaskLockRepository(f.db),
		&recordingRecorder{},
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
		Type:    agentdomain.TypeQwen,
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
