// Package testutil provides a shared template SQLite database for
// tests. Building the schema from scratch costs a full
// sqlite.Migrate run per fixture (~1.4s each; dozens of call sites
// across backup/api/service/cmd — T147). Instead, the first caller
// per test binary migrates once and VACUUM INTOs the result into a
// template file (same pattern as backup.Snapshot, backup.go); every
// test then copies that pristine, checkpointed file.
//
// This package is regular (non _test) code so _test files in any
// package can import it. It must stay dependency-light: only
// stdlib + internal/storage/sqlite.
package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ramgml/orenda/internal/storage/sqlite"
)

var (
	templateOnce sync.Once
	templatePath string
	templateErr  error
)

// ensureTemplate builds the template database once per test binary.
// The build is: migrate a scratch DB, then `VACUUM INTO` the
// template — the result is fully checkpointed (no -wal/-shm tails)
// and in a pristine zero-row state, safe to copy concurrently.
func ensureTemplate(t testing.TB) (string, error) {
	t.Helper()
	templateOnce.Do(func() {
		dir, err := os.MkdirTemp("", "orenda-tpl-*")
		if err != nil {
			templateErr = err
			return
		}

		scratch := filepath.Join(dir, "scratch.db")
		db, err := sqlite.Open(context.Background(), scratch, sqlite.OpenConfig{
			WALMode: false, EnableForeign: true, BusyTimeoutMs: 5000,
		})
		if err != nil {
			templateErr = fmt.Errorf("testutil: open scratch: %w", err)
			return
		}
		if err := sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"); err != nil {
			_ = db.Close()
			templateErr = fmt.Errorf("testutil: migrate scratch: %w", err)
			return
		}
		tpl := filepath.Join(dir, "template.db")
		if _, err := db.ExecContext(context.Background(), `VACUUM INTO ?`, tpl); err != nil {
			_ = db.Close()
			templateErr = fmt.Errorf("testutil: vacuum into template: %w", err)
			return
		}
		if err := db.Close(); err != nil {
			templateErr = fmt.Errorf("testutil: close scratch: %w", err)
			return
		}

		// The scratch DB is only needed during construction.
		_ = os.Remove(scratch)

		// Sanity: the template must exist and be non-trivial.
		info, err := os.Stat(tpl)
		if err != nil {
			templateErr = fmt.Errorf("testutil: stat template: %w", err)
			return
		}
		if info.Size() == 0 {
			templateErr = fmt.Errorf("testutil: template is empty")
			return
		}
		templatePath = tpl
	})
	return templatePath, templateErr
}

// TemplateDBPath returns the path of the migrated template database.
// The path lives in a per-binary temp dir; copy it before use.
func TemplateDBPath(t testing.TB) string {
	t.Helper()
	tpl, err := ensureTemplate(t)
	if err != nil {
		t.Fatalf("template DB: %v", err)
	}
	return tpl
}

// TemplateDB copies the template into the test's temp dir and returns
// the copy's path. The test owns the copy: it may write freely, the
// template stays pristine for the next test.
func TemplateDB(t testing.TB) string {
	t.Helper()
	tpl := TemplateDBPath(t)
	dst := filepath.Join(t.TempDir(), "orenda.db")
	data, err := os.ReadFile(tpl)
	if err != nil {
		t.Fatalf("template DB copy: %v", err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatalf("template DB copy: %v", err)
	}
	return dst
}

// TemplateDBOpen copies the template (TemplateDB) and opens it with
// the WAL + foreign-keys configuration the production code expects.
// The DB is registered for Close via t.Cleanup. Returns (*sql.DB, path).
func TemplateDBOpen(t testing.TB) (db *sql.DB, dbPath string) {
	t.Helper()
	dst := TemplateDB(t)
	db, err := sqlite.Open(context.Background(), dst, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	if err != nil {
		t.Fatalf("template DB open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, dst
}
