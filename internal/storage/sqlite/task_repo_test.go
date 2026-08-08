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

func TestTaskRepo_Subtasks(t *testing.T) {
	db := setupUserDB(t)
	p, col := setupTaskProject(t, db)
	repo := NewTaskRepository(db)

	parent := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "parent"}
	require.NoError(t, repo.Create(context.Background(), parent))

	sub1 := &task.Subtask{TaskID: parent.ID, Title: "first", Position: 0}
	sub2 := &task.Subtask{TaskID: parent.ID, Title: "second", Position: 1}
	require.NoError(t, repo.AddSubtask(context.Background(), sub1))
	require.NoError(t, repo.AddSubtask(context.Background(), sub2))
	assert.NotEmpty(t, sub1.ID)

	subs, err := repo.ListSubtasks(context.Background(), parent.ID)
	require.NoError(t, err)
	assert.Len(t, subs, 2)
	assert.Equal(t, "first", subs[0].Title)

	sub1.Done = true
	require.NoError(t, repo.UpdateSubtask(context.Background(), sub1))
	require.NoError(t, repo.DeleteSubtask(context.Background(), sub2.ID))

	subs, err = repo.ListSubtasks(context.Background(), parent.ID)
	require.NoError(t, err)
	assert.Len(t, subs, 1)
	assert.True(t, subs[0].Done)
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
