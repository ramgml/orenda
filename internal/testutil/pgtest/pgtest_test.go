package pgtest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain routes this package's own tests through the driver matrix
// so the embedded cluster is provisioned AND shut down by
// DriverMatrix — a bare TemplateDB call would leak the postmaster and
// its temp directories at binary exit.
func TestMain(m *testing.M) {
	os.Exit(DriverMatrix(m))
}

// TestTemplateDB_CloneAndIsolation is the accelerator's own smoke: the
// embedded (or ORENDA_TEST_PG_DSN) server provisions once, the template
// carries the migrated baseline, and every clone is an independent,
// pristine database. The ?-placeholder probe proves the dialect shim is
// live on the returned pool.
func TestTemplateDB_CloneAndIsolation(t *testing.T) {
	if ActiveDriver() != DriverPostgres {
		t.Skip("accelerator smoke exercises the postgres leg only; skipped on the sqlite leg of the matrix to keep the cluster start off that run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	db1 := TemplateDB(t)
	db2 := TemplateDB(t)

	// Baseline schema is present in every clone.
	var n int
	require.NoError(t, db1.QueryRowContext(ctx,
		`SELECT count(*) FROM schema_migrations`).Scan(&n))
	assert.Greater(t, n, 0, "template clone must carry applied migrations")

	// Writes stay private per clone.
	_, err := db1.ExecContext(ctx, `CREATE TABLE pgtest_probe (id TEXT PRIMARY KEY)`)
	require.NoError(t, err)
	// The shim rewrites ? → $1 before the driver sees the statement.
	_, err = db1.ExecContext(ctx, `INSERT INTO pgtest_probe (id) VALUES (?)`, "x")
	require.NoError(t, err)
	err = db2.QueryRowContext(ctx, `SELECT count(*) FROM pgtest_probe`).Scan(&n)
	require.Error(t, err, "clone 2 must not see clone 1's tables")

	// And a second read on clone 1 still sees its own row.
	var got string
	require.NoError(t, db1.QueryRowContext(ctx,
		`SELECT id FROM pgtest_probe WHERE id = ?`, "x").Scan(&got))
	assert.Equal(t, "x", got)
}

// TestDrivers_Parsing pins the ORENDA_TEST_DRIVERS contract.
func TestDrivers_Parsing(t *testing.T) {
	t.Setenv("ORENDA_TEST_DRIVERS", "")
	assert.Equal(t, []string{DriverSQLite, DriverPostgres}, Drivers())

	t.Setenv("ORENDA_TEST_DRIVERS", "sqlite")
	assert.Equal(t, []string{DriverSQLite}, Drivers())

	t.Setenv("ORENDA_TEST_DRIVERS", " postgres , sqlite ")
	assert.Equal(t, []string{DriverPostgres, DriverSQLite}, Drivers())

	t.Setenv("ORENDA_TEST_DRIVERS", "sqlite,sqlite")
	assert.Equal(t, []string{DriverSQLite, DriverSQLite}, Drivers())
}

// sweepDeadPID returns a pid of an already-exited process — the
// "dead" anchor for the sweep tests.
func sweepDeadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	_ = cmd.Wait()
	return pid
}

// writeSweepDir fabricates an orenda-pgtest-data-* dir with the given
// files (nil value = absent file) in an isolated base for the pure
// classify tests; sweep-visible dirs are made by TestSweepStaleTestClusters.
func writeSweepDir(t *testing.T, files map[string]*string) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	for name, content := range files {
		if content == nil {
			continue
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(*content), 0o644))
	}
	return dir
}

