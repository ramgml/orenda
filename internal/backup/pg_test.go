// T366: postgres backup round-trip tests (wiki:storage-adapters D8).
//
// The binary joins the two-driver matrix (TestMain → DriverMatrix) so
// the postgres legs get the shared pgtest accelerator (embedded cluster
// or ORENDA_TEST_PG_DSN) and its lifecycle. The PG-only tests skip on
// the sqlite leg; on either leg they skip with a named reason when
// pg_dump/pg_restore are absent — the tests shell out to the client
// tools, which the stock embedded bundle does not carry.
package backup_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/backup"
	"github.com/ramgml/orenda/internal/storage/postgres"
	"github.com/ramgml/orenda/internal/testutil/pgtest"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.DriverMatrix(m))
}

// requirePGClientTools skips with the exact missing-tool reason: the
// backup tests shell out to pg_dump/pg_restore and cannot fake them.
func requirePGClientTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("pg backup tests need %s on PATH (the embedded postgres bundle ships server binaries only): %v", tool, err)
		}
	}
}

// requirePostgresLeg pins the test to the postgres matrix run.
func requirePostgresLeg(t *testing.T) {
	t.Helper()
	if pgtest.ActiveDriver() != pgtest.DriverPostgres {
		t.Skip("pg backup round-trip runs on the postgres matrix leg (ORENDA_TEST_DRIVERS)")
	}
	requirePGClientTools(t)
}

