// Package postgres is the PostgreSQL storage adapter: the baseline DDL
// (T361, consolidating the sqlite migration chain 001–049), the
// migration runner that applies and rolls it back, the runtime pool
// builder for the driver-neutral seam (runtime.go — OpenRuntime wraps
// the pgx stdlib connector in the dialect shim), and the embedded
// cluster lifecycle (embedded.go, T363).
//
// The runner mirrors the sqlite runner's semantics (internal/storage/
// sqlite/db.go): a schema_migrations bookkeeping table, per-file
// transactions, and `<version>.down.sql` companions for rollback.
// PostgreSQL's transactional DDL makes the per-file transaction atomic
// without the sqlite runner's foreign_keys=OFF escape hatch, and the
// `-- orenda:foreign_keys_off` marker therefore has no meaning here.
//
// Open (this file) dials with the simple query protocol: migration
// files are multi-statement SQL bodies executed with a single Exec,
// which the extended protocol rejects. The dialect-aware migrate
// helpers on the seam's *DB use exactly this opener; the application's
// runtime pool (OpenRuntime) keeps pgx's default cached-statement mode.
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
