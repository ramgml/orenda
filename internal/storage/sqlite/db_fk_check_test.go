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
// and diffs them against snapshots:
//
//   - unsafe UP migrations that CREATE violations commit with a
//     warning; enforcement is deferred to the end of the Migrate chain —
//     a violation nobody healed fails the whole run with per-row
//     attribution. This is what lets legacy chains upgrade: 015
//     orphans the cafe board, 050 (same run) deletes it.
//   - unsafe DOWN migrations fail immediately — nobody heals below
//     the last migration;
//   - pre-existing violations are logged as warnings, never fatal;
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

// TestApplyMigrationUnsafe_IntroducedViolationWarnsAndRecords pins the
// up path: a marker migration that deletes a referenced project commits
// (record + warn), reporting its introductions to the caller for
// chain-end enforcement — it must NOT abort or roll back.
func TestApplyMigrationUnsafe_IntroducedViolationWarnsAndRecords(t *testing.T) {
	db, ctx := openFKTestDB(t)
	applyUpTo(t, ctx, db, "003_projects_tasks")
	seedProjectWithBoard(t, ctx, db, "p-371-live", "b-371-live")
	logs := captureSlog(t)

	body := fkTestMarker + "\nDELETE FROM projects WHERE id = 'p-371-live';"
	introduced, err := applyMigration(ctx, db, "900_fk_probe", body)

	require.NoError(t, err, "up-migrations commit; enforcement is chain-end")
	require.Len(t, introduced, 1, "the orphaned board must be reported to the caller")
	assert.Equal(t, "boards", introduced[0].table)
	assert.Equal(t, "projects", introduced[0].parent)
	assert.Equal(t, "table=boards rowid=1 parent=projects fkid=", introduced[0].String()[:len("table=boards rowid=1 parent=projects fkid=")],
		"the violation line must carry table/rowid/parent/fkid in the T369 layout")
	assert.NotContains(t, introduced[0].String(), "destination arguments",
		"Scan-shape errors must be impossible now that rows are read")

	// The migration committed: version recorded, the delete is real.
	versions, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.Contains(t, versions, "900_fk_probe", "up-migration must be recorded")
	var projects int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM projects WHERE id = 'p-371-live'`).Scan(&projects))
	assert.Equal(t, 0, projects, "the body's delete is committed, not rolled back")

	out := logs.String()
	assert.Contains(t, out, "introduced foreign key violations",
		"introductions must be surfaced as a warning at commit time")
	assert.Contains(t, out, "900_fk_probe")
	assert.Contains(t, out, "table=boards rowid=1 parent=projects")
}

// TestApplyMigrationUnsafe_PreExistingViolationWarns pins the up path
// for pre-existing damage: an innocent marker migration on a database
// that already carries an orphan must succeed, warn, and report no
// introductions of its own.
func TestApplyMigrationUnsafe_PreExistingViolationWarns(t *testing.T) {
	db, ctx := openFKTestDB(t)
	applyUpTo(t, ctx, db, "003_projects_tasks")
	seedOrphanBoard(t, ctx, db, "b-371-orphan")
	logs := captureSlog(t)

	body := fkTestMarker + "\nCREATE TABLE fk_probe_innocent (id TEXT);"
	introduced, err := applyMigration(ctx, db, "901_fk_probe_innocent", body)

	require.NoError(t, err)
	assert.Empty(t, introduced, "pre-existing damage is not this migration's doing")

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

// TestMigrate_LegacyChainUpgradeSucceeds pins the headline scenario on
// the real chain (applyUpTo style): a legacy-shaped database (cafe
// board, as seeded by 012) upgraded past 014 used to sail through 015
// because its check was blind. Now 015 sees the orphan it creates —
// commits with a warning — and 050, later in the SAME run, deletes it,
// so the chain ends clean and the legacy upgrade passes. This is the
// deferred-enforcement contract: failing 015 outright would strand
// pre-Phase-16 installs forever (no 050 without 015).
func TestMigrate_LegacyChainUpgradeSucceeds(t *testing.T) {
	db, ctx := openFKTestDB(t)
	applyUpTo(t, ctx, db, "014_child_tasks_inherit_column")

	const cafeProject = "00000000-0000-0000-0000-00000000cafe" // removed by 015
	// Migration 012 already seeded the cafe project (INSERT OR IGNORE);
	// the legacy shape we simulate is a board hanging off it.
	_, err := db.ExecContext(ctx,
		`INSERT INTO boards (id, project_id, name, position) VALUES ('b-371-cafe', ?, 'Main', 0)`,
		cafeProject)
	require.NoError(t, err)
	logs := captureSlog(t)

	// Sanity: the damage does not exist before the chain runs.
	require.Equal(t, 0, countFKViolations(t, ctx, db))

	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"),
		"015's introduction must be healed by 050 within the same chain")

	// The whole chain applied and ended FK-clean.
	versions, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.Contains(t, versions, "015_inbox_no_project")
	assert.Contains(t, versions, "050_orphan_board_cleanup")
	var boards int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM boards`).Scan(&boards))
	assert.Equal(t, 0, boards, "050 must have healed 015's orphan in the same run")
	assert.Equal(t, 0, countFKViolations(t, ctx, db), "chain must end FK-clean")

	// The deferral was visible: 015 warned with the row list.
	out := logs.String()
	assert.Contains(t, out, "015_inbox_no_project")
	assert.Contains(t, out, "introduced foreign key violations")
	assert.Contains(t, out, "table=boards rowid=1 parent=projects")
}

