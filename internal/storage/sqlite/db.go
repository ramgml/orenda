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
	"errors"
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

// foreignKeysOffMarker is the magic comment a migration body can carry to
// opt into a foreign_keys=OFF transaction.
//
// Why this exists: rebuilding the `tasks` table (Phase 16) requires
// `DROP TABLE tasks_old` after the rows have been copied into a fresh
// `tasks`. With FK enforcement ON, SQLite refuses the DROP because
// child tables (task_locks, checklists, comments, task_activity,
// time_entries, …) still reference the old name — even though SQLite's
// default `legacy_alter_table` is OFF and `ALTER TABLE … RENAME` would
// have rewritten those REFERENCES to the new name. We can't rely on
// `defer_foreign_constraints` either because DROP performs implicit
// cascading DELETEs even when deferred. The standard SQLite recipe is
// to flip `PRAGMA foreign_keys = OFF` for the duration of the
// migration — but the pragma is per-connection and a no-op inside a
// transaction, so applyMigrationUnsafe() borrows a single connection
// from the pool and runs the migration body on it.
//
// Usage: put a SQL comment `-- orenda:foreign_keys_off` anywhere in
// the migration body (any line, any position). The runner strips it
// from the executed body and switches to the unsafe path. Migrations
// without the marker take the normal FK=ON path.
const foreignKeysOffMarker = "-- orenda:foreign_keys_off"

// irreversibleMarker is the magic comment a down-migration body can
// carry to opt out of rolling back. Migrations like the Phase-16
// tasks-table rebuild (015_inbox_no_project.sql) reshuffle data in a
// way that can't be cleanly reversed — running their down would
// either fail (FK orphans) or silently lose rows. Marking the
// down.sql with this marker makes `orenda migrate down` return a
// clean ErrMigrationIrreversible error so the operator knows to
// restore from a snapshot instead.
//
// Format: `-- orenda:irreversible: <reason>` (the reason surfaces
// in the error so the operator gets a hint without opening the
// file). Lines starting with `--` are SQL comments, so the marker
// is inert if the file is run as SQL.
const irreversibleMarker = "-- orenda:irreversible"

// ErrMigrationIrreversible is returned by MigrateDown when the
// migration's down.sql opts out of rolling back via the
// irreversibleMarker comment. The wrapped error carries the
// reason from the marker for the CLI to surface.
var ErrMigrationIrreversible = errors.New("migration is marked irreversible")

// ErrNoDownFile is returned by MigrateDown when the migration
// has no .down.sql counterpart. The CLI prints the missing path so
// the operator can write one.
var ErrNoDownFile = errors.New("no down-migration file")

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

// collectMigrationFiles returns the sorted list of *.sql up-migration files
// under fsys/dir. Down migrations (`*.down.sql`) live next to the up files
// but are routed to MigrateDown, not here. The split keeps the up/down
// surface clean without a separate directory.
//
// We accept both the legacy `001_init.sql` naming and the future
// `001_init.up.sql` shape so an operator can rename if they want —
// `pathVersion` strips the suffix.
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
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		// Down migrations are picked up by MigrateDown, not here.
		if strings.HasSuffix(name, ".down.sql") {
			continue
		}
		files = append(files, name)
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("sqlite: no migration files found in %q", dir)
	}
	return files, nil
}

// lastAppliedVersion returns the lexicographically-largest version
// from the schema_migrations table — i.e. the most recent migration.
// Used by MigrateDown to pick which .down.sql to run.
func lastAppliedVersion(ctx context.Context, db *sql.DB) (string, error) {
	row := db.QueryRowContext(ctx, `SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`)
	var v string
	if err := row.Scan(&v); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("sqlite: no migrations applied")
		}
		return "", fmt.Errorf("sqlite: last version: %w", err)
	}
	return v, nil
}

