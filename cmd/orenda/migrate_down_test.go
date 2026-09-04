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
// last one the CLI can perform on the real migration set.
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
	require.Equal(t, "043_project_agent_access", head, "fresh up should end at 043")

	// Down ×2: 043 → 042 → 041. The head must move with every call
	// — the old hidden-UP bug pinned the head at 043 forever.
	require.NoError(t, runMigrateCLI(t, cfgPath, "down"))
	assert.Equal(t, "042_task_blocked_status", headVersion(t, dbPath),
		"first down must move the head 043 -> 042")
	require.NoError(t, runMigrateCLI(t, cfgPath, "down"))
	assert.Equal(t, "041_comment_edited_at", headVersion(t, dbPath),
		"second down must move the head 042 -> 041")

	// Re-up restores everything (no schema_migrations drift).
	require.NoError(t, runMigrateCLI(t, cfgPath, "up"))
	assert.Equal(t, head, headVersion(t, dbPath), "re-up must return to the original head")
}

// TestMigrateDownStopsAtIrreversible pins the guard: a `down` that
// lands on an irreversible migration (015_inbox_no_project carries the
// `-- orenda:irreversible` marker) is refused and the head stays put.
func TestMigrateDownStopsAtIrreversible(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	cfgPath := filepath.Join(dir, "config.yaml")

	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = dir
	cfg.Storage.DBPath = dbPath
	writeConfig(t, cfgPath, cfg)

	// Up to 016, then one down lands the head on 015; the next down
	// must be refused.
	require.NoError(t, runMigrateCLI(t, cfgPath, "up"))
	for headVersion(t, dbPath) != "016_task_dependencies" {
		require.NoError(t, runMigrateCLI(t, cfgPath, "down"))
	}
	require.NoError(t, runMigrateCLI(t, cfgPath, "down"))
	require.Equal(t, "015_inbox_no_project", headVersion(t, dbPath))

	err := runMigrateCLI(t, cfgPath, "down")
	require.Error(t, err)
	assert.ErrorIs(t, err, sqlite.ErrMigrationIrreversible)
	assert.Equal(t, "015_inbox_no_project", headVersion(t, dbPath),
		"refused down must not move the head")
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
