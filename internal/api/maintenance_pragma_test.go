package api

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// openPragmaFixtureDB opens an empty sqlite database with FK enforcement
// on, plus a parent/child pair for injecting referential violations the
// way real legacy data looks (child rows whose parent is gone).
func openPragmaFixtureDB(t *testing.T, ctx context.Context) *sql.DB {
	t.Helper()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "pragma.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.ExecContext(ctx, `
		CREATE TABLE parent (id TEXT PRIMARY KEY);
		CREATE TABLE child  (id TEXT PRIMARY KEY, parent_id TEXT NOT NULL REFERENCES parent(id));
	`)
	require.NoError(t, err)
	return db
}

// seedOrphanChild inserts child rows with a missing parent. Requires
// foreign_keys OFF for the duration — the same condition under which
// migration 015 (orenda:foreign_keys_off) baked orphans into upgraded
// installs.
func seedOrphanChild(t *testing.T, ctx context.Context, db *sql.DB, ids ...string) {
	t.Helper()
	_, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`)
	require.NoError(t, err)
	for _, id := range ids {
		_, err = db.ExecContext(ctx, `INSERT INTO child (id, parent_id) VALUES (?, 'missing')`, id)
		require.NoError(t, err)
	}
	_, err = db.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	require.NoError(t, err)
}

// TestRunMaintenancePragma_ForeignKeyViolation pins the T369 contract
// for the UI restore path: a real FK violation surfaces as a problem
// list (sqlite3 CLI layout) in the surfaced error text, never as a
// Scan-shape error like "sql: expected 4 destination arguments in Scan,
// not 1" — the old QueryRow+Scan failure operators saw instead of the
// offending rows.
func TestRunMaintenancePragma_ForeignKeyViolation(t *testing.T) {
	ctx := context.Background()
	db := openPragmaFixtureDB(t, ctx)

	// No violations yet — the pragma returns zero rows: must be clean.
	require.NoError(t, runMaintenancePragma(ctx, db, "foreign_key_check"))
	require.NoError(t, runMaintenancePragma(ctx, db, "integrity_check"))

	seedOrphanChild(t, ctx, db, "c1")
	err := runMaintenancePragma(ctx, db, "foreign_key_check")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "foreign_key_check:")
	assert.Contains(t, err.Error(), "table=child rowid=1 parent=parent fkid=",
		"violation line must carry table/rowid/parent/fkid")
	assert.NotContains(t, err.Error(), "destination arguments", "Scan-shape errors must be impossible")
	assert.NotContains(t, err.Error(), "sql:", "Scan-shape errors must be impossible")

	// Two violations — both rows must be reported, not just the first.
	seedOrphanChild(t, ctx, db, "c2")
	err = runMaintenancePragma(ctx, db, "foreign_key_check")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2 problem(s)")
	assert.Contains(t, err.Error(), "rowid=1")
	assert.Contains(t, err.Error(), "rowid=2")

	// Cleaning the orphans restores the "ok" contract.
	_, err = db.ExecContext(ctx, `DELETE FROM child`)
	require.NoError(t, err)
	require.NoError(t, runMaintenancePragma(ctx, db, "foreign_key_check"))
}