func TestResolvePGTool(t *testing.T) {
	// A fake client install: an executable the PATH lookup can find.
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "pg_dump-fake")
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	t.Setenv("PATH", binDir)

	t.Run("override bare name resolves via PATH", func(t *testing.T) {
		got, err := backup.ResolvePGTool("pg_dump", "pg_dump-fake")
		require.NoError(t, err)
		assert.Equal(t, fake, got)
	})

	t.Run("override full path wins outright", func(t *testing.T) {
		got, err := backup.ResolvePGTool("pg_dump", fake)
		require.NoError(t, err)
		assert.Equal(t, fake, got)
	})

	t.Run("override missing path errors with the tool name", func(t *testing.T) {
		_, err := backup.ResolvePGTool("pg_dump", filepath.Join(t.TempDir(), "nope"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pg_dump")
	})

	t.Run("PATH fallback then explicit hint error", func(t *testing.T) {
		// Nothing named pg_dump on PATH or in the embedded cache →
		// the error must say where to get the tool, not promise an
		// embedded dump.
		_, err := backup.ResolvePGTool("pg_dump", "")
		if _, lookErr := exec.LookPath("pg_dump"); lookErr == nil {
			t.Skip("system pg_dump is on PATH; the no-tool error path is unreachable here")
		}
		require.Error(t, err)
		assert.Contains(t, err.Error(), "postgresql-client")
		assert.Contains(t, err.Error(), "storage.postgres.dump_bin")
	})
}

// TestBackupPG_SnapshotMissingDumpBinFails: with a dump binary override
// that cannot resolve, Snapshot must fail with the install hint — the
// protective behavior the pre-T366 reject-gate provided, now at the
// point where it is actually true. No server, no *sql.DB needed.
func TestBackupPG_SnapshotMissingDumpBinFails(t *testing.T) {
	svc := backup.New(backup.Config{
		Dialect:     backup.DialectPostgres,
		SnapshotDir: t.TempDir(),
		Postgres: backup.PostgresConfig{
			DSN:     "postgres://127.0.0.1:5432/orenda",
			DumpBin: filepath.Join(t.TempDir(), "definitely-not-pg_dump"),
		},
	}, nil)

	_, err := svc.Snapshot(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pg_dump")
	assert.Contains(t, err.Error(), "override", "a broken dump_bin override must name the misconfigured source")
}

// TestBackupPG_SnapshotRestoreRoundTrip is the DoD core: seed data on a
// real postgres, pg_dump it via the backup Service, restore via the
// scratch-verify path, then restore into a kept database and read the
// seeded rows back — dump → restore → data survives.
func TestBackupPG_SnapshotRestoreRoundTrip(t *testing.T) {
	requirePostgresLeg(t)

	pool, dsn := pgtest.TemplateDBNamed(t)
	ctx := context.Background()

	// Probe table + rows through the shim pool (sqlite-syntax inserts,
	// exactly what the app's repositories speak).
	_, err := pool.ExecContext(ctx, `CREATE TABLE backup_pg_roundtrip (id TEXT PRIMARY KEY, payload TEXT)`)
	require.NoError(t, err)
	for _, row := range [][2]string{{"r1", "alpha"}, {"r2", "beta"}} {
		_, err := pool.ExecContext(ctx, `INSERT INTO backup_pg_roundtrip (id, payload) VALUES (?, ?)`, row[0], row[1])
		require.NoError(t, err)
	}

	snapDir := filepath.Join(t.TempDir(), "snapshots")
	svc := backup.New(backup.Config{
		Dialect:              backup.DialectPostgres,
		SnapshotDir:          snapDir,
		SnapshotRotationDays: 30,
		Postgres:             backup.PostgresConfig{DSN: dsn},
	}, pool)

	path, err := svc.Snapshot(ctx)
	require.NoError(t, err)
	assert.Equal(t, ".dump", filepath.Ext(path), "pg snapshot must carry the .dump suffix")
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0), "custom-format dump must not be empty")

	// Maintenance-verify shape: scratch restore + verify + teardown.
	res, err := svc.RestorePostgres(ctx, path, "")
	require.NoError(t, err)
	assert.False(t, res.Kept)
	assert.Greater(t, res.TOCEntries, 0, "pg_restore --list must see the dumped objects")

	// Promotion shape: keep the restored database and read the seeded
	// rows back from it.
	kept := "orenda_backup_pg_rt_kept"
	res, err = svc.RestorePostgres(ctx, path, kept)
	require.NoError(t, err)
	assert.True(t, res.Kept)
	assert.Equal(t, kept, res.Database)

	keptDSN := swapDatabase(t, dsn, kept)
	keptDB, err := postgres.Open(ctx, keptDSN)
	require.NoError(t, err)
	defer func() { _ = keptDB.Close() }()
	// Cleanup runs after the test's contexts are done — a detached
	// background context is the only correct choice here.
	cleanupCtx := context.Background()
	t.Cleanup(func() {
		admin, err := postgres.Open(cleanupCtx, dsn)
		require.NoError(t, err)
		defer func() { _ = admin.Close() }()
		_, err = admin.ExecContext(cleanupCtx,
			fmt.Sprintf(`DROP DATABASE IF EXISTS "%s" WITH (FORCE)`, kept))
		require.NoError(t, err)
	})

	var payload string
	require.NoError(t, keptDB.QueryRowContext(ctx, `SELECT payload FROM backup_pg_roundtrip WHERE id = 'r2'`).Scan(&payload))
	assert.Equal(t, "beta", payload, "restored database must carry the dumped rows")

	var applied int
	require.NoError(t, keptDB.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&applied))
	assert.Greater(t, applied, 0, "restored orenda dump carries applied migrations")

	// A second --to run over the kept database must refuse (no silent
	// overwrite of the operator's promotion target).
	_, err = svc.RestorePostgres(ctx, path, kept)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

