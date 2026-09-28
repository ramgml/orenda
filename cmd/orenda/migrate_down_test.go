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

// runMigrateCLI drives `orenda migrate <args...>` directly against the
// migrate subcommand. --config is a root persistent flag, so it is
// registered locally on the migrate command (cobra doesn't see root's
// persistent flags without a full Execute) — same pattern as
// runUserCreateCLI.
func runMigrateCLI(t *testing.T, cfgPath string, args ...string) error {
	t.Helper()
	cmd := newMigrateCmd()
	cmd.PersistentFlags().StringP("config", "c", "", "config path (test only)")
	cmd.SetArgs(append([]string{"--config", cfgPath}, args...))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd.ExecuteContext(context.Background())
}

// headVersion returns the latest applied version ("" when none).
func headVersion(t *testing.T, dbPath string) string {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	versions, err := sqlite.AppliedVersions(ctx, db)
	require.NoError(t, err)
	if len(versions) == 0 {
		return ""
	}
	return versions[len(versions)-1]
}

// TestMigrateDownRepeatedMovesHead pins the T154 fix: `orenda migrate
// down` used to open the DB through the migrating opener, whose hidden
// Migrate(UP) re-applied whatever a previous `down` had just rolled
// back — repeated calls never moved the head. Now `down` reads the DB
// raw and rolls back one migration per call.
//
// The walk stops at 015_inbox_no_project: its down file carries the
// `-- orenda:irreversible` marker, so the down step 016 → 015 is the
// last one the CLI can perform on the real migration set. T369 adds
// 050_orphan_board_cleanup on top, also irreversible — so a fresh
// install's head cannot come down at all, and the repeated-down walk
// is exercised from the newest walkable head (049) via the unbook050
// fixture below.
func TestMigrateDownRepeatedMovesHead(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	cfgPath := filepath.Join(dir, "config.yaml")

	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = dir
	cfg.Storage.DBPath = dbPath
	writeConfig(t, cfgPath, cfg)

	// Full up so the head is the latest migration.
	require.NoError(t, runMigrateCLI(t, cfgPath, "up"))

	head := headVersion(t, dbPath)
	// T369 (050_orphan_board_cleanup) is additive; the head of the set
	// moves with the newest applied migration.
	require.Equal(t, "050_orphan_board_cleanup", head, "fresh up should end at the latest applied migration")

	// The head migration's down is guarded (irreversible marker): the
	// CLI refuses without moving the head.
	err := runMigrateCLI(t, cfgPath, "down")
	require.ErrorIs(t, err, sqlite.ErrMigrationIrreversible)
	require.Equal(t, "050_orphan_board_cleanup", headVersion(t, dbPath),
		"refused down must not move the head")

	// Fixture: a database at head 049 — the newest walkable head. On
	// this fresh DB 050 deleted nothing (no orphan boards exist), so
	// unbooking its schema_migrations row leaves exactly that state.
	unbookMigration(t, dbPath, "050_orphan_board_cleanup")

	// Down walks back through the tail. The head must move
	// with every call — the old hidden-UP bug pinned the head at the
	// latest forever.
	require.NoError(t, runMigrateCLI(t, cfgPath, "down"))
	assert.Equal(t, "048_chat_messages_user_id", headVersion(t, dbPath),
		"first down must move the head 049 -> 048")
	require.NoError(t, runMigrateCLI(t, cfgPath, "down"))
	assert.Equal(t, "047_chat_threads", headVersion(t, dbPath),
		"second down must move the head 048 -> 047")

	// Re-up restores everything (no schema_migrations drift).
	require.NoError(t, runMigrateCLI(t, cfgPath, "up"))
	assert.Equal(t, head, headVersion(t, dbPath), "re-up must return to the original head")
}

// TestMigrateDownStopsAtIrreversible pins the guard: a `down` that
// lands on an irreversible migration is refused and the head stays put.
// T369 (050_orphan_board_cleanup) carries the marker too, so a fresh
// install's head is already guarded; below it, the walk still ends on
// 015_inbox_no_project's marker.
func TestMigrateDownStopsAtIrreversible(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	cfgPath := filepath.Join(dir, "config.yaml")

	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = dir
	cfg.Storage.DBPath = dbPath
	writeConfig(t, cfgPath, cfg)

	// Fresh up: the head (050) is irreversible, so the very first down
	// must be refused without moving the head.
	require.NoError(t, runMigrateCLI(t, cfgPath, "up"))
	require.Equal(t, "050_orphan_board_cleanup", headVersion(t, dbPath))
	err := runMigrateCLI(t, cfgPath, "down")
	require.ErrorIs(t, err, sqlite.ErrMigrationIrreversible)
	require.Equal(t, "050_orphan_board_cleanup", headVersion(t, dbPath),
		"refused down must not move the head")

	// Fixture: head 049 (see unbookMigration) — then walk down to 016;
	// the next down lands the head on 015, whose marker refuses.
	unbookMigration(t, dbPath, "050_orphan_board_cleanup")
	for headVersion(t, dbPath) != "016_task_dependencies" {
		require.NoError(t, runMigrateCLI(t, cfgPath, "down"))
	}
	require.NoError(t, runMigrateCLI(t, cfgPath, "down"))
	require.Equal(t, "015_inbox_no_project", headVersion(t, dbPath))

	err = runMigrateCLI(t, cfgPath, "down")
	require.Error(t, err)
	assert.ErrorIs(t, err, sqlite.ErrMigrationIrreversible)
	assert.Equal(t, "015_inbox_no_project", headVersion(t, dbPath),
		"refused down must not move the head")
}

// unbookMigration drops a migration's schema_migrations row so a
// fixture stands at the head one migration below it. Used for
// 050_orphan_board_cleanup, whose down is irreversible (it deletes
// orphan rows that cannot be resurrected) — on a fresh fixture 050
// deleted nothing, so unbooking it leaves exactly the state of a fresh
// install that stopped one migration short.
func unbookMigration(t *testing.T, dbPath, version string) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = ?`, version)
	require.NoError(t, err)
}

// TestMigrateStatusDoesNotMutate pins that `status` opens the DB raw:
// listing applied versions must not apply pending migrations (the old
// shared opener did — status on a fresh DB reported a fully migrated
// schema without any `up` ever running).
func TestMigrateStatusDoesNotMutate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	cfgPath := filepath.Join(dir, "config.yaml")

	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = dir
	cfg.Storage.DBPath = dbPath
	writeConfig(t, cfgPath, cfg)

	// Fresh DB: status must succeed and leave the file untouched.
	require.NoError(t, runMigrateCLI(t, cfgPath, "status"))
	assert.Empty(t, headVersion(t, dbPath),
		"status on a fresh DB must not apply any migration")
}
