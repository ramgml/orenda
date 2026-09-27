package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/ramgml/orenda/internal/storage/shim"
)

// RuntimeConfig carries the postgres connection target in driver-neutral
// form. It is the adapter's open surface: the storage seam and the CLI
// map their own config structs into it, ResolveDSN turns it into a
// libpq connection string, and OpenRuntime dials it.
//
// DSN takes priority: when set, the individual parts are ignored
// (wiki:storage-adapters D4). Embedded=true in the caller's config does
// NOT belong here — the embedded runtime builds its RuntimeConfig via
// EmbeddedConnection.
type RuntimeConfig struct {
	DSN           string
	Host          string
	Port          int
	User          string
	Password      string
	Database      string
	SSLMode       string
	BusyTimeoutMs int
}

// ResolveDSN returns the libpq connection string for cfg. A configured
// DSN wins outright; otherwise the parts are assembled into a URL with
// libpq defaults (127.0.0.1:5432, sslmode=prefer when unset) — the
// database name is required, guessing one would silently target the
// wrong cluster.
func ResolveDSN(cfg RuntimeConfig) (string, error) {
	if cfg.DSN != "" {
		return cfg.DSN, nil
	}
	if cfg.Database == "" {
		return "", fmt.Errorf("postgres: database is required when dsn is not set")
	}
	host := cfg.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := cfg.Port
	if port == 0 {
		port = 5432
	}
	u := url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
		Path:   cfg.Database,
	}
	switch {
	case cfg.User != "" && cfg.Password != "":
		u.User = url.UserPassword(cfg.User, cfg.Password)
	case cfg.User != "":
		u.User = url.User(cfg.User)
	}
	q := url.Values{}
	if cfg.SSLMode != "" {
		q.Set("sslmode", cfg.SSLMode)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// OpenRuntime opens the runtime connection pool for the postgres
// dialect: pgx stdlib connector wrapped in the SQLite→PostgreSQL
// dialect shim, so the returned *sql.DB drops into every
// sqlite.New*Repository constructor unchanged (wiki:storage-adapters
// D1/D4). It returns the resolved DSN alongside the pool — the migration
// helpers need it for their dedicated simple-protocol handle.
//
// Pool shape deliberately differs from the sqlite driver: no
// single-writer cap. The sqlite SetMaxOpenConns(1) exists to keep
// writers off SQLITE_BUSY; PostgreSQL multiplexes concurrent writers
// through MVCC and row locks, so the cap would only serialize the
// application against its own throughput. The pool stays at the
// database/sql default (unlimited) — a single-process local app is
// bounded by the server's max_connections, and pgx's default statement
// cache stays enabled.
func OpenRuntime(ctx context.Context, cfg RuntimeConfig) (string, *sql.DB, error) {
	dsn, err := ResolveDSN(cfg)
	if err != nil {
		return "", nil, err
	}
	pgCfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return "", nil, fmt.Errorf("postgres: parse dsn: %w", err)
	}
	if cfg.BusyTimeoutMs > 0 {
		// SQLite's busy_timeout bounds how long a statement waits on a
		// conflicting lock; PostgreSQL's session-level lock_timeout is
		// the closest equivalent (waits on table/row locks before the
		// statement errors). Applied per connection in the driver's
		// after-connect hook, before the shim ever sees the connection.
		// Always qualified with a unit — a bare "1500" would parse as
		// seconds and silently multiply the configured wait.
		pgCfg.AfterConnect = func(ctx context.Context, pgConn *pgconn.PgConn) error {
			_, err := pgConn.Exec(ctx, "SET lock_timeout = '"+fmt.Sprintf("%dms", cfg.BusyTimeoutMs)+"'").ReadAll()
			return err
		}
	}
	db := sql.OpenDB(shim.NewConnector(stdlib.GetConnector(*pgCfg), shim.DialectPostgres))
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return "", nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return dsn, db, nil
}