// TestBackupPG_SnapshotRotation: the pg snapshots share the sqlite
// naming + mtime-rotation convention — old .dump files with aged mtimes
// are removed when a fresh snapshot lands.
func TestBackupPG_SnapshotRotation(t *testing.T) {
	requirePostgresLeg(t)

	pool, dsn := pgtest.TemplateDBNamed(t)
	snapDir := filepath.Join(t.TempDir(), "snapshots")
	require.NoError(t, os.MkdirAll(snapDir, 0o755))

	old := filepath.Join(snapDir, "orenda-20000101-000000.dump")
	require.NoError(t, os.WriteFile(old, []byte("stale"), 0o644))
	stale := time.Now().AddDate(0, 0, -90)
	require.NoError(t, os.Chtimes(old, stale, stale))

	svc := backup.New(backup.Config{
		Dialect:              backup.DialectPostgres,
		SnapshotDir:          snapDir,
		SnapshotRotationDays: 30,
		Postgres:             backup.PostgresConfig{DSN: dsn},
	}, pool)

	path, err := svc.Snapshot(context.Background())
	require.NoError(t, err)
	assert.FileExists(t, path, "fresh snapshot survives rotation")
	assert.NoFileExists(t, old, "snapshot older than the rotation window is removed")
}

// swapDatabase repoints a libpq URI at another database on the same
// server (the tests' local mirror of the package's scratch-DSN swap).
func swapDatabase(t *testing.T, dsn, database string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.Path = "/" + database
	return u.String()
}

// TestBackup_ListSnapshots_IncludesDump exercises the shared list on
// both legs without a server: a .dump file sits next to .db files.
func TestBackup_ListSnapshots_IncludesDump(t *testing.T) {
	dir := t.TempDir()
	svc := backup.New(backup.Config{SnapshotDir: dir, Dialect: backup.DialectPostgres}, nil)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "orenda-20000101-000000.dump"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "orenda-20000102-000000.db"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{}"), 0o644))

	snaps, err := svc.ListSnapshots(context.Background())
	require.NoError(t, err)
	require.Len(t, snaps, 2, "only snapshot artifacts are listed")
}

// TestBackupPG_RestoreCorruptDumpLeavesNoScratch confirms the T374
// finding on the postgres leg: restore is verify-before-promote by
// construction, no reordering needed. A dump pg_restore cannot read
// fails the restore before anything is promoted, the scratch database
// is dropped (no artifact left on the server), and the live database
// the DSN points at keeps its own data — RestorePostgres never writes
// there.
func TestBackupPG_RestoreCorruptDumpLeavesNoScratch(t *testing.T) {
	requirePostgresLeg(t)

	pool, dsn := pgtest.TemplateDBNamed(t)
	ctx := context.Background()

	// Sentinel data in the live database — the failed restore must
	// leave it exactly as it was.
	_, err := pool.ExecContext(ctx, `CREATE TABLE restore_live_sentinel (id TEXT PRIMARY KEY, payload TEXT)`)
	require.NoError(t, err)
	_, err = pool.ExecContext(ctx, `INSERT INTO restore_live_sentinel (id, payload) VALUES ('live', 'untouched')`)
	require.NoError(t, err)

	garbage := filepath.Join(t.TempDir(), "orenda-corrupt.dump")
	require.NoError(t, os.WriteFile(garbage, []byte("this is not a pg_dump archive"), 0o644))

	svc := backup.New(backup.Config{
		Dialect:     backup.DialectPostgres,
		SnapshotDir: t.TempDir(),
		Postgres:    backup.PostgresConfig{DSN: dsn},
	}, pool)

	_, err = svc.RestorePostgres(ctx, garbage, "")
	require.Error(t, err, "a dump pg_restore cannot read must fail the restore")
	assert.Contains(t, err.Error(), "pg_restore")

	// No scratch left behind: the fail path drops it WITH (FORCE).
	admin, err := postgres.Open(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = admin.Close() }()
	var scratches int
	require.NoError(t, admin.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_database WHERE datname ~ '^orenda_restore_[0-9]+$'`).Scan(&scratches))
	assert.Zero(t, scratches, "a failed restore must leave no scratch database behind")

	// The live database was never touched.
	var payload string
	require.NoError(t, pool.QueryRowContext(ctx,
		`SELECT payload FROM restore_live_sentinel WHERE id = 'live'`).Scan(&payload))
	assert.Equal(t, "untouched", payload)
}
