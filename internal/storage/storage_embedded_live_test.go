package storage

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/storage/postgres"
)

// pgTestLogWriter feeds postmaster output into the test log.
type pgTestLogWriter struct {
	t *testing.T
}

func (w pgTestLogWriter) Write(p []byte) (int, error) {
	w.t.Logf("postmaster: %s", strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// TestOpen_PostgresEmbeddedLive covers the seam's embedded branch end
// to end: Open dials the bootstrap cluster (loopback, embedded port)
// and the busy_timeout → lock_timeout mapping applies there exactly
// like on the DSN path. Gated because it starts a real postmaster:
//
//	ORENDA_TEST_EMBEDDED_PG=1 go test ./internal/storage/ -run TestOpen_PostgresEmbeddedLive -v
func TestOpen_PostgresEmbeddedLive(t *testing.T) {
	if os.Getenv("ORENDA_TEST_EMBEDDED_PG") != "1" {
		t.Skip("integration smoke: set ORENDA_TEST_EMBEDDED_PG=1 to run the embedded branch against a real postmaster")
	}

	// A free port so parallel runs never collide.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())

	opts, err := postgres.BuildEmbeddedOptions("orenda_t363_seam", port, "", "")
	require.NoError(t, err)
	opts.DataPath = t.TempDir()
	scratch, err := postgres.ScratchRuntimePath()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(scratch) })
	opts.RuntimePath = scratch
	opts.Logs = pgTestLogWriter{t: t}

	cluster, err := postgres.StartEmbedded(opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cluster.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := Open(ctx, Config{
		Driver:        "postgres",
		BusyTimeoutMs: 2500,
		Postgres: PostgresConfig{
			Embedded:     true,
			Database:     opts.Database,
			EmbeddedPort: port,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	assert.Equal(t, DialectPostgres, db.Dialect())
	var lockTimeout string
	require.NoError(t, db.DB.QueryRowContext(ctx, "SHOW lock_timeout").Scan(&lockTimeout))
	assert.Equal(t, "2500ms", lockTimeout, "embedded branch must map busy_timeout like the DSN path")
}
