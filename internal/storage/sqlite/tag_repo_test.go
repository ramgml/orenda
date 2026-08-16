package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
)

// TestTaskRepo_Tags_CRUD covers the global tag catalogue: create
// (with id auto-assignment), list ordered by name, get-by-id,
// update, and delete.
func TestTaskRepo_Tags_CRUD(t *testing.T) {
	db := setupUserDB(t)
	repo := NewTaskRepository(db)

	// Create.
	a := &task.Tag{Name: "frontend", Color: "#22c55e"}
	require.NoError(t, repo.CreateTag(context.Background(), a))
	assert.NotEmpty(t, a.ID)

	b := &task.Tag{Name: "backend"}
	require.NoError(t, repo.CreateTag(context.Background(), b))
	assert.NotEmpty(t, b.ID)

	// Duplicate name fails — repository surfaces the FK/UNIQUE
	// violation verbatim; the handler translates to 409.
	dup := &task.Tag{Name: "frontend"}
	assert.Error(t, repo.CreateTag(context.Background(), dup))

	// List — alphabetical.
	all, err := repo.ListTags(context.Background())
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "backend", all[0].Name)
	assert.Equal(t, "frontend", all[1].Name)
	assert.Equal(t, "#22c55e", all[1].Color)
	assert.Equal(t, "", all[0].Color, "missing colour scans as empty string")

	// Get by id.
	got, err := repo.GetTagByID(context.Background(), a.ID)
	require.NoError(t, err)
	assert.Equal(t, "frontend", got.Name)

	_, err = repo.GetTagByID(context.Background(), "no-such")
	assert.ErrorIs(t, err, task.ErrNotFound)

	// Update — change colour, keep name.
	a.Color = "#0ea5e9"
	require.NoError(t, repo.UpdateTag(context.Background(), a))
	got, err = repo.GetTagByID(context.Background(), a.ID)
	require.NoError(t, err)
	assert.Equal(t, "#0ea5e9", got.Color)

	// Delete — cascades to task_tags (none yet, but verifies the FK
	// behaviour).
	require.NoError(t, repo.DeleteTag(context.Background(), b.ID))
	all, err = repo.ListTags(context.Background())
	require.NoError(t, err)
	assert.Len(t, all, 1)

	// Delete missing → ErrNotFound.
	assert.ErrorIs(t, repo.DeleteTag(context.Background(), "no-such"), task.ErrNotFound)

	// Validate() rejects empty name.
	assert.ErrorIs(t,
		(&task.Tag{Name: ""}).Validate(),
		task.ErrInvalidInput,
	)
	// Validate() rejects too-long name (51 chars).
	long := make([]byte, 51)
	for i := range long {
		long[i] = 'a'
	}
	assert.ErrorIs(t,
		(&task.Tag{Name: string(long)}).Validate(),
		task.ErrInvalidInput,
	)
	// Validate() rejects malformed colour.
	assert.ErrorIs(t,
		(&task.Tag{Name: "ok", Color: "blue"}).Validate(),
		task.ErrInvalidInput,
	)
	assert.ErrorIs(t,
		(&task.Tag{Name: "ok", Color: "#xyz"}).Validate(),
		task.ErrInvalidInput,
	)
	// Validate() accepts #rgb and #rrggbb.
	assert.NoError(t, (&task.Tag{Name: "ok", Color: "#abc"}).Validate())
	assert.NoError(t, (&task.Tag{Name: "ok", Color: "#aabbcc"}).Validate())
}

