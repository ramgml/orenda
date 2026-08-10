package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/user"
)

// setupUserDB opens a temporary DB, applies migrations, and returns the
// *sql.DB handle. Repositories are constructed separately by each test.
func setupUserDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(context.Background(), filepath.Join(dir, "test.db"), OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	require.NoError(t, Migrate(context.Background(), db, MigrationsFS, "migrations"))
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestUserRepo_CreateAndGet(t *testing.T) {
	db := setupUserDB(t)
	repo := NewUserRepository(db)

	u := &user.User{
		Email:        "alice@example.com",
		PasswordHash: "bcrypt$hash",
		DisplayName:  "Alice",
	}
	require.NoError(t, repo.Create(context.Background(), u))
	assert.NotEmpty(t, u.ID, "Create must assign ID")
	assert.False(t, u.CreatedAt.IsZero())

	got, err := repo.GetByID(context.Background(), u.ID)
	require.NoError(t, err)
	assert.Equal(t, u.Email, got.Email)
	assert.Equal(t, u.DisplayName, got.DisplayName)
	assert.Equal(t, user.RoleOwner, got.Role)
}

func TestUserRepo_GetByEmail_CaseInsensitive(t *testing.T) {
	db := setupUserDB(t)
	repo := NewUserRepository(db)

	err := repo.Create(context.Background(), &user.User{
		Email:        "Bob@Example.com",
		PasswordHash: "x",
		DisplayName:  "Bob",
	})
	require.NoError(t, err)

	// Lookup with a different case still finds the row.
	got, err := repo.GetByEmail(context.Background(), "BOB@example.COM")
	require.NoError(t, err)
	// Email is normalised to lowercase on insert.
	assert.Equal(t, "bob@example.com", got.Email)
}

func TestUserRepo_Create_DuplicateEmail(t *testing.T) {
	db := setupUserDB(t)
	repo := NewUserRepository(db)

	err := repo.Create(context.Background(), &user.User{
		Email:        "x@y.z",
		PasswordHash: "x",
		DisplayName:  "x",
	})
	require.NoError(t, err)

	err = repo.Create(context.Background(), &user.User{
		Email:        "X@Y.z",
		PasswordHash: "y",
		DisplayName:  "y",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, user.ErrEmailTaken)
}

func TestUserRepo_Update(t *testing.T) {
	db := setupUserDB(t)
	repo := NewUserRepository(db)

	u := &user.User{Email: "a@b.c", PasswordHash: "x", DisplayName: "Alice"}
	require.NoError(t, repo.Create(context.Background(), u))

	u.DisplayName = "Alice 2"
	require.NoError(t, repo.Update(context.Background(), u))

	got, err := repo.GetByID(context.Background(), u.ID)
	require.NoError(t, err)
	assert.Equal(t, "Alice 2", got.DisplayName)
	assert.True(t, !got.UpdatedAt.Before(got.CreatedAt),
		"updated_at should be >= created_at, got created=%v updated=%v",
		got.CreatedAt, got.UpdatedAt)
}

func TestUserRepo_Delete(t *testing.T) {
	db := setupUserDB(t)
	repo := NewUserRepository(db)

	u := &user.User{Email: "a@b.c", PasswordHash: "x", DisplayName: "Alice"}
	require.NoError(t, repo.Create(context.Background(), u))
	require.NoError(t, repo.Delete(context.Background(), u.ID))

	_, err := repo.GetByID(context.Background(), u.ID)
	assert.ErrorIs(t, err, user.ErrNotFound)
}

func TestUserRepo_GetByID_NotFound(t *testing.T) {
	db := setupUserDB(t)
	repo := NewUserRepository(db)

	_, err := repo.GetByID(context.Background(), "no-such-id")
	assert.ErrorIs(t, err, user.ErrNotFound)
}

func TestUserRepo_Delete_NotFound(t *testing.T) {
	db := setupUserDB(t)
	repo := NewUserRepository(db)

	err := repo.Delete(context.Background(), "no-such-id")
	assert.ErrorIs(t, err, user.ErrNotFound)
}

func TestUserRepo_List_EmptyAndOrdered(t *testing.T) {
	db := setupUserDB(t)
	repo := NewUserRepository(db)

	// Migrations 012 seeds a system-inbox user, so List() returns at
	// least one row even on a freshly migrated DB. We don't assert
	// the exact contents of the seed here — that's covered in the
	// migration tests — only that List returns the rows in
	// created_at ASC order.
	initial, err := repo.List(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, initial, "migrations should seed at least the system-inbox placeholder")

	// Insert two human users; expect them ordered by created_at then id.
	first := &user.User{Email: "first@x.io", PasswordHash: "h", DisplayName: "First"}
	require.NoError(t, repo.Create(context.Background(), first))
	second := &user.User{Email: "second@x.io", PasswordHash: "h", DisplayName: "Second"}
	require.NoError(t, repo.Create(context.Background(), second))

	users, err := repo.List(context.Background())
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(users), 3)

	// The two we just inserted must appear at the tail in order.
	last2 := users[len(users)-2:]
	assert.Equal(t, "first@x.io", last2[0].Email, "first-created must come first")
	assert.Equal(t, "second@x.io", last2[1].Email)
}

func TestUserRepo_List_ReturnsNonNil(t *testing.T) {
	db := setupUserDB(t)
	repo := NewUserRepository(db)

	users, err := repo.List(context.Background())
	require.NoError(t, err)
	// Even with the system-inbox seed present, callers must receive a
	// non-nil slice — never `nil`, which would force every caller to
	// guard against a null range.
	assert.NotNil(t, users)
}
