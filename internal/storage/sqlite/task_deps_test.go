package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
)

// Phase 15: dependency-graph CRUD on the repo layer.
//
// Cases:
//   - Add two tasks + edge between them; Blockers lists the right
//     task; Dependents lists the right id.
//   - RemoveDependency idempotent (re-removing is a no-op).
//   - Self-dependency rejected with ErrSelfDependency.
//   - SetTaskDependencies replaces the full set; second call with
//     empty list clears it.
//   - Blockers correctly reports "done" blockers as satisfied.
func TestTaskRepo_Dependencies(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)
	ctx := context.Background()

	a := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "A"}
	b := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "B"}
	c := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "C"}
	for _, tr := range []*task.Task{a, b, c} {
		require.NoError(t, repo.Create(ctx, tr))
	}

	// A blocked by B.
	require.NoError(t, repo.AddDependency(ctx, a.ID, b.ID))

	got, err := repo.Blockers(ctx, a.ID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, b.ID, got[0].BlockerID)
	assert.Equal(t, "B", got[0].Title)
	assert.False(t, got[0].Done, "B is not done yet → blocker is open")

	// Reverse lookup: B's dependents include A.
	deps, err := repo.Dependents(ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{a.ID}, deps)

	// Self-dependency is rejected.
	err = repo.AddDependency(ctx, a.ID, a.ID)
	require.Error(t, err)
	assert.ErrorIs(t, err, task.ErrSelfDependency)

	// Re-adding the same edge is idempotent (ErrDependencyExists).
	err = repo.AddDependency(ctx, a.ID, b.ID)
	require.Error(t, err)
	assert.ErrorIs(t, err, task.ErrDependencyExists)

	// RemoveDependency + re-add cycle.
	require.NoError(t, repo.RemoveDependency(ctx, a.ID, b.ID))
	got, err = repo.Blockers(ctx, a.ID)
	require.NoError(t, err)
	assert.Empty(t, got)

	// Idempotent remove (no error when nothing matches).
	require.NoError(t, repo.RemoveDependency(ctx, a.ID, b.ID))

	// SetTaskDependencies replaces the set.
	require.NoError(t, repo.SetTaskDependencies(ctx, a.ID, []string{b.ID, c.ID}))
	got, err = repo.Blockers(ctx, a.ID)
	require.NoError(t, err)
	require.Len(t, got, 2)
	// Ordered by title ASC.
	assert.Equal(t, "B", got[0].Title)
	assert.Equal(t, "C", got[1].Title)

	// Clear with empty list.
	require.NoError(t, repo.SetTaskDependencies(ctx, a.ID, nil))
	got, err = repo.Blockers(ctx, a.ID)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestTaskRepo_Dependencies_DoneFlag verifies that blockers whose
// target has status='done' (or a non-null completed_at) are reported
// as satisfied (Done=true), even though the edge is still in the
// graph. The implementor filters open blockers when "is blocked?"
// matters; the repo just returns the truth.
func TestTaskRepo_Dependencies_DoneFlag(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)
	ctx := context.Background()

	a := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "A"}
	b := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "B"}
	require.NoError(t, repo.Create(ctx, a))
	require.NoError(t, repo.Create(ctx, b))
	require.NoError(t, repo.AddDependency(ctx, a.ID, b.ID))

	got, err := repo.Blockers(ctx, a.ID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.False(t, got[0].Done)

	// Mark B done.
	b.Status = task.StatusDone
	require.NoError(t, repo.Update(ctx, b))

	got, err = repo.Blockers(ctx, a.ID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, got[0].Done, "B is done → blocker is satisfied")
}