// TestEnforceChainFKDiff pins the chain-end enforcement unit: a
// violation that survived the whole chain fails the run with the
// introducer attributed; healed introductions pass silently.
func TestEnforceChainFKDiff(t *testing.T) {
	baseline := []fkViolation{
		{table: "boards", rowid: "1", parent: "projects", fkid: "0"},
	}
	baselineKey := baseline[0].key()

	// Healed: 015 introduced an orphan, 050 deleted it — final ==
	// baseline minus nothing new → pass.
	healed := []fkViolation{{table: "boards", rowid: "1", parent: "projects", fkid: "0"}}
	require.NoError(t, enforceChainFKDiff(baseline, healed, map[string]string{baselineKey: "015_x"}))

	// Unhealed introduction → loud error with attribution.
	unresolved := append(healed, fkViolation{table: "tasks", rowid: "7", parent: "projects", fkid: "1"})
	err := enforceChainFKDiff(baseline, unresolved, map[string]string{
		// baseline row is not an introduction; the tasks row is.
		(&fkViolation{table: "tasks", rowid: "7", parent: "projects", fkid: "1"}).key(): "016_task_dependencies",
	})
	require.Error(t, err, "an unhealed introduction must fail the chain")
	assert.Contains(t, err.Error(), "migrate chain ended with 1 unresolved foreign key violation(s)")
	assert.Contains(t, err.Error(), "table=tasks rowid=7 parent=projects fkid=1")
	assert.Contains(t, err.Error(), "introduced by migration 016_task_dependencies, up")
	assert.NotContains(t, err.Error(), "table=boards rowid=1",
		"baseline damage must not be blamed on the chain")

	// Unattributed row renders without an attribution suffix.
	err = enforceChainFKDiff(nil, []fkViolation{
		{table: "boards", rowid: "2", parent: "projects", fkid: "0"},
	}, map[string]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "table=boards rowid=2 parent=projects fkid=0")
	assert.NotContains(t, err.Error(), "introduced by migration")
}

// TestApplyMigrationDownUnsafe_FKViolationFailsLoud pins the down path:
// enforcement stays immediate there — a marker down-migration that
// inserts an orphan fails with the violation list, rolls back the body,
// and keeps the version recorded.
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

// TestApplyMigrationDownUnsafe_PreExistingViolationWarns pins the down
// path for pre-existing orphans: warnings, and a clean down body still
// unrecords its version.
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

// TestFKDiffHelpers pins the per-migration diff arithmetic directly:
// identical snapshots produce nothing new, and a changed rowid counts
// as new.
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
