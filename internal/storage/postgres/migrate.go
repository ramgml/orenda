package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Migrate applies every embedded migration that hasn't been recorded in
// the schema_migrations table yet. Migration files are sorted by name
// (NNN_*.up.sql) and executed in order, one transaction per file.
// PostgreSQL's transactional DDL makes each file all-or-nothing — a
// failed migration leaves no half-applied schema behind.
//
// migrationsFS is expected to contain *.sql files under dir (typically
// "migrations"); the FS indirection allows embed patterns like
// //go:embed all:migrations/*.sql.
func Migrate(ctx context.Context, db *sql.DB, migrationsFS fs.FS, dir string) error {
	if err := ensureSchemaTable(ctx, db); err != nil {
		return err
	}
	applied, err := loadApplied(ctx, db)
	if err != nil {
		return err
	}
	files, err := collectUpFiles(migrationsFS, dir)
	if err != nil {
		return err
	}
	for _, name := range files {
		version := pathVersion(name)
		if _, ok := applied[version]; ok {
			continue
		}
		fullPath := path.Join(dir, name)
		body, err := fs.ReadFile(migrationsFS, fullPath)
		if err != nil {
			return fmt.Errorf("postgres: read migration %q: %w", fullPath, err)
		}
		if err := applyMigration(ctx, db, version, string(body)); err != nil {
			return fmt.Errorf("postgres: apply migration %q: %w", fullPath, err)
		}
	}
	return nil
}

// MigrateDown rolls back the most recently applied migration via its
// `<version>.down.sql` companion. The rollback runs in a single
// transaction together with the schema_migrations bookkeeping delete, so
// a half-rolled-back schema can never leak into the next `migrate up`.
//
// Unlike the sqlite runner there is no irreversible-marker escape hatch:
// the baseline down is a plain drop, and the sqlite chain's data-preserving
// rebuilds have no postgres counterpart to protect.
func MigrateDown(ctx context.Context, db *sql.DB, migrationsFS fs.FS, dir string) error {
	if err := ensureSchemaTable(ctx, db); err != nil {
		return err
	}
	version, err := lastAppliedVersion(ctx, db)
	if err != nil {
		return err
	}
	downPath := path.Join(dir, version+".down.sql")
	body, err := fs.ReadFile(migrationsFS, downPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("postgres: no down-migration file: %s", downPath)
		}
		return fmt.Errorf("postgres: read %s: %w", downPath, err)
	}
	if err := applyMigrationDown(ctx, db, version, string(body)); err != nil {
		return fmt.Errorf("postgres: apply down %q: %w", downPath, err)
	}
	return nil
}

// AppliedVersions returns the sorted list of migration versions currently
// recorded as applied, bootstrapping schema_migrations when missing so
// `orenda migrate status` works on a fresh database.
func AppliedVersions(ctx context.Context, db *sql.DB) ([]string, error) {
	if err := ensureSchemaTable(ctx, db); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("postgres: query schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]string, 0)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("postgres: scan schema_migrations: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterate schema_migrations: %w", err)
	}
	return out, nil
}

// ensureSchemaTable creates the schema_migrations bookkeeping table when
// missing. applied_at is timestamptz (native PG type) rather than the
// mirrored TEXT timestamps: it is runner bookkeeping, never read by the
// application.
func ensureSchemaTable(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
	`); err != nil {
		return fmt.Errorf("postgres: create schema_migrations: %w", err)
	}
	return nil
}

// loadApplied returns the set of already-applied migration versions.
func loadApplied(ctx context.Context, db *sql.DB) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("postgres: query schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]struct{})
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("postgres: scan schema_migrations: %w", err)
		}
		out[v] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterate schema_migrations: %w", err)
	}
	return out, nil
}

// collectUpFiles returns the sorted list of up-migration files under
// fsys/dir. Both the `NNN_name.up.sql` and the legacy `NNN_name.sql`
// naming are accepted; `*.down.sql` files are routed to MigrateDown.
func collectUpFiles(fsys fs.FS, dir string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("postgres: read migrations dir %q: %w", dir, err)
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".down.sql") {
			continue
		}
		files = append(files, name)
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("postgres: no migration files found in %q", dir)
	}
	return files, nil
}

// pathVersion returns the version identifier from a migration filename:
// "001_baseline.up.sql" → "001_baseline", legacy "001_init.sql" →
// "001_init".
func pathVersion(filename string) string {
	base := path.Base(filename)
	base = strings.TrimSuffix(base, ".up.sql")
	base = strings.TrimSuffix(base, ".down.sql")
	return strings.TrimSuffix(base, ".sql")
}

// lastAppliedVersion returns the lexicographically-largest applied
// version — the migration MigrateDown must roll back.
func lastAppliedVersion(ctx context.Context, db *sql.DB) (string, error) {
	row := db.QueryRowContext(ctx, `SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`)
	var v string
	if err := row.Scan(&v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("postgres: no migrations applied")
		}
		return "", fmt.Errorf("postgres: last version: %w", err)
	}
	return v, nil
}

// applyMigration executes body in a transaction and records the version.
// The version row commits atomically with the DDL — PostgreSQL rolls
// both back together on failure.
func applyMigration(ctx context.Context, db *sql.DB, version, body string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("exec body: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version) VALUES ($1)`, version); err != nil {
		return fmt.Errorf("record version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// applyMigrationDown executes the down body in a transaction and removes
// the version row, mirroring applyMigration.
func applyMigrationDown(ctx context.Context, db *sql.DB, version, body string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres: begin down: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("exec down body: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM schema_migrations WHERE version = $1`, version); err != nil {
		return fmt.Errorf("unrecord version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit down: %w", err)
	}
	return nil
}
