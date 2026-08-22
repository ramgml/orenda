package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
)

// TestTaskRepo_NumberAssignedSequentially pins the Phase-33 contract:
// every Create draws COALESCE(MAX(number),0)+1, so numbers are 1-based
// and monotonically increasing in creation order.
func TestTaskRepo_NumberAssignedSequentially(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)
	ctx := context.Background()

	prev := 0
	for i := 0; i < 5; i++ {
		tr := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "t"}
		require.NoError(t, repo.Create(ctx, tr))
		assert.Equal(t, prev+1, tr.Number, "numbers must be sequential")
		prev = tr.Number
	}
	assert.Equal(t, 5, prev)
}

// TestTaskRepo_NumberNeverReused: deleting a task must NOT free its
// number — a "T42" reference in a commit message or branch name has
// to keep pointing at the same (now deleted) task forever.
func TestTaskRepo_NumberNeverReused(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)
	ctx := context.Background()

	mk := func() *task.Task {
		tr := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "t"}
		require.NoError(t, repo.Create(ctx, tr))
		return tr
	}

	t.Run("delete head", func(t *testing.T) {
		a, b, c := mk(), mk(), mk()
		require.NoError(t, repo.Delete(ctx, c.ID)) // delete the max
		d := mk()
		assert.Equal(t, c.Number+1, d.Number,
			"after deleting the newest task its number must stay burned")
		_, err := repo.GetByNumber(ctx, c.Number)
		assert.ErrorIs(t, err, task.ErrNotFound)
		_ = a
		_ = b
	})

	t.Run("delete middle", func(t *testing.T) {
		a, b, c := mk(), mk(), mk()
		require.NoError(t, repo.Delete(ctx, b.ID)) // hole in the sequence
		d := mk()
		assert.Equal(t, c.Number+1, d.Number,
			"a hole in the sequence must not pull the next number down")
		_ = a
	})
}

// TestTaskRepo_GetByNumber: the "T<N>" lookup — hit and miss.
func TestTaskRepo_GetByNumber(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)
	ctx := context.Background()

	tr := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "by number"}
	require.NoError(t, repo.Create(ctx, tr))
	require.Greater(t, tr.Number, 0)

	got, err := repo.GetByNumber(ctx, tr.Number)
	require.NoError(t, err)
	assert.Equal(t, tr.ID, got.ID)
	assert.Equal(t, tr.Number, got.Number)
	assert.Equal(t, "by number", got.Title)

	_, err = repo.GetByNumber(ctx, tr.Number+1000)
	assert.ErrorIs(t, err, task.ErrNotFound)
}

// TestTaskRepo_NumberVsUUIDNoCollision: a numeric ref and a UUID are
// disjoint namespaces — GetByID never matches a number string and the
// UUID (which always contains '-' and hex letters) is never mistaken
// for a number by ParseRefNumber.
func TestTaskRepo_NumberVsUUIDNoCollision(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)
	ctx := context.Background()

	tr := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "uuid"}
	require.NoError(t, repo.Create(ctx, tr))

	// The UUID resolves by id...
	byID, err := repo.GetByID(ctx, tr.ID)
	require.NoError(t, err)
	assert.Equal(t, tr.Number, byID.Number)

	// ...and is never parsed as a number reference.
	_, ok := task.ParseRefNumber(tr.ID)
	assert.False(t, ok, "UUID must not parse as a number ref: %q", tr.ID)

	// The string form of the number resolves by number.
	got, err := repo.GetByNumber(ctx, tr.Number)
	require.NoError(t, err)
	assert.Equal(t, tr.ID, got.ID)
}
