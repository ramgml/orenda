// Package storage is the driver-neutral storage seam.
//
// Production code opens databases and resolves storage errors through
// this package instead of importing a driver package directly. Two
// drivers ship: sqlite (internal/storage/sqlite, default) and postgres
// (internal/storage/postgres, via the pgx stdlib connector and the
// SQLite→PostgreSQL dialect shim — the same *sql.DB drops into every
// sqlite.New*Repository constructor, wiki:storage-adapters D1/D4).
//
// The DB handle embeds *sql.DB, so it satisfies any repository
// constructor taking a *sql.DB via its promoted method set; the
// dialect-aware Migrate/MigrateDown/AppliedVersions methods dispatch on
// the dialect recorded at open time.
package storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ramgml/orenda/internal/storage/postgres"
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
// connection cap, postgres pool shape) is applied inside the driver's
// own open path.
type DB struct {
	*sql.DB
	dialect Dialect
	// pgDsn is the resolved libpq connection string for the postgres
	// dialect. The migration runner needs the simple query protocol
	// (multi-statement migration files), which the runtime pool's
	// cached-statement mode rejects — the migrate helpers below dial a
	// short-lived simple-protocol handle from this DSN instead.
	pgDsn string
}

// Dialect reports which SQL dialect this handle speaks.
func (db *DB) Dialect() Dialect { return db.dialect }

// Migrate applies pending schema migrations for this handle's dialect.
// Postgres runs through a dedicated simple-protocol handle — see the
// pgDsn comment on DB.
func (db *DB) Migrate(ctx context.Context) error {
	if db.dialect == DialectPostgres {
		return db.withMigrationHandle(ctx, func(h *sql.DB) error {
			return postgres.Migrate(ctx, h, postgres.MigrationsFS, "migrations")
		})
	}
	return sqlite.Migrate(ctx, db.DB, sqlite.MigrationsFS, "migrations")
}

// MigrateDown rolls back the most recent migration via its .down.sql
// companion, for this handle's dialect.
func (db *DB) MigrateDown(ctx context.Context) error {
	if db.dialect == DialectPostgres {
		return db.withMigrationHandle(ctx, func(h *sql.DB) error {
			return postgres.MigrateDown(ctx, h, postgres.MigrationsFS, "migrations")
		})
	}
	return sqlite.MigrateDown(ctx, db.DB, sqlite.MigrationsFS, "migrations")
}

// AppliedVersions lists applied migration versions for this handle's
// dialect. Single-statement queries — the runtime pool serves them
// directly on both dialects.
func (db *DB) AppliedVersions(ctx context.Context) ([]string, error) {
	if db.dialect == DialectPostgres {
		return postgres.AppliedVersions(ctx, db.DB)
	}
	return sqlite.AppliedVersions(ctx, db.DB)
}

// withMigrationHandle opens the short-lived simple-protocol handle the
// postgres migration runner requires (multi-statement migration bodies
// cannot go through prepared statements) and runs fn against it.
func (db *DB) withMigrationHandle(ctx context.Context, fn func(*sql.DB) error) error {
	h, err := postgres.Open(ctx, db.pgDsn)
	if err != nil {
		return fmt.Errorf("storage: open postgres migration handle: %w", err)
	}
	defer func() { _ = h.Close() }()
	return fn(h)
}

// PostgresConfig carries the postgres connection parameters. DSN takes
// priority over the individual parts (wiki:storage-adapters D4);
// Embedded selects the local cluster runtime instead of an external
// server (D7) — its connection parameters are derived from the
// bootstrap cluster, see internal/storage/postgres.EmbeddedOptions.
type PostgresConfig struct {
	DSN          string
	Host         string
	Port         int
	User         string
	Password     string
	Database     string
	SSLMode      string
	Embedded     bool
	EmbeddedPort int
	// BinariesURL overrides the Maven repository the embedded runtime
	// fetches postgres binaries from; empty uses the library default.
	BinariesURL string
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
// SQLite keeps its own tuning (SetMaxOpenConns(1), pragmas) inside
// internal/storage/sqlite; postgres dials through the dialect shim with
// an unbounded pool and pgx's default statement cache. Unknown drivers
// are rejected outright.
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
		pg := postgres.RuntimeConfig{
			DSN:           cfg.Postgres.DSN,
			Host:          cfg.Postgres.Host,
			Port:          cfg.Postgres.Port,
			User:          cfg.Postgres.User,
			Password:      cfg.Postgres.Password,
			Database:      cfg.Postgres.Database,
			SSLMode:       cfg.Postgres.SSLMode,
			BusyTimeoutMs: cfg.BusyTimeoutMs,
		}
		// The embedded cluster is dialed through its bootstrap
		// parameters — the lifecycle wrapper in the cmd layer starts
		// the cluster before Open and derives the same values.
		if cfg.Postgres.Embedded {
			opts, err := postgres.BuildEmbeddedOptions(
				cfg.Postgres.Database,
				cfg.Postgres.EmbeddedPort,
				cfg.Postgres.User,
				cfg.Postgres.Password,
			)
			if err != nil {
				return nil, fmt.Errorf("storage: %w", err)
			}
			pg = opts.Connection()
		}
		dsn, sqldb, err := postgres.OpenRuntime(ctx, pg)
		if err != nil {
			return nil, err
		}
		return &DB{DB: sqldb, dialect: DialectPostgres, pgDsn: dsn}, nil
	default:
		return nil, fmt.Errorf("storage: unknown driver %q (must be sqlite or postgres)", cfg.Driver)
	}
}

// Migrate applies pending schema migrations on the sqlite dialect.
// The migration set lives in the sqlite driver (embedded FS).
//
// Prefer (*DB).Migrate, which dispatches on the handle's dialect.
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
