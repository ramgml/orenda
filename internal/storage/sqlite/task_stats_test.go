package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
)

// Phase 17: aggregate counters on the list endpoint.
//
// The card UI needs comments/attachments/children/checklist_items per
// task without a per-card fetch; ListByProjectWithStats runs the
// aggregates once and stamps them on each Task.
//
// Cases here:
//   - empty result set → empty slice, no error
//   - tasks with no activity → zero counters (not missing keys)
//   - tasks with mixed activity → each metric counted correctly
//   - blockers count: open blockers counted, satisfied ones ignored
func TestTaskRepo_ListByProjectWithStats(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)
	ctx := context.Background()

	// Two top-level tasks ("A", "B") and a child of A.
	a := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "A"}
	b := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "B"}
	require.NoError(t, repo.Create(ctx, a))
	require.NoError(t, repo.Create(ctx, b))
	child1 := &task.Task{ProjectID: p.ID, Title: "child1", Status: task.StatusDone}
	child2 := &task.Task{ProjectID: p.ID, Title: "child2"}
	child1.ParentTaskID = a.ID
	child2.ParentTaskID = a.ID
	require.NoError(t, repo.Create(ctx, child1))
	require.NoError(t, repo.Create(ctx, child2))

	// Activity on A: 2 comments, 1 attachment, checklist of 3 (1 done).
	_, err := db.ExecContext(ctx, `INSERT INTO comments(id, target_type, target_id, author_type, author_id, body_md) VALUES
		('c1','task', ?, 'user', 'u1', 'first'), ('c2','task', ?, 'user', 'u1', 'second')`, a.ID, a.ID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO attachments(id, target_type, target_id, filename, mime, size, path, sha256, uploaded_by_type, uploaded_by_id) VALUES
		('att1','task', ?, 'a.txt', 'text/plain', 1, '/p', 'x', 'user', 'u1')`, a.ID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO checklists(id, task_id, title, position) VALUES
		('cl1', ?, 'list', 1)`, a.ID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO checklist_items(id, checklist_id, title, done, position) VALUES
		('ci1','cl1','a',1,1), ('ci2','cl1','b',0,2), ('ci3','cl1','c',0,3)`)
	require.NoError(t, err)

	// Blocker: B blocks A. B is not done → A.BlockedByCount = 1.
	require.NoError(t, repo.AddDependency(ctx, a.ID, b.ID))

	// Filter to top-level only — children are counted via the
	// aggregate but don't appear in the list (the kanban shows
	// top-level cards).
	top := ""
	got, err := repo.ListByProjectWithStats(ctx, task.Filter{
		ProjectID:    p.ID,
		ParentTaskID: &top,
	})
	require.NoError(t, err)
	require.Len(t, got, 2)

	byID := map[string]*task.Task{}
	for _, t := range got {
		byID[t.ID] = t
	}

	// A has all the activity.
	aGot := byID[a.ID]
	require.NotNil(t, aGot.Counters, "A should have counters populated")
	c := aGot.Counters
	assert.Equal(t, 2, c.Comments)
	assert.Equal(t, 1, c.Attachments)
	assert.Equal(t, 2, c.ChildrenTotal)
	assert.Equal(t, 1, c.ChildrenDone)
	assert.Equal(t, 3, c.ChecklistTotal)
	assert.Equal(t, 1, c.ChecklistDone)
	assert.Equal(t, 1, aGot.BlockedByCount, "A blocked by B (open)")

	// B has no activity but still gets a zero-valued (not nil) Counters
	// so the UI can render without nil-checks.
	bGot := byID[b.ID]
	require.NotNil(t, bGot.Counters)
	assert.Equal(t, 0, bGot.Counters.Comments)
	assert.Equal(t, 0, bGot.BlockedByCount, "B has no blockers")

	// Mark B done; A's blocked-by should drop to 0 on next call.
	b.Status = task.StatusDone
	require.NoError(t, repo.Update(ctx, b))
	got2, err := repo.ListByProjectWithStats(ctx, task.Filter{ProjectID: p.ID})
	require.NoError(t, err)
	for _, tk := range got2 {
		if tk.ID == a.ID {
			assert.Equal(t, 0, tk.BlockedByCount, "B is done → A no longer blocked")
		}
	}
}

// Empty result: zero tasks, no error.
func TestTaskRepo_ListByProjectWithStats_Empty(t *testing.T) {
	db := setupUserDB(t)
	repo := NewTaskRepository(db)
	got, err := repo.ListByProjectWithStats(context.Background(), task.Filter{ProjectID: "no-such-project"})
	require.NoError(t, err)
	assert.Empty(t, got)
}
