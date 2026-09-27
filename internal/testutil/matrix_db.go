package testutil

import (
	"database/sql"
	"testing"

	"github.com/ramgml/orenda/internal/testutil/pgtest"
)

// MatrixDB opens the fixture database for the active matrix driver
// (T364, wiki:storage-adapters D7). It is the seam for packages whose
// tests live outside internal/storage/sqlite and can therefore use this
// package directly:
//
//   - driver "sqlite" (default): the shared VACUUM INTO template copy
//     (TemplateDBOpen) — unchanged historic behaviour;
//   - driver "postgres": a fresh database cloned from the migrated
//     postgres template on the shared per-binary cluster (pgtest).
//
// Both branches register their cleanup with t. Packages opt into the
// matrix by calling pgtest.DriverMatrix from TestMain; without it the
// active driver stays "sqlite" and this helper is equivalent to
// TemplateDBOpen.
func MatrixDB(t testing.TB) *sql.DB {
	t.Helper()
	if pgtest.ActiveDriver() == pgtest.DriverPostgres {
		return pgtest.TemplateDB(t)
	}
	db, _ := TemplateDBOpen(t)
	return db
}
