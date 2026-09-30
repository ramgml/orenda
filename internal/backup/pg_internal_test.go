// T381: version-matched client resolution + restore-direction guard.
//
// Internal test file (package backup): the fixtures need to repoint the
// unexported pgMultiVersionRoots at a synthetic multi-version tree.
// pg_test.go (backup_test) keeps the external-contract tests; the
// live-server tests here run on the postgres matrix leg only.
package backup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/testutil/pgtest"
)

// requirePostgresLeg mirrors pg_test.go's helper for the internal test
// package — unexported helpers don't cross the _test package boundary.
func requirePostgresLeg(t *testing.T) {
	t.Helper()
	if pgtest.ActiveDriver() != pgtest.DriverPostgres {
		t.Skip("pg backup round-trip runs on the postgres matrix leg (ORENDA_TEST_DRIVERS)")
	}
	for _, tool := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("pg backup tests need %s on PATH (the embedded postgres bundle ships server binaries only): %v", tool, err)
		}
	}
}

// fakePGTool writes an executable that prints the given pg tool
// --version line — enough for pgToolMajor and for the resolver chain.
func fakePGTool(t *testing.T, dir, name, version string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p,
		[]byte("#!/bin/sh\necho \""+version+" (Ubuntu 0.0-fake)\"\n"), 0o755))
	return p
}

// repointPVMultiVersionRoots swaps the multi-version roots for a test
// fixture and restores the real layout on cleanup.
func repointPVMultiVersionRoots(t *testing.T, roots ...string) {
	t.Helper()
	previous := pgMultiVersionRoots
	pgMultiVersionRoots = roots
	t.Cleanup(func() { pgMultiVersionRoots = previous })
}

func TestPGToolMajor(t *testing.T) {
	t.Run("parses two-part version", func(t *testing.T) {
		bin := fakePGTool(t, t.TempDir(), "pg_dump", "pg_dump (PostgreSQL) 18.6")
		major, err := pgToolMajor(bin)
		require.NoError(t, err)
		assert.Equal(t, 18, major)
	})
	t.Run("parses single-part version", func(t *testing.T) {
		bin := fakePGTool(t, t.TempDir(), "pg_restore", "pg_restore (PostgreSQL) 16")
		major, err := pgToolMajor(bin)
		require.NoError(t, err)
		assert.Equal(t, 16, major)
	})
	t.Run("unparseable output is an error, not a guess", func(t *testing.T) {
		bin := fakePGTool(t, t.TempDir(), "pg_dump", "definitely not pg_dump")
		_, err := pgToolMajor(bin)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot parse")
	})
}

func TestVersionMatchedPGTool(t *testing.T) {
	root := t.TempDir()
	wanted := fakePGTool(t, filepath.Join(root, "16", "bin"), "pg_dump", "pg_dump (PostgreSQL) 16.15")
	other := fakePGTool(t, filepath.Join(root, "18", "bin"), "pg_dump", "pg_dump (PostgreSQL) 18.6")

	t.Setenv("HOME", t.TempDir()) // keep the embedded-cache scan empty
	repointPVMultiVersionRoots(t, root)

	assert.Equal(t, wanted, versionMatchedPGTool("pg_dump", 16), "the server-matching major must win")
	assert.Equal(t, other, versionMatchedPGTool("pg_dump", 18))
	assert.Empty(t, versionMatchedPGTool("pg_dump", 17), "an uninstalled major finds nothing")
	assert.Empty(t, versionMatchedPGTool("pg_dump", 0), "an unknown server major skips the multi-version scan")
}

func TestResolvePGToolForServerPrefersVersionMatched(t *testing.T) {
	root := t.TempDir()
	matched := fakePGTool(t, filepath.Join(root, "16", "bin"), "pg_dump", "pg_dump (PostgreSQL) 16.15")

	binDir := t.TempDir()
	newer := fakePGTool(t, binDir, "pg_dump", "pg_dump (PostgreSQL) 18.6")

	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", binDir)
	repointPVMultiVersionRoots(t, root)

	got, err := resolvePGToolForServer("pg_dump", "", 16)
	require.NoError(t, err)
	assert.Equal(t, matched, got, "a matching-major install must beat a newer PATH client")

	got, err = resolvePGToolForServer("pg_dump", "", 0)
	require.NoError(t, err)
	assert.Equal(t, newer, got, "without a known server major the chain falls back to PATH")

	got, err = resolvePGToolForServer("pg_dump", "", 17)
	require.NoError(t, err)
	assert.Equal(t, newer, got, "no 17 install → PATH fallback, never the 16 install")
}

