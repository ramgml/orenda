package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This is the T363 integration smoke for the postgres branch of the
// seam: storage.Open dials through the pgx stdlib connector and the
// dialect shim, the dialect-aware migrate helpers apply the real
// postgres baseline, and the constraint classifier sees real server
// errors. Gated on ORENDA_TEST_PG_DSN (e.g.
// postgres://postgres:test@127.0.0.1:21545/postgres?sslmode=disable)
// against a throwaway server:
//
//	docker run --rm -d --name orenda-t363-pg -e POSTGRES_PASSWORD=test \
//	  -p 127.0.0.1:21545:5432 postgres:16-alpine
func TestOpen_PostgresLive(t *testing.T) {
	dsn := os.Getenv("ORENDA_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("integration smoke: set ORENDA_TEST_PG_DSN to a throwaway postgres (see test comment)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := Open(ctx, Config{
		Driver:        "postgres",
		BusyTimeoutMs: 1500,
		Postgres:      PostgresConfig{DSN: dsn},
	})
	require.NoError(t, err)
	// Registered BEFORE the probe-table drop below so cleanup runs
	// drop-then-close (t.Cleanup is LIFO); a defer would close the
	// pool first and strand the drop on a dead handle.
	t.Cleanup(func() { _ = db.Close() })

	assert.Equal(t, DialectPostgres, db.Dialect())

	// Migration routing: the dialect-aware helper applies the postgres
	// baseline through its simple-protocol handle.
	require.NoError(t, db.Migrate(ctx))
	versions, err := db.AppliedVersions(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, versions, "baseline must register applied versions")

	// busy_timeout → lock_timeout mapping via the after-connect hook:
	// the pool hands out connections with the session GUC pre-set
	// (SHOW echoes the unit the value was SET with).
	var lockTimeout string
	require.NoError(t, db.DB.
		QueryRowContext(ctx, "SHOW lock_timeout").
		Scan(&lockTimeout))
	assert.Equal(t, "1500ms", lockTimeout)

	// Classifier against a real 23505: the SQLSTATE branch must fire
	// through errors.As on the pgconn error the server returns.
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS orenda_t363_unique_probe (id TEXT PRIMARY KEY)`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DROP TABLE IF EXISTS orenda_t363_unique_probe`)
	})
	_, err = db.ExecContext(ctx, `INSERT INTO orenda_t363_unique_probe (id) VALUES ('x')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO orenda_t363_unique_probe (id) VALUES ('x')`)
	require.Error(t, err)
	assert.True(t, IsUniqueViolation(err), "real 23505 must classify as unique violation: %v", err)
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "underlying pgconn error must stay unwrappable")
	assert.Equal(t, "23505", pgErr.Code)

	// A different error class must NOT match.
	_, err = db.ExecContext(ctx, "SELECT no_such_column FROM orenda_t363_unique_probe")
	require.Error(t, err)
	assert.False(t, IsUniqueViolation(err))
	fmt.Println("postgres live smoke: applied", len(versions), "versions; lock_timeout:", lockTimeout)
}
