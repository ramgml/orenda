package task_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// Task 376 (PRD F-T-3): the `rejected` parking column. The tests below
// run under the full driver matrix (sqlite + postgres legs) — every
// assertion goes through the domain/service surface, never raw SQL.

// rejectedColumns returns the board's parking column (last default) —
// seeded by CreateProject on every fresh board.
func rejectedColumns(t *testing.T, cols []*project.Column) *project.Column {
	t.Helper()
	require.NotEmpty(t, cols)
	last := cols[len(cols)-1]
	require.Equal(t, "rejected", last.Status, "rejected must be the last default column")
	return last
}

// T376: a human drag into the rejected column parks the card —
// status follows the column's machine key, ANY pending awaiting flag
// clears (parked work owes nobody anything), completed_at stays nil
// (rejection is not completion), and the move is audited.
func TestService_Move_ToRejectedParksTheCard(t *testing.T) {
	db := setupMoveDB(t)
	p, cols := setupMoveProject(t, db)
	repo := sqlite.NewTaskRepository(db)
	projRepo := sqlite.NewProjectRepository(db)
	rec := &recordingRecorder{}
	svc := taskservice.New(repo, nil, rec, nil, &recordingHub{})
	svc.Columns = projRepo

	rejectedCol := rejectedColumns(t, cols)
	todo := cols[1]

	// awaiting=human variant (a withdrawn review) parks cleanly.
	reviewed := &task.Task{
		ProjectID: p.ID, ColumnID: cols[3].ID, Title: "withdrawn",
		Status: task.StatusReview, Awaiting: task.AwaitingHuman,
	}
	require.NoError(t, repo.Create(context.Background(), reviewed))
	moved, err := svc.Move(context.Background(), reviewed.ID, taskservice.MoveOptions{
		TargetColumnID: rejectedCol.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, task.StatusRejected, moved.Status)
	assert.Equal(t, rejectedCol.ID, moved.ColumnID)
	assert.Equal(t, task.AwaitingNone, moved.Awaiting)
	assert.Nil(t, moved.CompletedAt, "rejection must not stamp completed_at")

	// awaiting=agent variant (declined mid-flight work) parks too —
	// unlike a todo-drag, which keeps awaiting=agent.
	agentsTurn := &task.Task{
		ProjectID: p.ID, ColumnID: todo.ID, Title: "declined",
		Status: task.StatusTodo, Awaiting: task.AwaitingAgent,
	}
	require.NoError(t, repo.Create(context.Background(), agentsTurn))
	moved, err = svc.Move(context.Background(), agentsTurn.ID, taskservice.MoveOptions{
		TargetColumnID: rejectedCol.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, task.AwaitingNone, moved.Awaiting)

	// Persisted, not just in-memory.
	back, err := repo.GetByID(context.Background(), agentsTurn.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusRejected, back.Status)
	assert.Equal(t, task.AwaitingNone, back.Awaiting)
	assert.Nil(t, back.CompletedAt)

	// Audit: the drag recorded a task.moved row naming the column.
	foundMoved := false
	for _, call := range rec.calls {
		if strings.Contains(call, string(activity.ActionMoved)) && strings.Contains(call, agentsTurn.ID) {
			foundMoved = true
		}
	}
	assert.True(t, foundMoved, "expected a task.moved activity row, got %v", rec.calls)
}

// T376: the status→column sync knows rejected — a PATCH
// {status: "rejected"} lands the card in the column that carries the
// machine key, keeping the 27.8 invariant `task.status ≡
// status(task.column_id)`.
func TestService_SyncStatusRejected_MovesToRejectedColumn(t *testing.T) {
	db := setupMoveDB(t)
	p, cols := setupMoveProject(t, db)
	repo := sqlite.NewTaskRepository(db)
	projRepo := sqlite.NewProjectRepository(db)
	svc := taskservice.New(repo, nil, &recordingRecorder{}, nil, &recordingHub{})
	svc.Columns = projRepo

	rejectedCol := rejectedColumns(t, cols)

	tr := &task.Task{
		ProjectID: p.ID, ColumnID: cols[1].ID, Title: "to park",
		Status: task.StatusTodo,
	}
	require.NoError(t, repo.Create(context.Background(), tr))

	prevStatus := tr.Status
	tr.Status = task.StatusRejected
	require.NoError(t, svc.SyncAndSave(context.Background(), tr, "u-1", activity.ActorUser, prevStatus))

	back, err := repo.GetByID(context.Background(), tr.ID)
	require.NoError(t, err)
	assert.Equal(t, rejectedCol.ID, back.ColumnID, "status=rejected must sync the column")
	assert.Equal(t, task.StatusRejected, back.Status)
	assert.Equal(t, task.AwaitingNone, back.Awaiting)
	assert.Nil(t, back.CompletedAt)
}

// T376: auto-block must not catch rejected cards — a parked card owes
// nobody anything, so a new blocker edge records itself without
// flipping the status (same treatment as done).
func TestService_AutoBlock_SkipsRejected(t *testing.T) {
	db := setupMoveDB(t)
	p, cols := setupMoveProject(t, db)
	tasks := sqlite.NewTaskRepository(db)
	svc := taskservice.New(tasks, nil, &recordingRecorder{}, nil, &recordingHub{})
	svc.Columns = sqlite.NewProjectRepository(db)

	rejectedCol := rejectedColumns(t, cols)

	parked := &task.Task{
		ProjectID: p.ID, ColumnID: rejectedCol.ID, Title: "parked",
		Status: task.StatusRejected,
	}
	require.NoError(t, tasks.Create(context.Background(), parked))
	blocker := &task.Task{
		ProjectID: p.ID, ColumnID: cols[1].ID, Title: "blocker",
		Status: task.StatusTodo,
	}
	require.NoError(t, tasks.Create(context.Background(), blocker))

	got, err := svc.AddBlocker(context.Background(), parked.ID, blocker.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusRejected, got.Status,
		"auto-block must leave a rejected card parked")
	assert.Empty(t, got.BlockedPrevStatus)
	assert.Equal(t, task.AwaitingNone, got.Awaiting)

	back, err := tasks.GetByID(context.Background(), parked.ID)
	require.NoError(t, err)
	assert.Equal(t, task.StatusRejected, back.Status)

	// The edge itself is real: the unfinished blocker still gates a
	// claim attempt on the parked card.
	_, err = svc.Claim(context.Background(), parked.ID, "agent-1")
	require.Error(t, err, "unfinished blockers keep gating Claim")
}

// T376: the revival loop — rejected → todo is a plain human drag. It
// un-parks the card with awaiting=none and stays claimable material
// (status=todo feeds the agent ready queue).
func TestService_Move_RejectedToTodoRevives(t *testing.T) {
	db := setupMoveDB(t)
	p, cols := setupMoveProject(t, db)
	repo := sqlite.NewTaskRepository(db)
	projRepo := sqlite.NewProjectRepository(db)
	svc := taskservice.New(repo, nil, &recordingRecorder{}, nil, &recordingHub{})
	svc.Columns = projRepo

	rejectedCol := rejectedColumns(t, cols)
	todo := cols[1]

	parked := &task.Task{
		ProjectID: p.ID, ColumnID: rejectedCol.ID, Title: "revive me",
		Status: task.StatusRejected,
	}
	require.NoError(t, repo.Create(context.Background(), parked))

	moved, err := svc.Move(context.Background(), parked.ID, taskservice.MoveOptions{
		TargetColumnID: todo.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, task.StatusTodo, moved.Status)
	assert.Equal(t, todo.ID, moved.ColumnID)
	assert.Equal(t, task.AwaitingNone, moved.Awaiting)
	assert.Nil(t, moved.CompletedAt)
}
