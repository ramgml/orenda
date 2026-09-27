// Package storage is the driver-neutral storage seam.
//
// Production code opens databases and resolves storage errors through
// this package instead of importing a driver package directly. Today
// the only driver is sqlite (internal/storage/sqlite); the postgres
// adapter lands in a later phase of the storage-adapters design
// (wiki:storage-adapters, D1/D5). Selecting driver=postgres fails fast
// here with a clear not-implemented error — no silent fallback.
//
// The DB handle embeds *sql.DB, so it satisfies any repository
// constructor taking a *sql.DB via its promoted method set; helpers
// below unwrap to *sql.DB because that remains the common currency of
// the repository constructors.
package storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// Dialect names the SQL dialect a DB handle speaks.
type Dialect string

// Supported dialects.
const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
)

// DB is a driver-neutral database handle: a *sql.DB plus the dialect
// it speaks. Driver-specific tuning (SQLite pragmas, single-writer
// connection cap) is applied inside the driver's own open path.
type DB struct {
	*sql.DB
	dialect Dialect
}

// Dialect reports which SQL dialect this handle speaks.
func (db *DB) Dialect() Dialect { return db.dialect }

// PostgresConfig carries the postgres connection parameters (T360:
// structurally validated in config, not yet consumed at runtime).
type PostgresConfig struct {
	Host         string
	Port         int
	User         string
	Password     string
	Database     string
	Embedded     bool
	EmbeddedPort int
}

// Config is the driver-neutral open configuration. Driver is
// "sqlite" (default when empty) or "postgres".
type Config struct {
	Driver        string
	Path          string // sqlite database file path
	WALMode       bool   // sqlite: WAL journal mode
	EnableForeign bool   // sqlite: foreign_keys pragma
	BusyTimeoutMs int    // sqlite: busy_timeout
	Postgres      PostgresConfig
}

// Open connects to the configured driver and returns a *DB handle.
//
// driver=postgres is rejected with a not-implemented error until the
// postgres adapter lands; unknown drivers are rejected outright.
// SQLite connection tuning (SetMaxOpenConns(1), pragmas) happens
// inside the sqlite driver, exactly as before the seam existed.
func Open(ctx context.Context, cfg Config) (*DB, error) {
	switch Dialect(cfg.Driver) {
	case "", DialectSQLite:
		db, err := sqlite.Open(ctx, cfg.Path, sqlite.OpenConfig{
			WALMode:       cfg.WALMode,
			EnableForeign: cfg.EnableForeign,
			BusyTimeoutMs: cfg.BusyTimeoutMs,
		})
		if err != nil {
			return nil, err
		}
		return &DB{DB: db, dialect: DialectSQLite}, nil
	case DialectPostgres:
		return nil, fmt.Errorf("storage: postgres driver is not implemented yet (this build ships sqlite only); set storage.driver to sqlite or remove the override")
	default:
		return nil, fmt.Errorf("storage: unknown driver %q (must be sqlite or postgres)", cfg.Driver)
	}
}

// Migrate applies pending schema migrations on the sqlite dialect.
// The migration set lives in the sqlite driver (embedded FS); the
// postgres baseline arrives with the postgres adapter.
func Migrate(ctx context.Context, db *sql.DB) error {
	return sqlite.Migrate(ctx, db, sqlite.MigrationsFS, "migrations")
}

// MigrateDown rolls back the most recent migration via its .down.sql
// companion (sqlite dialect).
func MigrateDown(ctx context.Context, db *sql.DB) error {
	return sqlite.MigrateDown(ctx, db, sqlite.MigrationsFS, "migrations")
}

// AppliedVersions lists applied migration versions (sqlite dialect).
func AppliedVersions(ctx context.Context, db *sql.DB) ([]string, error) {
	return sqlite.AppliedVersions(ctx, db)
}
