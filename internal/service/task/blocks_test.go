package task_test

// Task 115: service-level auto-block / auto-unblock matrix.
//
// Required cases (DoD):
//   - AddBlocker auto-flips status → blocked + saves prev (todo fallback
//     for NULL prev on legacy rows).
//   - Already-blocked task keeps its first prev value (second blocker
//     must not overwrite).
//   - Done target is never auto-blocked (edge still created).
//   - RemoveBlocker restores prev when the last unfinished blocker is
//     gone; stays blocked otherwise.
//   - NULL prev (legacy row) restores to todo.
//   - Close blocker (done) → dependents unblock via OnCloseUnblockDependents.
//   - Manual move out of blocked clears prev (override).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

func TestService_AddBlocker_AutoBlockMatrix(t *testing.T) {
	t.Parallel()
	db, svc, _, p, col := setupClaimDB(t)
	tasks := sqlite.NewTaskRepository(db)
	ctx := context.Background()

	// --- todo target → blocked, prev=todo.
	a := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "A"}
	b := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "B"}
	require.NoError(t, tasks.Create(ctx, a))
	require.NoError(t, tasks.Create(ctx, b))

	tr, err := svc.AddBlocker(ctx, a.ID, b.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusBlocked, tr.Status)
	assert.Equal(t, task.StatusTodo, tr.BlockedPrevStatus)

	// --- Second blocker must NOT overwrite prev (first blocker wins).
	c := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "C"}
	require.NoError(t, tasks.Create(ctx, c))
	tr, err = svc.AddBlocker(ctx, a.ID, c.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusBlocked, tr.Status)
	assert.Equal(t, task.StatusTodo, tr.BlockedPrevStatus, "prev preserved")

	// --- Remove one of two blockers: still blocked, prev untouched.
	tr, err = svc.RemoveBlocker(ctx, a.ID, c.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusBlocked, tr.Status, "B still unfinished")
	assert.Equal(t, task.StatusTodo, tr.BlockedPrevStatus)

	// --- Remove the last blocker: restore prev, clear column.
	tr, err = svc.RemoveBlocker(ctx, a.ID, b.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusTodo, tr.Status, "prev restored")
	assert.Equal(t, task.Status(""), tr.BlockedPrevStatus, "prev cleared")

	got, err := tasks.GetByID(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusTodo, got.Status)
	assert.Equal(t, task.Status(""), got.BlockedPrevStatus)
}

func TestService_AddBlocker_NullPrevFallsBackToTodo(t *testing.T) {
	t.Parallel()
	db, svc, _, p, col := setupClaimDB(t)
	tasks := sqlite.NewTaskRepository(db)
	ctx := context.Background()

	a := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "A"}
	b := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "B"}
	require.NoError(t, tasks.Create(ctx, a))
	require.NoError(t, tasks.Create(ctx, b))
	// Create the edge WITHOUT the auto-flip (raw repo, like a legacy
	// writer), then simulate the pre-042 row: status=blocked, prev NULL.
	// The seeding hook lives on the concrete sqlite repo, reached
	// through a local interface — the production task.Repository
	// stays free of test-only methods.
	require.NoError(t, tasks.AddDependency(ctx, a.ID, b.ID))
	type prevSeeder interface {
		SetStatusAndPrevForTest(ctx context.Context, id string, status, prev task.Status) error
	}
	seed, ok := tasks.(prevSeeder)
	require.True(t, ok, "sqlite repo must expose SetStatusAndPrevForTest")
	require.NoError(t, seed.SetStatusAndPrevForTest(ctx, a.ID, task.StatusBlocked, ""))

	// Removing the only blocker restores... todo (NULL → todo fallback).
	tr, err := svc.RemoveBlocker(ctx, a.ID, b.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusTodo, tr.Status, "NULL prev → todo")
}

func TestService_AddBlocker_DoneTargetNeverBlocked(t *testing.T) {
	t.Parallel()
	db, svc, _, p, col := setupClaimDB(t)
	tasks := sqlite.NewTaskRepository(db)
	ctx := context.Background()

	done := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "done"}
	blocker := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "B"}
	require.NoError(t, tasks.Create(ctx, done))
	require.NoError(t, tasks.Create(ctx, blocker))
	done.Status = task.StatusDone
	require.NoError(t, tasks.Update(ctx, done))

	// Edge is created, but the done task is not flipped.
	tr, err := svc.AddBlocker(ctx, done.ID, blocker.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusDone, tr.Status, "done never auto-blocked")
	assert.Equal(t, task.Status(""), tr.BlockedPrevStatus)
}

func TestService_OnClose_UnblocksDependents(t *testing.T) {
	t.Parallel()
	db, svc, _, p, col := setupClaimDB(t)
	tasks := sqlite.NewTaskRepository(db)
	ctx := context.Background()

	a := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "A"}
	b := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "B"}
	require.NoError(t, tasks.Create(ctx, a))
	require.NoError(t, tasks.Create(ctx, b))

	// A blocked by B (auto-flip A → blocked).
	_, err := svc.AddBlocker(ctx, a.ID, b.ID)
	require.NoError(t, err)

	// B closes (Review approve path → PATCH status=done → hook).
	b.Status = task.StatusDone
	require.NoError(t, tasks.Update(ctx, b))
	svc.OnCloseUnblockDependents(ctx, b.ID)

	got, err := tasks.GetByID(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusTodo, got.Status, "dependent unblocked by closure")
	assert.Equal(t, task.Status(""), got.BlockedPrevStatus)
}

func TestService_ManualMoveOutOfBlocked_ClearsPrev(t *testing.T) {
	t.Parallel()
	db, svc, _, p, col := setupClaimDB(t)
	tasks := sqlite.NewTaskRepository(db)
	projRepo := sqlite.NewProjectRepository(db)
	// Move lifts the target column's status only when Columns is
	// wired (production does; setupClaimDB leaves it nil).
	svc.Columns = projRepo
	ctx := context.Background()

	a := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "A"}
	b := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "B"}
	require.NoError(t, tasks.Create(ctx, a))
	require.NoError(t, tasks.Create(ctx, b))

	// Manual move (user drags the card): wins over the auto-block —
	// the move lands the card on the target column and clears prev.
	_, boardCols, err := projRepo.GetBoard(ctx, p.ID)
	require.NoError(t, err)
	require.NotEmpty(t, boardCols)
	var doneColID string
	for _, c := range boardCols {
		if c.Status == string(task.StatusDone) {
			doneColID = c.ID
			break
		}
	}
	require.NotEmpty(t, doneColID, "board must have a done column")

	moved, err := svc.Move(ctx, a.ID, taskservice.MoveOptions{TargetColumnID: doneColID})
	require.NoError(t, err)
	assert.Equal(t, task.StatusDone, moved.Status, "manual move lifts status")
	assert.Equal(t, task.Status(""), moved.BlockedPrevStatus, "override clears prev")
}

func TestService_RemoveBlocker_UnknownEdge404(t *testing.T) {
	t.Parallel()
	db, svc, _, p, col := setupClaimDB(t)
	tasks := sqlite.NewTaskRepository(db)
	ctx := context.Background()

	a := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "A"}
	require.NoError(t, tasks.Create(ctx, a))

	_, err := svc.RemoveBlocker(ctx, a.ID, "no-such-blocker")
	require.Error(t, err)
}
