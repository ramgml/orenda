package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
)

// setupTaskProject is a copy of the helper used by task_repo_test.go; the
// activity table has a FK to tasks so we need a real task row.
func setupTaskProjectForActivity(t *testing.T, db *sql.DB) (*project.Project, *project.Column) {
	t.Helper()
	users := NewUserRepository(db)
	owner := &user.User{
		Email:        "act-" + t.Name() + "-" + newUUID()[:8] + "@x.com",
		PasswordHash: "x", DisplayName: "Owner",
	}
	require.NoError(t, users.Create(context.Background(), owner))
	projRepo := NewProjectRepository(db)
	p, _, cols, err := projRepo.CreateProject(context.Background(), &project.Project{
		Name: "Orenda", OwnerID: owner.ID,
	})
	require.NoError(t, err)
	return p, cols[0]
}

func TestActivityRepo_CreateAndListByTask(t *testing.T) {
	db := setupAgentDB(t)
	p, col := setupTaskProjectForActivity(t, db)
	taskRepo := NewTaskRepository(db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "x"}
	require.NoError(t, taskRepo.Create(context.Background(), tr))

	repo := NewActivityRepository(db)
	for i := 0; i < 3; i++ {
		require.NoError(t, repo.Create(context.Background(), &activity.Activity{
			TaskID: tr.ID, ActorID: "u-1", Action: activity.ActionCreated,
			Payload: "{}",
		}))
	}
	got, err := repo.ListByTask(context.Background(), tr.ID)
	require.NoError(t, err)
	assert.Len(t, got, 3)
	for _, a := range got {
		assert.Equal(t, tr.ID, a.TaskID)
		assert.False(t, a.CreatedAt.IsZero())
	}
}

func TestActivityRepo_ListByActor(t *testing.T) {
	db := setupAgentDB(t)
	p, col := setupTaskProjectForActivity(t, db)
	taskRepo := NewTaskRepository(db)

	repo := NewActivityRepository(db)

	for i, taskID := range []string{"t-1", "t-2", "t-3"} {
		tr := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: taskID}
		require.NoError(t, taskRepo.Create(context.Background(), tr))
		_ = i
		// Use the real task ID for FK satisfaction.
	}

	// Re-fetch task IDs from the DB (Create generates UUIDs).
	allTasks, err := taskRepo.ListByProject(context.Background(), task.Filter{ProjectID: p.ID})
	require.NoError(t, err)
	require.Len(t, allTasks, 3)

	require.NoError(t, repo.Create(context.Background(), &activity.Activity{
		TaskID: allTasks[0].ID, ActorType: activity.ActorAgent, ActorID: "a-1", Action: activity.ActionClaimed,
	}))
	require.NoError(t, repo.Create(context.Background(), &activity.Activity{
		TaskID: allTasks[1].ID, ActorType: activity.ActorUser, ActorID: "u-1", Action: activity.ActionCreated,
	}))
	require.NoError(t, repo.Create(context.Background(), &activity.Activity{
		TaskID: allTasks[2].ID, ActorType: activity.ActorAgent, ActorID: "a-1", Action: activity.ActionSubmitted,
	}))

	got, err := repo.ListByActor(context.Background(), activity.ActorAgent, "a-1")
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestActivityRepo_ValidateError(t *testing.T) {
	db := setupAgentDB(t)
	repo := NewActivityRepository(db)

	err := repo.Create(context.Background(), &activity.Activity{TaskID: "t", ActorID: "u", Action: ""})
	require.Error(t, err)
}
