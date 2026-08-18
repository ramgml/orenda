package sqlite

// Storage tests for project_activity (migration 024) —
// wiki:agent-project-description. Mirrors course_activity_test.go:
// round-trip, limit, Validate enforcement, null payload, and
// ON DELETE CASCADE against the projects FK.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/user"
)

// seedProjectForActivity inserts an owner + a project row through the
// real repositories so the activity rows have a live FK target.
func seedProjectForActivity(t *testing.T, db interface {
	CreateProject(ctx context.Context, p *project.Project) (*project.Project, []*project.Board, []*project.Column, error)
}, ownerID string) *project.Project {
	t.Helper()
	p, _, _, err := db.CreateProject(context.Background(), &project.Project{
		Name:    "Proj",
		Color:   project.DefaultColor,
		OwnerID: ownerID,
	})
	require.NoError(t, err)
	return p
}

func TestProjectActivity_CreateAndList(t *testing.T) {
	db := openStudyTestDB(t)
	defer db.Close()

	users := NewUserRepository(db)
	owner := &user.User{Email: uniqueEmail(t), PasswordHash: "x", DisplayName: "Owner"}
	require.NoError(t, users.Create(context.Background(), owner))
	projects := NewProjectRepository(db)
	p := seedProjectForActivity(t, projects, owner.ID)

	repo := NewProjectActivityRepository(db)
	now := time.Now().UTC()

	older := &project.Activity{
		ID:        "pa-1",
		ProjectID: p.ID,
		ActorType: project.ActorAgent,
		ActorID:   "a-1",
		Kind:      project.ActivityDescriptionChanged,
		Payload:   `{"before":"a","after":"b"}`,
		CreatedAt: now.Add(-1 * time.Minute),
	}
	newer := &project.Activity{
		ID:        "pa-2",
		ProjectID: p.ID,
		ActorType: project.ActorAgent,
		ActorID:   "a-1",
		Kind:      project.ActivityDescriptionChanged,
		Payload:   `{"before":"b","after":"c"}`,
		CreatedAt: now,
	}
	require.NoError(t, repo.Create(context.Background(), older))
	require.NoError(t, repo.Create(context.Background(), newer))

	rows, err := repo.ListByProject(context.Background(), p.ID, 50)
	require.NoError(t, err)
	require.Len(t, rows, 2, "both rows returned")
	assert.Equal(t, "pa-2", rows[0].ID, "newest first")
	assert.Equal(t, "pa-1", rows[1].ID)
	assert.Equal(t, project.ActivityDescriptionChanged, rows[0].Kind)
	assert.Equal(t, project.ActorAgent, rows[0].ActorType)
	assert.Equal(t, `{"before":"b","after":"c"}`, rows[0].Payload)
}

func TestProjectActivity_ListByProject_RespectsLimit(t *testing.T) {
	db := openStudyTestDB(t)
	defer db.Close()

	users := NewUserRepository(db)
	owner := &user.User{Email: uniqueEmail(t), PasswordHash: "x", DisplayName: "Owner"}
	require.NoError(t, users.Create(context.Background(), owner))
	projects := NewProjectRepository(db)
	p := seedProjectForActivity(t, projects, owner.ID)

	repo := NewProjectActivityRepository(db)
	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		require.NoError(t, repo.Create(context.Background(), &project.Activity{
			ID:        "pa-" + string(rune('a'+i)),
			ProjectID: p.ID,
			ActorType: project.ActorAgent,
			ActorID:   "a-1",
			Kind:      project.ActivityDescriptionChanged,
			Payload:   "x",
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		}))
	}
	rows, err := repo.ListByProject(context.Background(), p.ID, 2)
	require.NoError(t, err)
	assert.Len(t, rows, 2, "limit bounds the result")
	assert.Equal(t, "pa-e", rows[0].ID)
	assert.Equal(t, "pa-d", rows[1].ID)
}

func TestProjectActivity_ValidateEnforced(t *testing.T) {
	db := openStudyTestDB(t)
	defer db.Close()
	repo := NewProjectActivityRepository(db)

	badActor := &project.Activity{
		ID:        "pa-bad-actor",
		ProjectID: "p",
		ActorType: project.ActorType("alien"),
		ActorID:   "a-1",
		Kind:      project.ActivityDescriptionChanged,
		CreatedAt: time.Now().UTC(),
	}
	err := repo.Create(context.Background(), badActor)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "actor_type")

	badKind := &project.Activity{
		ID:        "pa-bad-kind",
		ProjectID: "p",
		ActorType: project.ActorAgent,
		ActorID:   "a-1",
		Kind:      project.ActivityKind("mystery"),
		CreatedAt: time.Now().UTC(),
	}
	err = repo.Create(context.Background(), badKind)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kind")

	missingProject := &project.Activity{
		ID:        "pa-no-proj",
		ProjectID: "",
		ActorType: project.ActorAgent,
		ActorID:   "a-1",
		Kind:      project.ActivityDescriptionChanged,
		CreatedAt: time.Now().UTC(),
	}
	err = repo.Create(context.Background(), missingProject)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "project_id")
}

func TestProjectActivity_NilPayload(t *testing.T) {
	db := openStudyTestDB(t)
	defer db.Close()

	users := NewUserRepository(db)
	owner := &user.User{Email: uniqueEmail(t), PasswordHash: "x", DisplayName: "Owner"}
	require.NoError(t, users.Create(context.Background(), owner))
	projects := NewProjectRepository(db)
	p := seedProjectForActivity(t, projects, owner.ID)

	repo := NewProjectActivityRepository(db)
	require.NoError(t, repo.Create(context.Background(), &project.Activity{
		ID:        "pa-nil",
		ProjectID: p.ID,
		ActorType: project.ActorAgent,
		ActorID:   "a-1",
		Kind:      project.ActivityDescriptionChanged,
		CreatedAt: time.Now().UTC(),
	}))
	rows, err := repo.ListByProject(context.Background(), p.ID, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "", rows[0].Payload, "empty payload reads back as empty string")
}

func TestProjectActivity_CascadeDelete(t *testing.T) {
	db := openStudyTestDB(t)
	defer db.Close()

	users := NewUserRepository(db)
	owner := &user.User{Email: uniqueEmail(t), PasswordHash: "x", DisplayName: "Owner"}
	require.NoError(t, users.Create(context.Background(), owner))
	projects := NewProjectRepository(db)
	p := seedProjectForActivity(t, projects, owner.ID)

	repo := NewProjectActivityRepository(db)
	require.NoError(t, repo.Create(context.Background(), &project.Activity{
		ID:        "pa-cascade",
		ProjectID: p.ID,
		ActorType: project.ActorAgent,
		ActorID:   "a-1",
		Kind:      project.ActivityDescriptionChanged,
		CreatedAt: time.Now().UTC(),
	}))
	rows, err := repo.ListByProject(context.Background(), p.ID, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)

	// Deleting the project cascades to its activity rows.
	require.NoError(t, projects.DeleteProject(context.Background(), p.ID))
	rows, err = repo.ListByProject(context.Background(), p.ID, 10)
	require.NoError(t, err)
	assert.Empty(t, rows, "activity rows should cascade-delete with project")
}
