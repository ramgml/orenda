package main

import (
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/config"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// runBackupCLI drives `orenda backup <args...>` against the backup
// command tree. --config is a root persistent flag, so it is registered
// locally on the backup command (cobra doesn't see root's persistent
// flags without a full Execute) — same pattern as runMigrateCLI.
func runBackupCLI(t *testing.T, cfgPath string, args ...string) error {
	t.Helper()
	cmd := newBackupCmd()
	cmd.PersistentFlags().StringP("config", "c", "", "config path (test only)")
	cmd.SetArgs(append([]string{"--config", cfgPath}, args...))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.ExecuteContext(context.Background())
}

// soleSnapshot returns the only *.db file under dir (the snapshot the
// snapshot subcommand just wrote; the CLI prints the path to stdout,
// which these tests discard).
func soleSnapshot(t *testing.T, dir string) string {
	t.Helper()
	snaps, err := filepath.Glob(filepath.Join(dir, "*.db"))
	require.NoError(t, err)
	require.Len(t, snaps, 1, "expected exactly one snapshot in %s", dir)
	return snaps[0]
}

// freshInstallFixture migrates a brand-new database, creates the first
// user (the whole fresh-install flow) and returns the config it ran on.
func freshInstallFixture(t *testing.T) (cfgPath, dbPath string) {
	t.Helper()
	dir := t.TempDir()
	dbPath = filepath.Join(dir, "orenda.db")
	cfgPath = filepath.Join(dir, "config.yaml")

	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = dir
	cfg.Storage.DBPath = dbPath
	cfg.Backup.SnapshotDir = filepath.Join(dir, "snapshots")
	writeConfig(t, cfgPath, cfg)

	require.NoError(t, runMigrateCLI(t, cfgPath, "up"))
	require.NoError(t, runUserCreateCLI(t, cfgPath,
		[]string{"--email=owner@fresh.local", "--display-name=Owner"},
		"hunter2!\n"))
	return cfgPath, dbPath
}

// seedOrphanBoard re-creates the legacy damage on an upgraded install: a
// bootstrap-era board whose project row is gone, inserted with foreign
// keys OFF exactly like migration 015 left it.
func seedOrphanBoard(t *testing.T, ctx context.Context, dbPath string) {
	t.Helper()
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO boards (id, project_id, name, position)
		VALUES ('70194de4-ac95-48ca-818c-000000000001',
		        '00000000-0000-0000-0000-00000000cafe', 'Main', 0)`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	require.NoError(t, err)
}

// TestBackupRestore_FreshInstallVerify is the T369 DoD regression: a
// fresh install (migrate up + first user) must pass `backup restore`
// verification — integrity and foreign keys — with no FK errors.
func TestBackupRestore_FreshInstallVerify(t *testing.T) {
	cfgPath, dbPath := freshInstallFixture(t)

	require.NoError(t, runBackupCLI(t, cfgPath, "snapshot"))
	snap := soleSnapshot(t, filepath.Join(filepath.Dir(dbPath), "snapshots"))

	restored := filepath.Join(filepath.Dir(dbPath), "restored.db")
	require.NoError(t, runBackupCLI(t, cfgPath, "restore",
		"--from", snap, "--to", restored, "--yes"),
		"fresh-install snapshot must pass restore verify")
}

// TestBackupRestore_TaintedSnapshotVerifyListsViolations pins the
// operator-visible contract of the fix: restoring a snapshot whose data
// carries a real FK violation (the legacy orphan board baked into
// pre-050 snapshots) FAILS verification honestly — with the offending
// row named (table/rowid/parent/fkid), never with the old
// "sql: expected 4 destination arguments in Scan, not 1" Scan error.
func TestBackupRestore_TaintedSnapshotVerifyListsViolations(t *testing.T) {
	ctx := context.Background()
	cfgPath, dbPath := freshInstallFixture(t)
	seedOrphanBoard(t, ctx, dbPath)

	require.NoError(t, runBackupCLI(t, cfgPath, "snapshot"))
	snap := soleSnapshot(t, filepath.Join(filepath.Dir(dbPath), "snapshots"))

	restored := filepath.Join(filepath.Dir(dbPath), "restored.db")
	err := runBackupCLI(t, cfgPath, "restore",
		"--from", snap, "--to", restored, "--yes")
	require.Error(t, err, "a snapshot with an FK violation must fail restore verify")
	assert.Contains(t, err.Error(), "foreign_key_check")
	assert.Contains(t, err.Error(), "table=boards rowid=1 parent=projects fkid=",
		"the violation list must name the offending row")
	assert.NotContains(t, err.Error(), "destination arguments",
		"Scan-shape errors must be impossible")
}