// TestClassifyStaleCluster pins every sweep decision: the living are
// protected (live preview marker, live postmaster, live owner), the
// provably dead are reclaimed, the unknown is skipped.
func TestClassifyStaleCluster(t *testing.T) {
	pid := fmt.Sprint(os.Getpid())

	t.Run("dead postmaster pid is reclaimed", func(t *testing.T) {
		dir := writeSweepDir(t, map[string]*string{
			"postmaster.pid": new(fmt.Sprint(sweepDeadPID(t)) + "\n"),
		})
		_, reason, ok := classifyStaleCluster(dir)
		assert.True(t, ok)
		assert.Contains(t, reason, "dead postmaster")
	})

	t.Run("live postmaster is protected with a stop hint", func(t *testing.T) {
		dir := writeSweepDir(t, map[string]*string{
			"postmaster.pid": new(pid + "\n"),
		})
		_, reason, ok := classifyStaleCluster(dir)
		assert.False(t, ok)
		assert.Contains(t, reason, "live postmaster")
	})

	t.Run("live PREVIEW_OWNER is protected", func(t *testing.T) {
		dir := writeSweepDir(t, map[string]*string{
			"postmaster.pid": new(fmt.Sprint(sweepDeadPID(t)) + "\n"),
			"PREVIEW_OWNER":  new("pid=" + pid + "\nowner=pm\npurpose=review\n"),
		})
		_, reason, ok := classifyStaleCluster(dir)
		assert.False(t, ok, "a dead postmaster under a live marker is still protected")
		assert.Contains(t, reason, "live PREVIEW_OWNER")
	})

	t.Run("dead PREVIEW_OWNER is reclaimed", func(t *testing.T) {
		dir := writeSweepDir(t, map[string]*string{
			"postmaster.pid": new(fmt.Sprint(sweepDeadPID(t)) + "\n"),
			"PREVIEW_OWNER":  new(fmt.Sprintf("pid=%d\nowner=pm\n", sweepDeadPID(t))),
		})
		_, reason, ok := classifyStaleCluster(dir)
		assert.True(t, ok)
		assert.Contains(t, reason, "dead PREVIEW_OWNER")
	})

	t.Run("invalid PREVIEW_OWNER is protected", func(t *testing.T) {
		dir := writeSweepDir(t, map[string]*string{
			"postmaster.pid": new(fmt.Sprint(sweepDeadPID(t)) + "\n"),
			"PREVIEW_OWNER":  new("owner=pm\n"),
		})
		_, reason, ok := classifyStaleCluster(dir)
		assert.False(t, ok, "unknown ownership must read as protected")
		assert.Contains(t, reason, "unparseable PREVIEW_OWNER")
	})

	t.Run("live owner without pid file is an in-flight start", func(t *testing.T) {
		dir := writeSweepDir(t, map[string]*string{
			"PGTEST_OWNER": new("pid=" + pid + "\nruntime_path=/nowhere\n"),
		})
		_, reason, ok := classifyStaleCluster(dir)
		assert.False(t, ok)
		assert.Empty(t, reason, "in-flight dirs are skipped silently")
	})

	t.Run("dead owner without pid file is reclaimed", func(t *testing.T) {
		dir := writeSweepDir(t, map[string]*string{
			"PGTEST_OWNER": new(fmt.Sprintf("pid=%d\n", sweepDeadPID(t))),
		})
		_, reason, ok := classifyStaleCluster(dir)
		assert.True(t, ok)
		assert.Contains(t, reason, "dead owner")
	})

	t.Run("no evidence at all is skipped", func(t *testing.T) {
		dir := writeSweepDir(t, nil)
		_, reason, ok := classifyStaleCluster(dir)
		assert.False(t, ok)
		assert.Empty(t, reason)
	})
}

// TestSweepStaleTestClusters is the end-to-end sweep: fabricated
// leftovers in the real temp dir — a dead-owner cluster paired with a
// runtime scratch dir, and a live cluster of a "concurrent" binary —
// prove the sweep reclaims exactly the dead and leaves the live.
func TestSweepStaleTestClusters(t *testing.T) {
	// Dead cluster with a paired runtime dir (both must go).
	deadDir, err := os.MkdirTemp("", dataDirPrefix+"t370-dead-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(deadDir) })
	deadRuntime, err := os.MkdirTemp("", runtimePrefix+"t370-dead-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(deadRuntime) })
	require.NoError(t, os.WriteFile(filepath.Join(deadDir, pgtestOwnerFile),
		[]byte(fmt.Sprintf("pid=%d\nruntime_path=%s\n", sweepDeadPID(t), deadRuntime)), 0o644))

	// Live cluster of a concurrent binary (must stay).
	liveDir, err := os.MkdirTemp("", dataDirPrefix+"t370-live-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(liveDir) })
	require.NoError(t, os.WriteFile(filepath.Join(liveDir, "postmaster.pid"),
		[]byte(fmt.Sprint(os.Getpid())+"\n"+liveDir+"\n"), 0o644))

	sweepStaleTestClusters(t)

	_, deadErr := os.Stat(deadDir)
	assert.True(t, os.IsNotExist(deadErr), "the dead cluster dir must be swept")
	_, runtimeErr := os.Stat(deadRuntime)
	assert.True(t, os.IsNotExist(runtimeErr), "the paired runtime dir must be swept with its cluster")
	_, liveErr := os.Stat(liveDir)
	assert.NoError(t, liveErr, "the live cluster dir must survive the sweep")

	// A foreign path sharing the temp dir must never be touched.
	foreign, err := os.MkdirTemp("", "orenda-pgtest-not-a-cluster-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(foreign) })
	sweepStaleTestClusters(t)
	_, err = os.Stat(foreign)
	assert.NoError(t, err, "the sweep must stay inside its own prefix")
}
