// Package postgres is the PostgreSQL storage adapter's schema home: the
// baseline DDL (T361, consolidating the sqlite migration chain 001–049)
// and the migration runner that applies and rolls it back.
//
// The runner mirrors the sqlite runner's semantics (internal/storage/
// sqlite/db.go): a schema_migrations bookkeeping table, per-file
// transactions, and `<version>.down.sql` companions for rollback.
// PostgreSQL's transactional DDL makes the per-file transaction atomic
// without the sqlite runner's foreign_keys=OFF escape hatch, and the
// `-- orenda:foreign_keys_off` marker therefore has no meaning here.
//
// The package is deliberately independent of the driver-neutral storage
// seam (internal/storage): the seam's postgres rebind lands with T362,
// and until then `orenda migrate` wires this package directly.
package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Open connects to PostgreSQL at dsn and returns a database handle
// configured for the migration runner.
//
// The simple query protocol is required: migration files are
// multi-statement SQL bodies executed with a single Exec, which the
// extended protocol rejects ("cannot insert multiple commands into a
// prepared statement"). Single-statement callers are unaffected by the
// mode.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse dsn: %w", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	db := stdlib.OpenDB(*cfg)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return db, nil
}
