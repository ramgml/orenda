package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
)

// setupTaskProject creates a user + project + first default column and
// returns the trio for task-related tests.
func setupTaskProject(t *testing.T, db *sql.DB) (*project.Project, *project.Column) {
	t.Helper()
	owner := seedUserForProject(t, db)
	projects := NewProjectRepository(db)
	p, _, cols, err := projects.CreateProject(context.Background(), &project.Project{
		Name:    "Orenda",
		OwnerID: owner.ID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, cols)
	return p, cols[0]
}

func TestTaskRepo_CreateAssignsIDAndDefaults(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)

	tr := &task.Task{
		ProjectID: p.ID,
		ColumnID:  col.ID,
		Title:     "Implement login",
	}
	require.NoError(t, repo.Create(context.Background(), tr))
	assert.NotEmpty(t, tr.ID)
	assert.Equal(t, task.StatusTodo, tr.Status)
	assert.Equal(t, task.PriorityMedium, tr.Priority)
	assert.Equal(t, task.AwaitingNone, tr.Awaiting)
	assert.False(t, tr.CreatedAt.IsZero())
}

func TestTaskRepo_GetByID_NotFound(t *testing.T) {
	db := setupUserDB(t)
	repo := NewTaskRepository(db)

	_, err := repo.GetByID(context.Background(), "no-such")
	assert.ErrorIs(t, err, task.ErrNotFound)
}

func TestTaskRepo_Update(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)

	tr := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "x"}
	require.NoError(t, repo.Create(context.Background(), tr))

	tr.Title = "renamed"
	tr.Priority = task.PriorityHigh
	require.NoError(t, repo.Update(context.Background(), tr))
	assert.Equal(t, "renamed", tr.Title)
	assert.Equal(t, task.PriorityHigh, tr.Priority)
}

func TestTaskRepo_ListByProject_Ordered(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)

	for i := 0; i < 3; i++ {
		require.NoError(t, repo.Create(context.Background(), &task.Task{
			ProjectID: p.ID, ColumnID: col.ID,
			Title: "task", Position: float64(i) * 10,
		}))
	}

	got, err := repo.ListByProject(context.Background(), task.Filter{ProjectID: p.ID, ColumnID: col.ID})
	require.NoError(t, err)
	assert.Len(t, got, 3)
	for i := 1; i < len(got); i++ {
		assert.Greater(t, got[i].Position, got[i-1].Position)
	}
}

func TestTaskRepo_ListByProject_FilterByAssignee(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)

	owner := seedUserForProject(t, db)
	assigned := &task.Task{
		ProjectID: p.ID, ColumnID: col.ID, Title: "a",
		AssigneeType: task.AssigneeUser, AssigneeID: owner.ID,
	}
	unassigned := &task.Task{
		ProjectID: p.ID, ColumnID: col.ID, Title: "u",
	}
	require.NoError(t, repo.Create(context.Background(), assigned))
	require.NoError(t, repo.Create(context.Background(), unassigned))

	got, err := repo.ListByProject(context.Background(), task.Filter{
		ProjectID:    p.ID,
		AssigneeType: task.AssigneeUser,
		AssigneeID:   owner.ID,
	})
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, "a", got[0].Title)
}

func TestTaskRepo_Delete(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)

	tr := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "x"}
	require.NoError(t, repo.Create(context.Background(), tr))
	require.NoError(t, repo.Delete(context.Background(), tr.ID))

	_, err := repo.GetByID(context.Background(), tr.ID)
	assert.ErrorIs(t, err, task.ErrNotFound)
}

func TestTaskRepo_Children(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)

	parent := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "parent"}
	require.NoError(t, repo.Create(context.Background(), parent))

	// Three children, two of which are done. We expect the progress
	// helper to reflect that and ListChildren to return all three.
	child1 := &task.Task{ProjectID: p.ID, ParentTaskID: parent.ID, Title: "first", Position: 1, Status: task.StatusDone}
	child2 := &task.Task{ProjectID: p.ID, ParentTaskID: parent.ID, Title: "second", Position: 2, Status: task.StatusDone}
	child3 := &task.Task{ProjectID: p.ID, ParentTaskID: parent.ID, Title: "third", Position: 3, Status: task.StatusTodo}
	for _, c := range []*task.Task{child1, child2, child3} {
		require.NoError(t, repo.Create(context.Background(), c))
	}

	children, err := repo.ListChildren(context.Background(), parent.ID)
	require.NoError(t, err)
	assert.Len(t, children, 3)
	assert.Equal(t, "first", children[0].Title)

	total, done, err := repo.ChildProgress(context.Background(), parent.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Equal(t, 2, done)

	// Deleting the parent cascades to children (parent_task_id FK).
	// After Delete the parent row is gone, so ListChildren returns
	// ErrNotFound (we deliberately use ErrNotFound here, not an empty
	// slice, so callers can distinguish "no children" from "bad id").
	require.NoError(t, repo.Delete(context.Background(), parent.ID))
	_, err = repo.ListChildren(context.Background(), parent.ID)
	assert.ErrorIs(t, err, task.ErrNotFound)
	// But the children themselves are gone too.
	total, done, err = repo.ChildProgress(context.Background(), parent.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, total)
	assert.Equal(t, 0, done)
}

func TestTaskRepo_Children_ParentNotFound(t *testing.T) {
	db := setupUserDB(t)
	repo := NewTaskRepository(db)
	_, err := repo.ListChildren(context.Background(), "no-such-parent")
	assert.ErrorIs(t, err, task.ErrNotFound)
}

func TestTaskRepo_ListByProject_FilterTopLevel(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)

	top := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "top-level"}
	require.NoError(t, repo.Create(context.Background(), top))
	child := &task.Task{ProjectID: p.ID, ColumnID: col.ID, ParentTaskID: top.ID, Title: "nested"}
	require.NoError(t, repo.Create(context.Background(), child))

	// No filter: both rows are returned.
	all, err := repo.ListByProject(context.Background(), task.Filter{ProjectID: p.ID})
	require.NoError(t, err)
	assert.Len(t, all, 2)

	// Empty pointer (top-level only): child is excluded.
	empty := ""
	only, err := repo.ListByProject(context.Background(), task.Filter{
		ProjectID:    p.ID,
		ParentTaskID: &empty,
	})
	require.NoError(t, err)
	require.Len(t, only, 1)
	assert.Equal(t, top.ID, only[0].ID)

	// Specific parent id: only the nested child.
	onlyChild, err := repo.ListByProject(context.Background(), task.Filter{
		ProjectID:    p.ID,
		ParentTaskID: &top.ID,
	})
	require.NoError(t, err)
	require.Len(t, onlyChild, 1)
	assert.Equal(t, child.ID, onlyChild[0].ID)
}

func TestTaskRepo_Create_InvalidStatus(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)

	tr := &task.Task{
		ProjectID: p.ID, ColumnID: col.ID,
		Title:  "x",
		Status: task.Status("weird"),
	}
	err := repo.Create(context.Background(), tr)
	require.Error(t, err)
	assert.ErrorIs(t, err, task.ErrInvalidInput)
}
