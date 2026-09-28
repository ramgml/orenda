package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T371 regression tests for the migration runner's foreign-key safety
// net. The old check ran `PRAGMA foreign_key_check` through ExecContext,
// which discards result rows, so every violation passed silently (that's
// how migration 015 shipped orphan boards). The check now reads the rows
// and diffs them against a BEFORE snapshot taken on the same connection:
//
//   - a migration that CREATES violations fails loud with the
//     table/rowid/parent/fkid list (up and down);
//   - pre-existing violations are logged as warnings, never fatal —
//     legacy upgrades carry damage that later cleanup migrations
//     (e.g. 050) remove within the same chain;
//   - the FK=OFF route on clean databases behaves exactly as before.

const (
	fkTestMarker = "-- orenda:foreign_keys_off"
	fkTestUser   = "u-371"
	// fkTestMissingProject never exists; boards pointing at it are the
	// canonical orphan shape used across these tests.
	fkTestMissingProject = "p-371-missing"
)

// captureSlog redirects the default slog logger into a buffer so tests
// can assert on runner warnings. The previous logger is restored via
// t.Cleanup. Tests using this helper must not use t.Parallel.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// openFKTestDB opens a throwaway database with the standard test config.
func openFKTestDB(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "orenda.db"), OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, ctx
}

// seedProjectWithBoard inserts a referentially sound user → project →
// board chain (the FK graph is ON here, so order matters).
func seedProjectWithBoard(t *testing.T, ctx context.Context, db *sql.DB, projectID, boardID string) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
		INSERT INTO users (id, email, password_hash, display_name)
		VALUES (?, 'owner@371.local', 'x', 'Owner')`, fkTestUser)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO projects (id, name, owner_id) VALUES (?, 'live', ?)`, projectID, fkTestUser)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO boards (id, project_id, name, position) VALUES (?, ?, 'Main', 0)`, boardID, projectID)
	require.NoError(t, err)
}

// seedOrphanBoard plants a board whose project does not exist, using
// foreign_keys=OFF — the same condition under which the FK=OFF route
// runs and under which 015 originally shipped its orphan.
func seedOrphanBoard(t *testing.T, ctx context.Context, db *sql.DB, boardID string) {
	t.Helper()
	_, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO boards (id, project_id, name, position)
		VALUES (?, ?, 'orphan', 0)`, boardID, fkTestMissingProject)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	require.NoError(t, err)
}

// TestApplyMigrationUnsafe_FKViolationFailsLoud pins scenario (a) on the
// up path: a marker migration that deletes a referenced project must
// fail with the violation list and roll back completely.
func TestApplyMigrationUnsafe_FKViolationFailsLoud(t *testing.T) {
	db, ctx := openFKTestDB(t)
	applyUpTo(t, ctx, db, "003_projects_tasks")
	seedProjectWithBoard(t, ctx, db, "p-371-live", "b-371-live")

	body := fkTestMarker + "\nDELETE FROM projects WHERE id = 'p-371-live';"
	err := applyMigration(ctx, db, "900_fk_probe", body)

	require.Error(t, err, "a migration that orphans a board must fail")
	assert.Contains(t, err.Error(), "foreign_key_check")
	assert.Contains(t, err.Error(), "migration 900_fk_probe (up)")
	assert.Contains(t, err.Error(), "introduced 1 foreign key violation(s)")
	assert.Contains(t, err.Error(), "table=boards rowid=1 parent=projects fkid=",
		"the violation line must carry table/rowid/parent/fkid in the T369 layout")
	assert.NotContains(t, err.Error(), "destination arguments",
		"Scan-shape errors must be impossible now that rows are read")

	// Rollback: the project, the board, and the version record are all
	// exactly as before the failed migration.
	var projects int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM projects WHERE id = 'p-371-live'`).Scan(&projects))
	assert.Equal(t, 1, projects, "failed migration must roll back")
	var boards int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM boards`).Scan(&boards))
	assert.Equal(t, 1, boards)
	versions, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.NotContains(t, versions, "900_fk_probe", "failed migration must not be recorded")
}

