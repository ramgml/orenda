package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ramgml/orenda/internal/testutil/pgtest"
)

// TestMain turns this package's suite into the two-driver repository
// matrix (T364, wiki:storage-adapters D7): every repository test runs
// once on sqlite and once on postgres through the dialect shim. The
// active driver is read by matrixDB, which every fixture helper goes
// through; ORENDA_TEST_DRIVERS=sqlite opts out of the postgres leg,
// ORENDA_TEST_PG_DSN moves the postgres leg onto an external server.
func TestMain(m *testing.M) {
	os.Exit(pgtest.DriverMatrix(m))
}

// matrixDB opens the fixture database for the active matrix driver.
//
// The postgres leg clones the shared pgtest template (native
// CREATE DATABASE ... TEMPLATE on the per-binary cluster). The sqlite
// leg copies a package-local VACUUM INTO template — the same pattern
// testutil/template_db.go established, but local because testutil
// imports the storage seam, which imports this package: an import from
// inside package sqlite would be a cycle.
func matrixDB(t *testing.T) *sql.DB {
	t.Helper()
	if pgtest.ActiveDriver() == pgtest.DriverPostgres {
		return pgtest.TemplateDB(t)
	}
	return openSQLiteTemplateDB(t)
}

var (
	sqliteTemplateOnce sync.Once
	sqliteTemplatePath string
	sqliteTemplateErr  error
)

// buildSQLiteTemplate migrates a scratch database once per test binary
// and VACUUM INTOs the result into a pristine, checkpointed template
// file (testutil/template_db.go's pattern).
func buildSQLiteTemplate() (string, error) {
	sqliteTemplateOnce.Do(func() {
		dir, err := os.MkdirTemp("", "orenda-sqlite-tpl-*")
		if err != nil {
			sqliteTemplateErr = err
			return
		}
		sdb, err := Open(context.Background(), filepath.Join(dir, "scratch.db"), OpenConfig{
			WALMode: false, EnableForeign: true, BusyTimeoutMs: 5000,
		})
		if err != nil {
			sqliteTemplateErr = fmt.Errorf("sqlite template: open scratch: %w", err)
			return
		}
		if err := Migrate(context.Background(), sdb, MigrationsFS, "migrations"); err != nil {
			_ = sdb.Close()
			sqliteTemplateErr = fmt.Errorf("sqlite template: migrate scratch: %w", err)
			return
		}
		tpl := filepath.Join(dir, "template.db")
		if _, err := sdb.ExecContext(context.Background(), `VACUUM INTO ?`, tpl); err != nil {
			_ = sdb.Close()
			sqliteTemplateErr = fmt.Errorf("sqlite template: vacuum into template: %w", err)
			return
		}
		if err := sdb.Close(); err != nil {
			sqliteTemplateErr = fmt.Errorf("sqlite template: close scratch: %w", err)
			return
		}
		_ = os.Remove(filepath.Join(dir, "scratch.db"))
		sqliteTemplatePath = tpl
	})
	return sqliteTemplatePath, sqliteTemplateErr
}

// openSQLiteTemplateDB copies the package template into the test's temp
// dir and opens it with the same WAL + foreign-keys + busy_timeout
// configuration the historic per-test Open+Migrate fixtures used.
func openSQLiteTemplateDB(t *testing.T) *sql.DB {
	t.Helper()
	tpl, err := buildSQLiteTemplate()
	if err != nil {
		t.Fatalf("sqlite template: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "orenda.db")
	data, err := os.ReadFile(tpl)
	if err != nil {
		t.Fatalf("sqlite template copy: %v", err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatalf("sqlite template copy: %v", err)
	}
	db, err := Open(context.Background(), dst, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	if err != nil {
		t.Fatalf("sqlite template open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