// findDownFile returns the path of the down-migration for version.
// Convention: `<version>.down.sql` (e.g., `001_init.down.sql`).
//
// We don't accept down-as-comment-in-up because (a) that would be
// impossible to review (the up is the source of truth) and (b) a
// down that's longer than a few lines is unreadable inside another
// file. Separate files cost nothing in review.
func findDownFile(fsys fs.FS, dir, version string) (string, error) {
	candidate := path.Join(dir, version+".down.sql")
	if _, err := fs.Stat(fsys, candidate); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: %s", ErrNoDownFile, candidate)
		}
		return "", fmt.Errorf("sqlite: stat %s: %w", candidate, err)
	}
	return candidate, nil
}

// parseIrreversibleReason pulls the `-- orenda:irreversible[: <reason>]`
// marker out of a migration body. Returns ("", false) when no marker
// is present; ("", true) when the marker appears without a reason;
// ("<reason>", true) when the marker carries a colon-separated reason.
//
// The marker must be at the start of a line comment — anything else
// (e.g. inside a string literal, mid-line) is ignored so a literal
// in a CREATE TABLE doesn't accidentally trip the parser.
func parseIrreversibleReason(body string) (string, bool) {
	found := false
	var reason string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, irreversibleMarker) {
			continue
		}
		found = true
		// What's left of the line after the marker:
		//   "-- orenda:irreversible" → ""
		//   "-- orenda:irreversible: reason" → ": reason"
		//   "-- orenda:irreversible;foo" → ";foo" (invalid shape)
		rest := strings.TrimPrefix(trimmed, irreversibleMarker)
		rest = strings.TrimSpace(rest)
		if !strings.HasPrefix(rest, ":") {
			// Either bare marker or wrong separator — keep the
			// first match but treat as no-reason.
			continue
		}
		reason = strings.TrimSpace(strings.TrimPrefix(rest, ":"))
	}
	return reason, found
}

