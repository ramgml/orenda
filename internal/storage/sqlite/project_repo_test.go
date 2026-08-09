package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/user"
)

func seedUserForProject(t *testing.T, db *sql.DB) *user.User {
	t.Helper()
	repo := NewUserRepository(db)
	u := &user.User{Email: uniqueEmail(t), PasswordHash: "x", DisplayName: "Owner"}
	require.NoError(t, repo.Create(context.Background(), u))
	return u
}

// uniqueEmail returns an email unique to the call site (test name + UUIDv7
// suffix) so multiple invocations within the same test don't collide on the
// UNIQUE(email) constraint.
func uniqueEmail(t *testing.T) string {
	t.Helper()
	suffix := strings.ReplaceAll(t.Name(), "/", "-")
	return "owner-" + suffix + "-" + newUUID() + "@x.com"
}

func TestProjectRepo_CreateDefaultsBoardAndColumns(t *testing.T) {
	db := setupUserDB(t)
	users := NewUserRepository(db)
	projects := NewProjectRepository(db)

	owner := &user.User{Email: "a@b.c", PasswordHash: "x", DisplayName: "Owner"}
	require.NoError(t, users.Create(context.Background(), owner))

	p, boards, cols, err := projects.CreateProject(context.Background(), &project.Project{
		Name:    "Orenda",
		OwnerID: owner.ID,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, p.ID)
	assert.False(t, p.CreatedAt.IsZero())

	require.Len(t, boards, 1)
	assert.Equal(t, p.ID, boards[0].ProjectID)

	require.Len(t, cols, len(project.DefaultColumns))
	for i, name := range project.DefaultColumns {
		assert.Equal(t, name, cols[i].Name)
	}
}

func TestProjectRepo_GetBoard_ReturnsOrderedColumns(t *testing.T) {
	db := setupUserDB(t)
	owner := seedUserForProject(t, db)
	projects := NewProjectRepository(db)

	_, _, _, err := projects.CreateProject(context.Background(), &project.Project{
		Name: "Orenda", OwnerID: owner.ID,
	})
	require.NoError(t, err)

	pList, err := projects.ListProjects(context.Background(), owner.ID)
	require.NoError(t, err)
	// After Phase 11 the system-created Inbox project also appears in
	// the list, so we just check that our project is there.
	var found *project.Project
	for _, p := range pList {
		if p.Name == "Orenda" {
			found = p
			break
		}
	}
	require.NotNil(t, found, "created project not in list")

	board, cols, err := projects.GetBoard(context.Background(), found.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, board.ID)
	require.NotEmpty(t, cols)
	for i := 1; i < len(cols); i++ {
		assert.Greater(t, cols[i].Position, cols[i-1].Position)
	}
}

func TestProjectRepo_UpdateAndDelete(t *testing.T) {
	db := setupUserDB(t)
	owner := seedUserForProject(t, db)
	projects := NewProjectRepository(db)

	p, _, _, err := projects.CreateProject(context.Background(), &project.Project{
		Name: "Orenda", OwnerID: owner.ID,
	})
	require.NoError(t, err)

	p.Name = "Orenda 2"
	require.NoError(t, projects.UpdateProject(context.Background(), p))
	got, err := projects.GetProject(context.Background(), p.ID)
	require.NoError(t, err)
	assert.Equal(t, "Orenda 2", got.Name)

	require.NoError(t, projects.DeleteProject(context.Background(), p.ID))
	_, err = projects.GetProject(context.Background(), p.ID)
	assert.ErrorIs(t, err, project.ErrNotFound)
}

func TestProjectRepo_GetProject_NotFound(t *testing.T) {
	db := setupUserDB(t)
	projects := NewProjectRepository(db)

	_, err := projects.GetProject(context.Background(), "no-such")
	assert.ErrorIs(t, err, project.ErrNotFound)
}
