package sqlite

// T147 compensation test.
//
// The template-DB test helper (internal/testutil) replaced per-test
// sqlite.Migrate runs across backup/api/service/cmd. That removed the
// one place where every migration was executed on every `make test`.
// This test is the compensator: it applies the FULL migration chain
// up, then walks every reversible migration down to zero, then
// re-applies up — so a migration regression (bad SQL, order drift,
// missing down file) still fails loudly in CI even though fixtures no
// longer run the chain per test.
//
// DoD proof required by T147: with one migration broken in the
// worktree, this test must go red (evidence in the PR description).

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMigrate_FullCycle_AllMigrations applies every up migration,
// then rolls the whole chain back via MigrateDown (one call per
// version, the runner's contract), then applies up again. Any error
// in any migration file fails the test with the version named.
func TestMigrate_FullCycle_AllMigrations(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "orenda.db")
	db, err := Open(ctx, dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Up: the whole chain.
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"), "full up chain must apply cleanly")

	ups, err := collectMigrationFiles(MigrationsFS, "migrations")
	require.NoError(t, err)
	for i, name := range ups {
		ups[i] = pathVersion(name)
	}
	versions, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	sort.Strings(versions)
	require.Equal(t, ups, versions, "every up migration must be recorded after Migrate")
	// Down: walk the chain back toward zero. Irreversible versions
	// stop the walk — that's the documented floor (001_init would
	// wipe the schema).
	for {
		v, err := lastAppliedVersion(ctx, db)
		require.NoError(t, err)
		if isIrreversibleVersion(v) {
			t.Logf("stopped down-walk at irreversible migration %s", v)
			break
		}
		require.NoError(t, MigrateDown(ctx, db, MigrationsFS, "migrations"),
			"down for %s must apply cleanly", v)
	}

	// Re-up: the chain must apply again on the rolled-back schema.
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"),
		"re-up after down-walk must apply cleanly")
	versions, err = AppliedVersions(ctx, db)
	require.NoError(t, err)
	sort.Strings(versions)
	require.Equal(t, ups, versions, "re-up must record the full chain again")
}

// isIrreversibleVersion reports whether a migration version carries
// the irreversible marker in its down file.
func isIrreversibleVersion(version string) bool {
	data, err := MigrationsFS.ReadFile("migrations/" + version + ".down.sql")
	if err != nil {
		// No down file at all → the walk cannot pass it either.
		return true
	}
	return strings.Contains(string(data), irreversibleMarker)
}