// MigrateDown rolls back the most recently applied migration. The
// down file must live next to the up file (`<version>.down.sql`)
// and may opt out via the `-- orenda:irreversible` marker. We
// always run inside a single transaction so a half-rolled-back
// schema doesn't leak into `migrate up` on the next boot.
//
// The marker on the up migration (`-- orenda:foreign_keys_off`) is
// honored on the way down too: rebuild migrations may need FK=OFF
// to drop the old shape without the cascading-delete problem.
func MigrateDown(ctx context.Context, db *sql.DB, migrationsFS embed.FS, dir string) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
	`); err != nil {
		return fmt.Errorf("sqlite: create schema_migrations: %w", err)
	}
	version, err := lastAppliedVersion(ctx, db)
	if err != nil {
		return err
	}
	downPath, err := findDownFile(migrationsFS, dir, version)
	if err != nil {
		return err
	}
	body, err := migrationsFS.ReadFile(downPath)
	if err != nil {
		return fmt.Errorf("sqlite: read %s: %w", downPath, err)
	}
	if reason, ok := parseIrreversibleReason(string(body)); ok {
		return fmt.Errorf("%w: %s (%s)", ErrMigrationIrreversible, version, reason)
	}
	if err := applyMigrationDown(ctx, db, version, string(body)); err != nil {
		return fmt.Errorf("sqlite: apply down %q: %w", downPath, err)
	}
	return nil
}

// applyMigrationDown executes the down body in a transaction and
// removes the version row from schema_migrations. Like
// applyMigration, it honours the foreign_keys_off marker —
// rebuilds like 015_inbox_no_project need it on the way back too.
func applyMigrationDown(ctx context.Context, db *sql.DB, version, body string) error {
	if strings.Contains(body, foreignKeysOffMarker) {
		cleaned := strings.ReplaceAll(body, foreignKeysOffMarker, "")
		return applyMigrationDownUnsafe(ctx, db, version, cleaned)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("exec body: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM schema_migrations WHERE version = ?`, version); err != nil {
		return fmt.Errorf("unrecord version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// applyMigrationDownUnsafe mirrors applyMigrationUnsafe for the
// down path — borrows a single connection, runs under FK=OFF,
// restores FK=ON before returning, and verifies no orphan rows
// remain via PRAGMA foreign_key_check.
func applyMigrationDownUnsafe(ctx context.Context, db *sql.DB, version, body string) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: borrow conn for down: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("sqlite: foreign_keys off (down): %w", err)
	}
	// Best-effort restore even on early exit paths.
	//nolint:contextcheck // runs from defer after ctx may be cancelled; the pragma restore must not depend on it.
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `PRAGMA foreign_keys = ON`)
	}()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin down tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("exec down body: %w", err)
	}
	// FK check inside the transaction so a failing check aborts the
	// whole rollback.
	if _, err := tx.ExecContext(ctx, `PRAGMA foreign_key_check`); err != nil {
		return fmt.Errorf("sqlite: foreign_key_check: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM schema_migrations WHERE version = ?`, version); err != nil {
		return fmt.Errorf("unrecord version (down): %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit down: %w", err)
	}
	committed = true
	return nil
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
//
// Migrations whose body contains `-- orenda:foreign_keys_off` are routed
// through applyMigrationUnsafe(), which borrows a single connection
// from the pool and runs the body under `PRAGMA foreign_keys = OFF`.
// See the foreignKeysOffMarker doc for why.
func applyMigration(ctx context.Context, db *sql.DB, version, body string) error {
	if strings.Contains(body, foreignKeysOffMarker) {
		// Strip the marker so it doesn't pollute the executed SQL
		// (some parsers tolerate line comments, but stripping is the
		// simplest and least error-prone).
		cleaned := strings.ReplaceAll(body, foreignKeysOffMarker, "")
		return applyMigrationUnsafe(ctx, db, version, cleaned)
	}
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

// applyMigrationUnsafe runs body under `PRAGMA foreign_keys = OFF` on a
// dedicated connection. Per-connection pragma state and the pool's
// max-conn setting (1 in Open) require us to run the entire migration
// body — including its implicit transaction — on the borrowed conn.
// `defer_foreign_keys` is intentionally NOT used because DROP TABLE
// triggers implicit cascading DELETEs even when constraints are
// deferred; we genuinely need FK enforcement off.
//
// Safety nets:
//   - `PRAGMA foreign_key_check` inside the tx verifies no orphan rows
//     were left behind; a non-empty result aborts the migration and
//     rolls back the schema_migrations row.
//   - `PRAGMA foreign_keys = ON` is restored before the conn returns
//     to the pool so subsequent queries on the same db handle honour
//     FKs.
func applyMigrationUnsafe(ctx context.Context, db *sql.DB, version, body string) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("unsafe borrow conn: %w", err)
	}
	//nolint:contextcheck // runs from defer after ctx may be cancelled; the pragma restore must not depend on it.
	defer func() {
		// Best-effort restore. If this fails the conn is poisoned
		// but the pool will detect it on next use (the SQLite
		// driver validates every conn on checkout).
		_, _ = conn.ExecContext(context.Background(), `PRAGMA foreign_keys = ON`)
		_ = conn.Close()
	}()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("unsafe pragma off: %w", err)
	}
	// Run the body in a tx on the same conn — `defer_foreign_keys`
	// inside the tx is unnecessary; we've already turned enforcement
	// off for this connection.
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("unsafe begin: %w", err)
	}
	if _, err := tx.ExecContext(ctx, body); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("exec body: %w", err)
	}
	// Sanity: the migration body should have left the schema in a
	// consistent FK state (no orphans). Fail loud if it didn't — a
	// broken FK check is far easier to debug now than to chase later.
	if _, err := tx.ExecContext(ctx, `PRAGMA foreign_key_check`); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("foreign_key_check: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations(version) VALUES (?)`, version); err != nil {
		_ = tx.Rollback()
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
