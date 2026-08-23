package api_test

// Shared test infrastructure for the api_test package.
//
// The template DB pattern avoids running all 33 migrations on every
// test fixture.  TestMain creates a fully-migrated SQLite file once;
// each test copies it to a fresh temp directory.  The copy is a file
// system clone (cheap on the same filesystem) and inherits the
// DELETE journal mode set on the template, so no WAL/SHM artifacts
// need to be carried over.

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
	templateDBOnce sync.Once
	templateDBPath string
	templateDBErr  error
)

// ensureTemplateDB runs migrations once and returns the path to a
// fully-migrated SQLite file in DELETE journal mode (safe to copy).
func ensureTemplateDB(t *testing.T) string {
	t.Helper()
	templateDBOnce.Do(func() {
		dir, err := os.MkdirTemp("", "orenda-template-*")
		if err != nil {
			templateDBErr = err
			return
		}
		// The directory persists for the lifetime of the test binary.
		// The OS cleans up the temp dir on reboot; no explicit cleanup needed.

		dbPath := filepath.Join(dir, "template.db")
		db, err := sqlite.Open(context.Background(), dbPath, sqlite.OpenConfig{
			// Use DELETE journal so copies are trivially safe.
			WALMode: false, EnableForeign: true, BusyTimeoutMs: 5000,
		})
		if err != nil {
			templateDBErr = err
			return
		}
		if err := sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"); err != nil {
			_ = db.Close()
			templateDBErr = err
			return
		}
		// Switch to DELETE journal so file copies are safe even without
		// checkpointing.
		_, _ = db.ExecContext(context.Background(), "PRAGMA journal_mode = DELETE")
		_ = db.Close()
		templateDBPath = dbPath
	})
	require.NoError(t, templateDBErr, "template DB creation failed")
	return templateDBPath
}

// copyTemplateDB copies the pre-migrated template to a fresh temp
// directory and returns (*sql.DB, tempDir).  The caller must close
// the DB via t.Cleanup.
func copyTemplateDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	src := ensureTemplateDB(t)
	dir := t.TempDir()
	dst := filepath.Join(dir, "orenda.db")

	// Copy the main DB file.
	data, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dst, data, 0o600))

	// Copy any ancillary files ( WAL, SHM ) that may exist — they
	// are empty after DELETE-journal close, but be safe.
	for _, ext := range []string{"-wal", "-shm"} {
		srcExt := src + ext
		if _, err := os.Stat(srcExt); err == nil {
			d, err := os.ReadFile(srcExt)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(dst+ext, d, 0o600))
		}
	}

	db, err := sqlite.Open(context.Background(), dst, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, dir
}