// TestApplyMigrationUnsafe_PreExistingViolationWarns pins scenario (b):
// an innocent marker migration on a database that already carries an
// orphan must succeed — with a warning — instead of stranding the
// upgrade on damage it did not create.
func TestApplyMigrationUnsafe_PreExistingViolationWarns(t *testing.T) {
	db, ctx := openFKTestDB(t)
	applyUpTo(t, ctx, db, "003_projects_tasks")
	seedOrphanBoard(t, ctx, db, "b-371-orphan")
	logs := captureSlog(t)

	body := fkTestMarker + "\nCREATE TABLE fk_probe_innocent (id TEXT);"
	require.NoError(t, applyMigration(ctx, db, "901_fk_probe_innocent", body))

	versions, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.Contains(t, versions, "901_fk_probe_innocent", "innocent migration must be recorded")
	var tables int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='fk_probe_innocent'`).Scan(&tables))
	assert.Equal(t, 1, tables)

	out := logs.String()
	assert.Contains(t, out, "pre-existing foreign key violations",
		"pre-existing damage must be surfaced as a warning")
	assert.Contains(t, out, "table=boards rowid=1 parent=projects",
		"the warning must name the offending row")
	assert.Contains(t, out, "901_fk_probe_innocent")
}

// TestMigrate_015ForeignKeysOffViolationFailsLoud pins the headline
// scenario on the real chain (applyUpTo style): a legacy-shaped database
// (cafe project with a board) upgraded past 014 used to sail through 015
// — its blind check discarded the orphan rows it created. The runner must
// now abort 015 with the violation list and roll the migration back.
func TestMigrate_015ForeignKeysOffViolationFailsLoud(t *testing.T) {
	db, ctx := openFKTestDB(t)
	applyUpTo(t, ctx, db, "014_child_tasks_inherit_column")

	const cafeProject = "00000000-0000-0000-0000-00000000cafe" // removed by 015
	// Migration 012 already seeded the cafe project (INSERT OR IGNORE);
	// the legacy shape we simulate is a board hanging off it.
	_, err := db.ExecContext(ctx,
		`INSERT INTO boards (id, project_id, name, position) VALUES ('b-371-cafe', ?, 'Main', 0)`,
		cafeProject)
	require.NoError(t, err)

	err = Migrate(ctx, db, MigrationsFS, "migrations")
	require.Error(t, err, "015 must fail loud when it orphans the cafe board")
	assert.Contains(t, err.Error(), "015_inbox_no_project")
	assert.Contains(t, err.Error(), "foreign_key_check")
	assert.Contains(t, err.Error(), "introduced 1 foreign key violation(s)")
	assert.Contains(t, err.Error(), "table=boards rowid=1 parent=projects fkid=")
	assert.NotContains(t, err.Error(), "destination arguments")

	// Rollback: 015 is not recorded, the legacy rows are untouched and
	// foreign_key_check is clean again (the tx was fully undone).
	versions, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.NotContains(t, versions, "015_inbox_no_project")
	var projects, boards int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM projects WHERE id = ?`, cafeProject).Scan(&projects))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM boards`).Scan(&boards))
	assert.Equal(t, 1, projects, "failed 015 must roll back the project delete")
	assert.Equal(t, 1, boards)
	require.Equal(t, 0, countFKViolations(t, ctx, db), "rollback must leave no violations")
}

// TestApplyMigrationDownUnsafe_FKViolationFailsLoud pins scenario (a) on
// the down path: a marker down-migration that inserts an orphan row must
// fail with the violation list, roll back the body, and keep the version
// recorded.
func TestApplyMigrationDownUnsafe_FKViolationFailsLoud(t *testing.T) {
	db, ctx := openFKTestDB(t)
	applyUpTo(t, ctx, db, "003_projects_tasks")

	// Simulate the applied state the down would roll back from.
	_, err := db.ExecContext(ctx,
		`INSERT INTO schema_migrations(version) VALUES ('902_fk_probe_down')`)
	require.NoError(t, err)

	body := fkTestMarker + "\n" +
		"INSERT INTO boards (id, project_id, name, position) " +
		"VALUES ('b-371-down-orphan', '" + fkTestMissingProject + "', 'x', 0);"
	err = applyMigrationDown(ctx, db, "902_fk_probe_down", body)

	require.Error(t, err, "a down migration that orphans a board must fail")
	assert.Contains(t, err.Error(), "sqlite: foreign_key_check")
	assert.Contains(t, err.Error(), "migration 902_fk_probe_down (down)")
	assert.Contains(t, err.Error(), "introduced 1 foreign key violation(s)")
	assert.Contains(t, err.Error(), "table=boards rowid=1 parent=projects fkid=")
	assert.NotContains(t, err.Error(), "destination arguments")

	var orphans int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM boards WHERE id = 'b-371-down-orphan'`).Scan(&orphans))
	assert.Equal(t, 0, orphans, "failed down must roll back its body")
	versions, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.Contains(t, versions, "902_fk_probe_down",
		"failed down must not unrecord the version")
}

// TestApplyMigrationDownUnsafe_PreExistingViolationWarns pins scenario
// (b) on the down path: pre-existing orphans are warnings, and a clean
// down body still unrecords its version.
func TestApplyMigrationDownUnsafe_PreExistingViolationWarns(t *testing.T) {
	db, ctx := openFKTestDB(t)
	applyUpTo(t, ctx, db, "003_projects_tasks")
	seedOrphanBoard(t, ctx, db, "b-371-down-pre")
	_, err := db.ExecContext(ctx,
		`INSERT INTO schema_migrations(version) VALUES ('903_fk_probe_down_innocent')`)
	require.NoError(t, err)
	logs := captureSlog(t)

	body := fkTestMarker + "\nDROP TABLE IF EXISTS fk_probe_down_innocent;"
	require.NoError(t, applyMigrationDown(ctx, db, "903_fk_probe_down_innocent", body))

	versions, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.NotContains(t, versions, "903_fk_probe_down_innocent",
		"a clean down must unrecord its version")
	out := logs.String()
	assert.Contains(t, out, "pre-existing foreign key violations")
	assert.Contains(t, out, "table=boards rowid=1 parent=projects")
	assert.Contains(t, out, "903_fk_probe_down_innocent")
}

// TestFKDiffHelpers pins the diff arithmetic directly: identical
// snapshots produce nothing new, and a changed rowid counts as new.
func TestFKDiffHelpers(t *testing.T) {
	before := []fkViolation{
		{table: "boards", rowid: "1", parent: "projects", fkid: "0"},
	}
	same := []fkViolation{
		{table: "boards", rowid: "1", parent: "projects", fkid: "0"},
	}
	assert.Empty(t, newFKViolations(before, same), "unchanged damage is not new")

	shifted := []fkViolation{
		{table: "boards", rowid: "2", parent: "projects", fkid: "0"},
		{table: "boards", rowid: "1", parent: "projects", fkid: "0"},
	}
	fresh := newFKViolations(before, shifted)
	require.Len(t, fresh, 1)
	assert.Equal(t, "2", fresh[0].rowid, "a new orphan row must be reported individually")
	assert.Equal(t, "table=boards rowid=2 parent=projects fkid=0", fresh[0].String())
}