// TestTaskRepo_SetTaskTags_ReplaceSemantics verifies the single
// transaction + replace-semantics contract:
//
//   - Attach A,B → task has [A,B]
//   - Replace with [C] → task has [C] (no A or B)
//   - Empty slice → task has []
//   - Duplicate ids in the input → no error, no duplicate rows
//   - Calling SetTaskTags with the same set as current → idempotent
//     (still succeeds, no rows added/removed)
func TestTaskRepo_SetTaskTags_ReplaceSemantics(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)

	tr := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "x"}
	require.NoError(t, repo.Create(context.Background(), tr))

	a := &task.Tag{Name: "alpha"}
	b := &task.Tag{Name: "bravo"}
	c := &task.Tag{Name: "charlie"}
	for _, tg := range []*task.Tag{a, b, c} {
		require.NoError(t, repo.CreateTag(context.Background(), tg))
	}

	// Attach A,B.
	require.NoError(t, repo.SetTaskTags(context.Background(), tr.ID, []string{a.ID, b.ID}))
	tags, err := repo.ListTagsForTask(context.Background(), tr.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t,
		[]string{"alpha", "bravo"},
		[]string{tags[0].Name, tags[1].Name},
	)

	// Replace with [C] only.
	require.NoError(t, repo.SetTaskTags(context.Background(), tr.ID, []string{c.ID}))
	tags, err = repo.ListTagsForTask(context.Background(), tr.ID)
	require.NoError(t, err)
	require.Len(t, tags, 1)
	assert.Equal(t, "charlie", tags[0].Name)

	// Empty slice → clear.
	require.NoError(t, repo.SetTaskTags(context.Background(), tr.ID, []string{}))
	tags, err = repo.ListTagsForTask(context.Background(), tr.ID)
	require.NoError(t, err)
	assert.Empty(t, tags)

	// Duplicate id is collapsed by the join PK.
	require.NoError(t, repo.SetTaskTags(context.Background(), tr.ID, []string{a.ID, a.ID, a.ID}))
	tags, err = repo.ListTagsForTask(context.Background(), tr.ID)
	require.NoError(t, err)
	assert.Len(t, tags, 1)
	assert.Equal(t, "alpha", tags[0].Name)

	// Idempotent re-apply: same set, no error.
	require.NoError(t, repo.SetTaskTags(context.Background(), tr.ID, []string{a.ID}))
	require.NoError(t, repo.SetTaskTags(context.Background(), tr.ID, []string{a.ID}))
	tags, err = repo.ListTagsForTask(context.Background(), tr.ID)
	require.NoError(t, err)
	assert.Len(t, tags, 1)
}

// TestTaskRepo_SetTaskTags_TransactionalFailure verifies that a bad
// id (FK violation) rolls back the whole replacement rather than
// leaving the task half-tagged.
//
// We seed a tag id that exists, then pass a deliberately bogus
// second id; the FK on task_tags.tag_id should reject it. The
// expected behaviour is that the previously-attached tag remains
// after the failed call (rollback worked).
func TestTaskRepo_SetTaskTags_FKViolationRollback(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)

	tr := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "x"}
	require.NoError(t, repo.Create(context.Background(), tr))

	a := &task.Tag{Name: "alpha"}
	require.NoError(t, repo.CreateTag(context.Background(), a))

	// Pre-existing attachment.
	require.NoError(t, repo.SetTaskTags(context.Background(), tr.ID, []string{a.ID}))

	// Now attempt a replacement with one valid + one bogus id.
	err := repo.SetTaskTags(context.Background(), tr.ID, []string{a.ID, "deadbeef-0000-0000-0000-000000000000"})
	assert.Error(t, err, "bogus tag id should surface as FK error")

	// After failure, the previous tag should still be there (rollback).
	tags, lerr := repo.ListTagsForTask(context.Background(), tr.ID)
	require.NoError(t, lerr)
	assert.Len(t, tags, 1, "rollback should preserve the prior attachment")
	assert.Equal(t, "alpha", tags[0].Name)
}

// TestTaskRepo_TagsForTasks_Batch verifies the batch shape used by
// the kanban list endpoint (Phase 17 will rely on this for
// per-card enrichment).

// TestTaskRepo_TagsForTasks_Batch verifies the batch shape used by
// the kanban list endpoint (Phase 17 will rely on this for
// per-card enrichment).
func TestTaskRepo_TagsForTasks_Batch(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)

	t1 := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "t1"}
	t2 := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "t2"}
	t3 := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "t3"}
	for _, tt := range []*task.Task{t1, t2, t3} {
		require.NoError(t, repo.Create(context.Background(), tt))
	}

	a := &task.Tag{Name: "alpha"}
	b := &task.Tag{Name: "bravo"}
	for _, tg := range []*task.Tag{a, b} {
		require.NoError(t, repo.CreateTag(context.Background(), tg))
	}
	// t1 → [a,b], t2 → [a], t3 → none.
	require.NoError(t, repo.SetTaskTags(context.Background(), t1.ID, []string{a.ID, b.ID}))
	require.NoError(t, repo.SetTaskTags(context.Background(), t2.ID, []string{a.ID}))

	got, err := repo.TagsForTasks(context.Background(), []string{t1.ID, t2.ID, t3.ID})
	require.NoError(t, err)

	// t1 has both (alpha, bravo).
	require.Len(t, got[t1.ID], 2)
	names := []string{got[t1.ID][0].Name, got[t1.ID][1].Name}
	assert.ElementsMatch(t, []string{"alpha", "bravo"}, names)

	// t2 has just alpha.
	require.Len(t, got[t2.ID], 1)
	assert.Equal(t, "alpha", got[t2.ID][0].Name)

	// t3 has none but the key should exist with empty slice.
	_, ok := got[t3.ID]
	assert.True(t, ok, "every input task id must be present in the result")
	assert.Empty(t, got[t3.ID])

	// Empty input → empty result (no DB round-trip needed).
	empty, err := repo.TagsForTasks(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}
