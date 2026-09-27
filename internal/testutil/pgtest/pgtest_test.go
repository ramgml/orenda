package pgtest

import (
	"context"
	"os"
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
