package pgtest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTemplateDB_CloneAndIsolation is the accelerator's own smoke: the
// embedded (or ORENDA_TEST_PG_DSN) server provisions once, the template
// carries the migrated baseline, and every clone is an independent,
// pristine database. The ?-placeholder probe proves the dialect shim is
// live on the returned pool.
func TestTemplateDB_CloneAndIsolation(t *testing.T) {
	hasPostgres := false
	for _, d := range Drivers() {
		if d == DriverPostgres {
			hasPostgres = true
		}
	}
	if !hasPostgres {
		t.Skip("postgres leg opted out: ORENDA_TEST_DRIVERS does not include postgres")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
