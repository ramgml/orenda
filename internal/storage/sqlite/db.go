// Package sqlite provides an *sql.DB connection to the Orenda SQLite database
// plus a minimal embedded-SQL migration runner.
//
// Phase 0 scope: open the database with the recommended pragmas (WAL,
// foreign_keys, busy_timeout) and apply any pending *.sql files from the
// embedded migrations directory. Phase 1+ will add repository implementations.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	// Pure-Go SQLite driver registered as "sqlite".
	_ "modernc.org/sqlite"
)

// Open returns an *sql.DB connected to dbPath with the requested pragmas.
//
// cfg.WALMode, cfg.EnableForeign, and cfg.BusyTimeoutMs control the
// connection-level settings. Pragmas are applied via a single Exec that runs
// after connection (modernc/sqlite does not support DSN parameters for them).
func Open(ctx context.Context, dbPath string, cfg OpenConfig) (*sql.DB, error) {
	// DSN: add ?_pragma=... query params for the modernc driver as a belt-and-braces
	// approach in case Exec below races. The driver respects _pragma for
	// busy_timeout and journal_mode; foreign_keys must still be set per-conn.
	dsn := buildDSN(dbPath, cfg)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %q: %w", dbPath, err)
	}

	// Single writer for SQLite; modernc honours this with busy_timeout.
	db.SetMaxOpenConns(1)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: ping %q: %w", dbPath, err)
	}

	// Belt-and-braces: apply pragmas via Exec in case the DSN didn't pick them up.
	pragmas := buildPragmaSQL(cfg)
	if _, err := db.ExecContext(ctx, pragmas); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: apply pragmas: %w", err)
	}

	return db, nil
}

// OpenConfig holds the SQLite connection options the operator controls via config.
type OpenConfig struct {
	WALMode       bool
	EnableForeign bool
	BusyTimeoutMs int
}

// buildDSN returns a modernc-compatible DSN with optional _pragma parameters.
//
// Note: foreign_keys is intentionally NOT set here — modernc doesn't honour
// the per-connection foreign_keys pragma via _pragma, so we apply it through
// Exec in Open().
func buildDSN(dbPath string, cfg OpenConfig) string {
	params := []string{
		"_pragma=busy_timeout(" + itoa(cfg.BusyTimeoutMs) + ")",
	}
	if cfg.WALMode {
		params = append(params, "_pragma=journal_mode(WAL)")
	}
	return dbPath + "?" + strings.Join(params, "&")
}

// buildPragmaSQL returns the SQL applied after the connection is established.
//
// _pragma=foreign_keys is not honoured by the driver, so we run it here. This
// guarantees FK enforcement regardless of any future driver-version changes.
func buildPragmaSQL(cfg OpenConfig) string {
	var stmts []string
	if cfg.EnableForeign {
		stmts = append(stmts, "PRAGMA foreign_keys = ON;")
	}
	if cfg.WALMode {
		// Setting WAL is idempotent and a no-op if already in WAL.
		stmts = append(stmts, "PRAGMA journal_mode = WAL;")
	}
	if cfg.BusyTimeoutMs > 0 {
		stmts = append(stmts, fmt.Sprintf("PRAGMA busy_timeout = %d;", cfg.BusyTimeoutMs))
	}
	return strings.Join(stmts, "\n")
}

// itoa formats an int without importing strconv just for this.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// Migrate applies every embedded migration that hasn't been recorded in the
// schema_migrations table yet. Migration files are sorted by name (NNN_*.sql)
// and executed in order inside individual transactions.
//
// migrationsFS is expected to contain *.sql files in the directory dir
// (typically "migrations"); this allows embed patterns like
// //go:embed migrations/*.sql.
func Migrate(ctx context.Context, db *sql.DB, migrationsFS embed.FS, dir string) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
	`); err != nil {
		return fmt.Errorf("sqlite: create schema_migrations: %w", err)
	}

	applied, err := loadApplied(ctx, db)
	if err != nil {
		return err
	}

	files, err := collectMigrationFiles(migrationsFS, dir)
	if err != nil {
		return err
	}

	for _, name := range files {
		version := pathVersion(name)
		if _, ok := applied[version]; ok {
			continue
		}
		fullPath := path.Join(dir, name)
		body, err := migrationsFS.ReadFile(fullPath)
		if err != nil {
			return fmt.Errorf("sqlite: read migration %q: %w", fullPath, err)
		}
		if err := applyMigration(ctx, db, version, string(body)); err != nil {
			return fmt.Errorf("sqlite: apply migration %q: %w", fullPath, err)
		}
	}
	return nil
}

// loadApplied returns the set of already-applied migration versions.
func loadApplied(ctx context.Context, db *sql.DB) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: query schema_migrations: %w", err)
	}
	defer rows.Close()

	out := make(map[string]struct{})
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("sqlite: scan schema_migrations: %w", err)
		}
		out[v] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// collectMigrationFiles returns the sorted list of *.sql files under fsys/dir.
func collectMigrationFiles(fsys fs.FS, dir string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("sqlite: read migrations dir %q: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("sqlite: no migration files found in %q", dir)
	}
	return files, nil
}

// pathVersion returns the canonical version identifier from a migration filename.
//
// "001_init.sql" -> "001_init"
// "002_auth.up.sql" -> "002_auth.up"
func pathVersion(filename string) string {
	base := path.Base(filename)
	return strings.TrimSuffix(base, path.Ext(base))
}

// applyMigration executes body in a transaction and records the version.
func applyMigration(ctx context.Context, db *sql.DB, version, body string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("exec body: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version) VALUES (?)`, version); err != nil {
		return fmt.Errorf("record version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// AppliedVersions returns the sorted list of migration versions currently
// recorded as applied. Useful for `orenda migrate status`.
func AppliedVersions(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: query schema_migrations: %w", err)
	}
	defer rows.Close()

	out := make([]string, 0)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
