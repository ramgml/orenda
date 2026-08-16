package task_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// Phase 15: service-level dependency tests — cycle prevention, self-loop
// rejection, claim-with-blocker error, and the unlock-on-done behaviour.

// SetTaskDependencies rejects cycles (A→B + B→A).
func TestService_SetDependencies_CycleRejected(t *testing.T) {
	db, svc, _, p, col := setupClaimDB(t)
	tasks := sqlite.NewTaskRepository(db)
	ctx := context.Background()

	a := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "A"}
	b := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "B"}
	require.NoError(t, tasks.Create(ctx, a))
	require.NoError(t, tasks.Create(ctx, b))

	// A blocked-by B is fine.
	require.NoError(t, svc.SetTaskDependencies(ctx, a.ID, []string{b.ID}))

	// Now propose B blocked-by A: cycle (B → A → B).
	err := svc.SetTaskDependencies(ctx, b.ID, []string{a.ID})
	require.Error(t, err)
	assert.True(t,
		errors.Is(err, taskservice.ErrDependencyCycle),
		"expected ErrDependencyCycle, got %v", err)
}

// A→B→C is fine, but C→A must be rejected (3-cycle).
func TestService_SetDependencies_TransitiveCycleRejected(t *testing.T) {
	db, svc, _, p, col := setupClaimDB(t)
	tasks := sqlite.NewTaskRepository(db)
	ctx := context.Background()

	a := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "A"}
	b := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "B"}
	c := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "C"}
	require.NoError(t, tasks.Create(ctx, a))
	require.NoError(t, tasks.Create(ctx, b))
	require.NoError(t, tasks.Create(ctx, c))

	require.NoError(t, svc.SetTaskDependencies(ctx, a.ID, []string{b.ID}))
	require.NoError(t, svc.SetTaskDependencies(ctx, b.ID, []string{c.ID}))
	// Closing C→A would form A→B→C→A.
	err := svc.SetTaskDependencies(ctx, c.ID, []string{a.ID})
	require.Error(t, err)
	assert.True(t, errors.Is(err, taskservice.ErrDependencyCycle))
}

// Self-dependency is a 422.
func TestService_SetDependencies_SelfRejected(t *testing.T) {
	db, svc, _, p, col := setupClaimDB(t)
	tasks := sqlite.NewTaskRepository(db)
	ctx := context.Background()

	a := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "A"}
	require.NoError(t, tasks.Create(ctx, a))

	err := svc.SetTaskDependencies(ctx, a.ID, []string{a.ID})
	require.Error(t, err)
	assert.True(t, errors.Is(err, taskservice.ErrSelfDependency))
}

// Claim refuses a task with an unfinished blocker; the error carries
// the blocker IDs so the handler can render "blocked by these".
func TestService_Claim_BlockedBy(t *testing.T) {
	db, svc, _, p, col := setupClaimDB(t)
	tasks := sqlite.NewTaskRepository(db)
	ctx := context.Background()

	a := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "A"}
	b := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "B"}
	require.NoError(t, tasks.Create(ctx, a))
	require.NoError(t, tasks.Create(ctx, b))
	require.NoError(t, svc.SetTaskDependencies(ctx, a.ID, []string{b.ID}))

	_, err := svc.Claim(ctx, a.ID, "agent-1")
	require.Error(t, err)
	var blocked *taskservice.BlockedError
	require.True(t, errors.As(err, &blocked), "expected BlockedError, got %T", err)
	assert.Equal(t, []string{b.ID}, blocked.BlockerIDs)
}

// Once the blocker is done, claim succeeds.
func TestService_Claim_UnblocksWhenBlockerDone(t *testing.T) {
	db, svc, _, p, col := setupClaimDB(t)
	tasks := sqlite.NewTaskRepository(db)
	ctx := context.Background()

	a := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "A"}
	b := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "B"}
	require.NoError(t, tasks.Create(ctx, a))
	require.NoError(t, tasks.Create(ctx, b))
	require.NoError(t, svc.SetTaskDependencies(ctx, a.ID, []string{b.ID}))

	// Mark B done (no agent claim needed — we just flip the status).
	b.Status = task.StatusDone
	require.NoError(t, tasks.Update(ctx, b))

	// Claim needs an agent row (task_locks.agent_id has FK to agents).
	agentID := seedAgent(t, db, "unblock-claim")
	tr, err := svc.Claim(ctx, a.ID, agentID)
	require.NoError(t, err)
	assert.Equal(t, agentID, tr.AssigneeID)
	assert.Equal(t, task.StatusInProgress, tr.Status)
}
