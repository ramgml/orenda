package postgres

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmbeddedLifecycle is the real postmaster smoke: start → serve
// queries → stop → verify no leftover postgres processes → start again
// on the same DataPath (cluster reuse) → stop. Gated because the first
// run downloads the zonky binaries (~50 MB, cached under
// ~/.embedded-postgres-go):
//
//	ORENDA_TEST_EMBEDDED_PG=1 go test ./internal/storage/postgres/ -run TestEmbeddedLifecycle -v
func TestEmbeddedLifecycle(t *testing.T) {
	if os.Getenv("ORENDA_TEST_EMBEDDED_PG") != "1" {
		t.Skip("integration smoke: set ORENDA_TEST_EMBEDDED_PG=1 to run the embedded lifecycle against a real postmaster")
	}

	dataPath := filepath.Join(t.TempDir(), "postgres")
	opts, err := BuildEmbeddedOptions("orenda_t363_test", 0, "", "")
	require.NoError(t, err)
	opts.DataPath = dataPath
	opts.Logs = testLogWriter{t: t}

	t.Run("start, query, stop", func(t *testing.T) {
		cluster := startCluster(t, opts)

		// Dial through the exact RuntimeConfig the seam will use.
		dsn, err := ResolveDSN(opts.Connection())
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		dsn2, pool, err := OpenRuntime(ctx, RuntimeConfig{DSN: dsn, BusyTimeoutMs: 1200})
		require.NoError(t, err)
		defer func() { _ = pool.Close() }()
		assert.Equal(t, dsn, dsn2)
		var one int
		require.NoError(t, pool.QueryRowContext(ctx, "SELECT 1").Scan(&one))
		assert.Equal(t, 1, one)

		require.NoError(t, cluster.Stop())
		requireNoPostmaster(t, dataPath)
		assert.NoFileExists(t, filepath.Join(dataPath, "postmaster.pid"), "graceful stop removes the pid file")
		// Scratch runtime dir is removed with the cluster.
		assert.DirExists(t, dataPath, "data directory survives for reuse")
	})

	t.Run("restart reuses the cluster", func(t *testing.T) {
		cluster := startCluster(t, opts)

		dsn, err := ResolveDSN(opts.Connection())
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, pool, err := OpenRuntime(ctx, RuntimeConfig{DSN: dsn})
		require.NoError(t, err)
		defer func() { _ = pool.Close() }()
		// The database created by initdb in the previous run proves the
		// data directory was reused, not re-initialized.
		var datname string
		require.NoError(t, pool.QueryRowContext(ctx,
			`SELECT datname FROM pg_database WHERE datname = 'orenda_t363_test'`).Scan(&datname))
		assert.Equal(t, opts.Database, datname)

		require.NoError(t, cluster.Stop())
		requireNoPostmaster(t, dataPath)
	})
}

// startCluster starts opts and registers an unconditional cleanup stop,
// so a failing test can never leak a postmaster.
func startCluster(t *testing.T, opts EmbeddedOptions) *Embedded {
	t.Helper()
	cluster, err := StartEmbedded(opts)
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := cluster.Stop(); err != nil {
			t.Logf("cleanup stop: %v", err)
		}
	})
	return cluster
}

// requireNoPostmaster asserts no postgres process references the data
// path — the "clean Stop" contract (no orphaned postmaster after stop).
func requireNoPostmaster(t *testing.T, dataPath string) {
	t.Helper()
	out, err := exec.Command("pgrep", "-af", dataPath).Output()
	if err != nil {
		// pgrep exits 1 when nothing matches — exactly what we want.
		return
	}
	t.Fatalf("postmaster still running after stop: %s", strings.TrimSpace(string(out)))
}

type testLogWriter struct {
	t *testing.T
}

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Logf("postmaster: %s", strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
