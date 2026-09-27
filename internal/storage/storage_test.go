package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// TestOpen_SQLite covers the happy path: driver defaults to sqlite,
// the handle reports its dialect and serves real queries.
func TestOpen_SQLite(t *testing.T) {
	db, err := Open(context.Background(), Config{
		Path:          filepath.Join(t.TempDir(), "seam.db"),
		WALMode:       true,
		EnableForeign: true,
		BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	assert.Equal(t, DialectSQLite, db.Dialect())
	var one int
	require.NoError(t, db.QueryRowContext(context.Background(), "SELECT 1").Scan(&one))
	assert.Equal(t, 1, one)
}

func TestOpen_SQLiteExplicitDriver(t *testing.T) {
	db, err := Open(context.Background(), Config{
		Driver: "sqlite",
		Path:   filepath.Join(t.TempDir(), "seam.db"),
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	assert.Equal(t, DialectSQLite, db.Dialect())
}

// TestOpen_PostgresNotImplemented pins the T360 gate: selecting
// driver=postgres yields a clear configuration error — no panic, no
// silent fallback to sqlite.
func TestOpen_PostgresNotImplemented(t *testing.T) {
	_, err := Open(context.Background(), Config{Driver: "postgres"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres driver is not implemented")
	assert.NotErrorIs(t, err, sqlite.ErrUniqueViolation)
}

func TestOpen_UnknownDriver(t *testing.T) {
	_, err := Open(context.Background(), Config{Driver: "bogus"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown driver")
}

// TestMigrate_SQLite applies the real migration set through the seam.
func TestMigrate_SQLite(t *testing.T) {
	db, err := Open(context.Background(), Config{
		Path:          filepath.Join(t.TempDir(), "migrated.db"),
		WALMode:       true,
		EnableForeign: true,
		BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	require.NoError(t, Migrate(context.Background(), db.DB))
	versions, err := AppliedVersions(context.Background(), db.DB)
	require.NoError(t, err)
	assert.NotEmpty(t, versions)
}

// TestSentinelsAliases pins that the neutral seam sentinels are the
// very values the driver returns — errors.Is must hold in both
// directions of the re-export.
func TestSentinelsAliases(t *testing.T) {
	assert.Equal(t, sqlite.ErrLockTaken, ErrLockTaken)
	assert.Equal(t, sqlite.ErrLockNotFound, ErrLockNotFound)
	assert.Equal(t, sqlite.ErrLockNotHeld, ErrLockNotHeld)
	assert.Equal(t, sqlite.ErrTokenNotFound, ErrTokenNotFound)
	assert.Equal(t, sqlite.ErrUniqueViolation, ErrUniqueViolation)
	assert.Equal(t, sqlite.ErrFKViolation, ErrFKViolation)
	assert.True(t, errors.Is(ErrLockTaken, sqlite.ErrLockTaken))
}

// TestClassifier covers the sqlite-implementation classifier that
// moved under the seam's exported names.
func TestClassifier(t *testing.T) {
	assert.True(t, IsUniqueViolation(errors.New("constraint failed: UNIQUE constraint failed: tags.name")))
	assert.False(t, IsUniqueViolation(errors.New("FOREIGN KEY constraint failed")))
	assert.False(t, IsUniqueViolation(nil))
	assert.True(t, IsFKViolation(errors.New("FOREIGN KEY constraint failed")))
	assert.False(t, IsFKViolation(errors.New("UNIQUE constraint failed: tags.name")))
	assert.False(t, IsFKViolation(nil))
}
