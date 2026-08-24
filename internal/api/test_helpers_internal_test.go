package api

// Shared test infrastructure for the api package (white-box tests).
//
// Mirrors the api_test helper: a fully-migrated template DB is
// created once per test binary via sync.Once, then copied into each
// test's temp directory.  The template uses DELETE journal mode so
// copies are trivially safe.

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/storage/sqlite"
)

var (
	internalTemplateOnce sync.Once
	internalTemplatePath string
	internalTemplateErr  error
)

// ensureInternalTemplateDB runs migrations once and returns the path
// to a fully-migrated SQLite file in DELETE journal mode.
func ensureInternalTemplateDB(t *testing.T) string {
	t.Helper()
	internalTemplateOnce.Do(func() {
		dir, err := os.MkdirTemp("", "orenda-template-int-*")
		if err != nil {
			internalTemplateErr = err
			return
		}
		// The directory persists for the lifetime of the test binary.

		dbPath := filepath.Join(dir, "template.db")
		db, err := sqlite.Open(context.Background(), dbPath, sqlite.OpenConfig{
			WALMode: false, EnableForeign: true, BusyTimeoutMs: 5000,
		})
		if err != nil {
			internalTemplateErr = err
			return
		}
		if err := sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"); err != nil {
			_ = db.Close()
			internalTemplateErr = err
			return
		}
		_, _ = db.ExecContext(context.Background(), "PRAGMA journal_mode = DELETE")
		_ = db.Close()
		internalTemplatePath = dbPath
	})
	require.NoError(t, internalTemplateErr, "internal template DB creation failed")
	return internalTemplatePath
}

// copyInternalTemplateDB copies the pre-migrated template to a fresh
// temp directory and returns *sql.DB.  The caller must close the DB
// via t.Cleanup.
func copyInternalTemplateDB(t *testing.T) *sql.DB {
	t.Helper()
	src := ensureInternalTemplateDB(t)
	dir := t.TempDir()
	dst := filepath.Join(dir, "orenda.db")

	data, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dst, data, 0o600))

	db, err := sqlite.Open(context.Background(), dst, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