// TestRestorePostgresRefusesNewerRestoreClient pins the guard on the
// exact breakage Task 381 caught on the ubuntu-26.04 runner image:
// system client 18 against the pinned embedded 16-server. A fake
// pg_restore reporting major 18 must fail loudly BEFORE any scratch is
// created, naming the remedies — never half-restore into a server that
// rejects the newer client's preamble.
func TestRestorePostgresRefusesNewerRestoreClient(t *testing.T) {
	requirePostgresLeg(t)

	binDir := t.TempDir()
	fakePGTool(t, binDir, "pg_dump", "pg_dump (PostgreSQL) 18.6")
	fakePGTool(t, binDir, "pg_restore", "pg_restore (PostgreSQL) 18.6")

	// Empty multi-version roots: no matching-major install exists, the
	// guard must refuse instead of silently switching tools.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", binDir)
	repointPVMultiVersionRoots(t, t.TempDir())

	pool, dsn := pgtest.TemplateDBNamed(t)
	ctx := context.Background()

	dump := filepath.Join(t.TempDir(), "orenda-fake.dump")
	require.NoError(t, os.WriteFile(dump, []byte("not exercised — the guard fires first"), 0o644))

	svc := New(Config{
		Dialect:              DialectPostgres,
		SnapshotDir:          t.TempDir(),
		SnapshotRotationDays: 30,
		Postgres:             PostgresConfig{DSN: dsn},
	}, pool)

	_, err := svc.RestorePostgres(ctx, dump, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "major 18", "the refusal must name the client major")
	assert.Contains(t, err.Error(), "major 16", "the refusal must name the server major")
	assert.Contains(t, err.Error(), "postgresql-client-16", "the refusal must name the remedy")
	assert.Contains(t, err.Error(), "storage.postgres.dump_bin")

	// The guard precedes scratch creation: nothing may leak.
	var leaks int
	require.NoError(t, pool.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_database WHERE datname LIKE 'orenda_restore_%'`).Scan(&leaks))
	assert.Zero(t, leaks, "a refused restore must not leave a scratch database behind")
}

// TestPGRestoreList_SurfacesStderr pins the T366 review fix (restored in
// T381: the merge with dev initially dropped it together with the old
// file): a failing pg_restore --list must carry the tool's stderr (e.g.
// the "did not find magic string" parser complaint), not an empty "exit
// status 1:" reason — that's what the CLI prints and the HTTP 422 detail
// shows. Self-sufficient: no matrix leg, no server — just a garbage file.
func TestPGRestoreList_SurfacesStderr(t *testing.T) {
	if _, err := exec.LookPath("pg_restore"); err != nil {
		t.Skipf("pg_restore not on PATH (embedded bundle ships server binaries only): %v", err)
	}
	bad := filepath.Join(t.TempDir(), "garbage.dump")
	require.NoError(t, os.WriteFile(bad, []byte("definitely not a pg_dump archive"), 0o644))

	_, err := pgRestoreList(context.Background(), "pg_restore", bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pg_restore --list")
	assert.Contains(t, err.Error(), "valid archive",
		"stderr must be surfaced so the operator sees WHY the archive is unreadable")
}

// TestSnapshotRestoreRoundTripVersionMatched pins the fix end to end in
// the exact ubuntu-26.04 CI shape: system client NEWER than the server
// + an installed matching-major client. The resolver must pick the
// matched client for both directions and the full round-trip stays
// green — never the newer system client. Environments without a newer
// system client (24.04 image, dev boxes on 16) skip: the ordinary
// TestBackupPG_SnapshotRestoreRoundTrip already covers them.
func TestSnapshotRestoreRoundTripVersionMatched(t *testing.T) {
	requirePostgresLeg(t)

	systemDump, err := exec.LookPath("pg_dump")
	if err != nil {
		t.Skipf("pg backup tests need pg_dump on PATH (the embedded postgres bundle ships server binaries only): %v", err)
	}
	majorSystem, err := pgToolMajor(systemDump)
	require.NoError(t, err)

	pool, dsn := pgtest.TemplateDBNamed(t)
	ctx := context.Background()
	serverMajor, err := pgServerMajor(ctx, pool)
	require.NoError(t, err)

	if majorSystem <= serverMajor {
		t.Skipf("system client major %d <= server major %d — the newer-client shape is absent here", majorSystem, serverMajor)
	}
	if versionMatchedPGTool("pg_dump", serverMajor) == "" || versionMatchedPGTool("pg_restore", serverMajor) == "" {
		t.Skipf("no version-matched client %d under the multi-version roots — CI installs one (Task 381)", serverMajor)
	}

	snapDir := filepath.Join(t.TempDir(), "snapshots")
	svc := New(Config{
		Dialect:              DialectPostgres,
		SnapshotDir:          snapDir,
		SnapshotRotationDays: 30,
		Postgres:             PostgresConfig{DSN: dsn},
	}, pool)

	dumpPath, err := svc.Snapshot(ctx)
	require.NoError(t, err, "snapshot must resolve the version-matched client and succeed")
	_, err = svc.RestorePostgres(ctx, dumpPath, "")
	require.NoError(t, err, "restore must run the same-major client against the server, not the newer system one")
}
