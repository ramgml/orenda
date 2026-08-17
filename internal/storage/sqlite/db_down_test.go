package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrateDown_RoundTrip exercises the down path for the
// reversible migrations: open a fresh DB, apply up to v (so v
// is the latest applied), then call MigrateDown once and confirm
// v disappears from schema_migrations.
//
// We don't pin every column — that's covered by the per-migration
// up tests. The point here is to make sure the runner actually
// picks up the .down.sql file for v.
func TestMigrateDown_RoundTrip(t *testing.T) {
	// Reversible migrations. 001 is irreversible (would wipe every
	// table) so we start from 002. 013/015/017 are irreversible so
	// we stop at 012, then test 014 / 016 / 019 individually.
	reversible := []string{
		"002_auth", "003_projects_tasks", "004_agents",
		"005_comments_attachments", "006_calendar_time",
		"007_time_entries_actor", "008_wiki", "009_notifications",
		"010_backups", "011_sync_ops", "012_events_to_tasks",
		"014_child_tasks_inherit_column", "016_task_dependencies",
		"019_courses", "020_columns_status", "021_agent_type_labels",
		"022_study_planning",
	}
	for _, v := range reversible {
		t.Run(v, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			dbPath := filepath.Join(dir, "orenda.db")
			db, err := Open(ctx, dbPath, OpenConfig{
				WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })

			// Apply only the migration under test (plus every
			// one before it). applyUpTo sorts by name and stops
			// once the target is reached — v ends up as the
			// latest applied.
			applyUpTo(t, ctx, db, v)

			// v IS the latest; MigrateDown should roll it back
			// in one call.
			require.NoError(t, MigrateDown(ctx, db, MigrationsFS, "migrations"))
			versions, err := AppliedVersions(ctx, db)
			require.NoError(t, err)
			assert.NotContains(t, versions, v, "migration %s should be rolled back", v)
		})
	}
}

// TestMigrateDown_Irreversible pins the marker behaviour:
// migrations 001/013/015 carry the irreversible marker, so
// MigrateDown must surface ErrMigrationIrreversible without
// running anything. The marker text becomes part of the error so
// the operator sees the reason without opening the file.
//
// We apply up to each irreversible migration (so it's the LATEST
// applied) and call MigrateDown — that's the runner's contract:
// one migration back per call.
func TestMigrateDown_Irreversible(t *testing.T) {
	irreversible := []string{
		"001_init",
		"013_subtasks_to_children",
		"015_inbox_no_project",
	}
	for _, v := range irreversible {
		t.Run(v, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			dbPath := filepath.Join(dir, "orenda.db")
			db, err := Open(ctx, dbPath, OpenConfig{
				WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })

			// Apply up to (and including) v. After this, v is the
			// latest applied migration; MigrateDown reads v.
			applyUpTo(t, ctx, db, v)

			err = MigrateDown(ctx, db, MigrationsFS, "migrations")
			require.Error(t, err, "irreversible migration %s should refuse the down", v)
			assert.True(t, errors.Is(err, ErrMigrationIrreversible),
				"expected ErrMigrationIrreversible, got: %v", err)
			assert.Contains(t, err.Error(), v, "error should name the version")

			// Confirm nothing actually changed: the migration is
			// still in schema_migrations.
			versions, _ := AppliedVersions(ctx, db)
			assert.Contains(t, versions, v, "irreversible migration should not have run")
		})
	}
}

// TestMigrateDown_MissingDownFile covers the "operator wrote a .up
// but no .down yet" path. The runner should return ErrNoDownFile
// with the missing path embedded for fast debugging.
func TestMigrateDown_MissingDownFile(t *testing.T) {
	// We don't have a real missing-down scenario in the shipped
	// migrations (every .up has a matching .down or is marked
	// irreversible). Simulate by deleting one of the down files
	// via the embed FS — we can't, since it's compiled in. Instead
	// we verify the helper directly: findDownFile returns the
	// expected error path.
	_, err := findDownFile(MigrationsFS, "migrations", "does_not_exist")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoDownFile))
	assert.Contains(t, err.Error(), "does_not_exist.down.sql")
}

// TestParseIrreversibleReason pins the marker format. Two accepted
// shapes: bare marker (no reason) and `marker: <reason>`. A marker
// with the wrong separator (semicolon) still registers as found —
// the runner surfaces the body so the operator can fix the typo,
// but no reason is parsed.
func TestParseIrreversibleReason(t *testing.T) {
	cases := []struct {
		body, reason string
		ok           bool
	}{
		{"-- some comment\n", "", false},
		{"-- orenda:irreversible\n", "", true},
		{"-- orenda:irreversible: data reshape\n", "data reshape", true},
		{"-- orenda:irreversible:    spaced reason\n", "spaced reason", true},
		{"some SQL\n-- orenda:irreversible: middle\n", "middle", true},
		// Wrong separator: marker present, no reason parsed.
		{"-- orenda:irreversible;semicolon-not-colon\n", "", true},
	}
	for _, c := range cases {
		reason, ok := parseIrreversibleReason(c.body)
		assert.Equal(t, c.ok, ok, "body=%q", c.body)
		if ok {
			assert.Equal(t, c.reason, reason, "body=%q", c.body)
		}
	}
}
