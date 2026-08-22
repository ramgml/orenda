package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/project"
)

// TestProjectRepo_NumberAssignedSequentially pins the Phase-36 contract:
// every CreateProject draws COALESCE(MAX(number),0)+1, so numbers are
// 1-based and monotonically increasing in creation order.
func TestProjectRepo_NumberAssignedSequentially(t *testing.T) {
	db := setupUserDB(t)
	owner := seedUserForProject(t, db)
	repo := NewProjectRepository(db)
	ctx := context.Background()

	prev := 0
	for i := 0; i < 5; i++ {
		p := &project.Project{Name: "proj", OwnerID: owner.ID}
		_, _, _, err := repo.CreateProject(ctx, p)
		require.NoError(t, err)
		assert.Equal(t, prev+1, p.Number, "numbers must be sequential")
		prev = p.Number
	}
	assert.Equal(t, 5, prev)
}

// TestProjectRepo_NumberNeverReused: deleting a project must NOT free its
// number — a "P7" reference in a commit message or branch name has to
// keep pointing at the same (now deleted) project forever.
func TestProjectRepo_NumberNeverReused(t *testing.T) {
	db := setupUserDB(t)
	owner := seedUserForProject(t, db)
	repo := NewProjectRepository(db)
	ctx := context.Background()

	mk := func() *project.Project {
		p := &project.Project{Name: "proj", OwnerID: owner.ID}
		_, _, _, err := repo.CreateProject(ctx, p)
		require.NoError(t, err)
		return p
	}

	t.Run("delete head", func(t *testing.T) {
		a, b, c := mk(), mk(), mk()
		require.NoError(t, repo.DeleteProject(ctx, c.ID)) // delete the max
		d := mk()
		assert.Equal(t, c.Number+1, d.Number,
			"after deleting the newest project its number must stay burned")
		_, err := repo.GetByNumber(ctx, c.Number)
		assert.ErrorIs(t, err, project.ErrNotFound)
		_ = a
		_ = b
	})

	t.Run("delete middle", func(t *testing.T) {
		a, b, c := mk(), mk(), mk()
		require.NoError(t, repo.DeleteProject(ctx, b.ID)) // hole in the sequence
		d := mk()
		assert.Equal(t, c.Number+1, d.Number,
			"a hole in the sequence must not pull the next number down")
		_ = a
	})
}

// TestProjectRepo_GetByNumber: the "P<N>" lookup — hit and miss.
func TestProjectRepo_GetByNumber(t *testing.T) {
	db := setupUserDB(t)
	owner := seedUserForProject(t, db)
	repo := NewProjectRepository(db)
	ctx := context.Background()

	p := &project.Project{Name: "by number", OwnerID: owner.ID}
	_, _, _, err := repo.CreateProject(ctx, p)
	require.NoError(t, err)
	require.Greater(t, p.Number, 0)

	got, err := repo.GetByNumber(ctx, p.Number)
	require.NoError(t, err)
	assert.Equal(t, p.ID, got.ID)
	assert.Equal(t, p.Number, got.Number)
	assert.Equal(t, "by number", got.Name)

	_, err = repo.GetByNumber(ctx, p.Number+1000)
	assert.ErrorIs(t, err, project.ErrNotFound)
}

// TestProjectRepo_NumberVsUUIDNoCollision: a numeric ref and a UUID are
// disjoint namespaces — GetProject never matches a number string and the
// UUID (which always contains '-' and hex letters) is never mistaken
// for a number by ParseProjectRef.
func TestProjectRepo_NumberVsUUIDNoCollision(t *testing.T) {
	db := setupUserDB(t)
	owner := seedUserForProject(t, db)
	repo := NewProjectRepository(db)
	ctx := context.Background()

	p := &project.Project{Name: "uuid", OwnerID: owner.ID}
	_, _, _, err := repo.CreateProject(ctx, p)
	require.NoError(t, err)

	// The UUID resolves by id...
	byID, err := repo.GetProject(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, p.Number, byID.Number)

	// ...and is never parsed as a number reference.
	_, ok := project.ParseProjectRef(p.ID)
	assert.False(t, ok, "UUID must not parse as a number ref: %q", p.ID)

	// The string form of the number resolves by number.
	got, err := repo.GetByNumber(ctx, p.Number)
	require.NoError(t, err)
	assert.Equal(t, p.ID, got.ID)
}
