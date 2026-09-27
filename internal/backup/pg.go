// Package backup — postgres snapshot/restore strategy (T366,
// wiki:storage-adapters D8).
//
// The sqlite dialect snapshots via VACUUM INTO (backup.go, unchanged);
// the postgres dialect shells out to pg_dump --format=custom into the
// SAME SnapshotDir under the SAME orenda-YYYYMMDD-HHMMSS naming with
// the same mtime-based rotation. Restore/verify creates a scratch
// database on the target server, pg_restore --no-owner's the dump into
// it, checks readability (pg_restore --list) plus the applied
// migrations, and drops the scratch — the live database is never
// touched under a running server (promotion is an operator action:
// `orenda backup restore --to <database>`).
//
// Binary sourcing: pg_dump/pg_restore are client tools and are NOT part
// of the stock embedded-postgres bundle (fergusstrange ships server
// binaries only — initdb/pg_ctl/postgres). The lookup therefore tries
// the operator override (storage.postgres.dump_bin) first, then any
// extracted runtime directories under ~/.embedded-postgres-go (a future
// bundle or an in-flight cluster extraction would be the exact version
// match for the running server), then the system PATH — the normal hit
// in practice. When nothing resolves, the error says so explicitly
// instead of promising an embedded dump.
package backup

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ramgml/orenda/internal/storage/postgres"
)

// PostgresConfig carries the pg_dump/pg_restore connection target —
// the same dsn/parts shape the storage seam resolves (DSN wins over
// the individual parts, wiki:storage-adapters D4). Embedded selects
// the bootstrap cluster parameters the embedded runtime dials.
type PostgresConfig struct {
	DSN      string
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string

	Embedded     bool
	EmbeddedPort int

	// DumpBin overrides the pg_dump lookup: a bare name is resolved
	// via PATH, a path is used as-is (pg_restore is then looked up
	// next to it). Empty falls back to the embedded-cache + PATH
	// chain described in the package comment above.
	DumpBin string
}

// pgTarget resolves the libpq connection string pg_dump/pg_restore
// dial. It reuses the storage adapter's resolver — no second DSN
// convention.
func (s *Service) pgTarget() (string, error) {
	p := s.getCfg().Postgres
	if p.Embedded {
		opts, err := postgres.BuildEmbeddedOptions(p.Database, p.EmbeddedPort, p.User, p.Password)
		if err != nil {
			return "", fmt.Errorf("backup: resolve embedded postgres target: %w", err)
		}
		dsn, err := postgres.ResolveDSN(opts.Connection())
		if err != nil {
			return "", fmt.Errorf("backup: resolve embedded postgres target: %w", err)
		}
		return dsn, nil
	}
	dsn, err := postgres.ResolveDSN(postgres.RuntimeConfig{
		DSN:      p.DSN,
		Host:     p.Host,
		Port:     p.Port,
		User:     p.User,
		Password: p.Password,
		Database: p.Database,
		SSLMode:  p.SSLMode,
	})
	if err != nil {
		return "", fmt.Errorf("backup: resolve postgres target: %w", err)
	}
	return dsn, nil
}

// ResolvePGTool locates the pg client binary named tool ("pg_dump").
//
// Order: override (bare name → PATH; path → must exist as a regular
// file), then extracted runtime dirs under ~/.embedded-postgres-go
// (newest first — the stock bundle is server-only, so this usually
// finds nothing), then PATH.
func ResolvePGTool(tool, override string) (string, error) {
	if override != "" {
		if !strings.ContainsRune(override, os.PathSeparator) {
			p, err := exec.LookPath(override)
			if err != nil {
				return "", fmt.Errorf("backup: %s override %q not found in PATH: %w", tool, override, err)
			}
			return p, nil
		}
		info, err := os.Stat(override)
		if err != nil {
			return "", fmt.Errorf("backup: %s override %q: %w", tool, override, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("backup: %s override %q is a directory", tool, override)
		}
		return override, nil
	}
	if p := embeddedPGTool(tool); p != "" {
		return p, nil
	}
	if p, err := exec.LookPath(tool); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("backup: %s not found: the embedded postgres bundle ships server binaries only — install postgresql-client so %s is on PATH, or set storage.postgres.dump_bin", tool, tool)
}

// resolvePGRestoreTool locates pg_restore, preferring the directory of
// the already-resolved pg_dump (version-consistent client install).
func resolvePGRestoreTool(dumpBin string) (string, error) {
	if dumpBin != "" {
		sibling := filepath.Join(filepath.Dir(dumpBin), "pg_restore")
		if info, err := os.Stat(sibling); err == nil && !info.IsDir() {
			return sibling, nil
		}
	}
	return ResolvePGTool("pg_restore", "")
}

// embeddedPGTool returns the newest extracted pg client binary under
// ~/.embedded-postgres-go, or "" when none is present.
func embeddedPGTool(tool string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	candidates := []string{
		filepath.Join(home, ".embedded-postgres-go", "*", "bin", tool),
		filepath.Join(home, ".embedded-postgres-go", "*", tool),
	}
	var hits []string
	for _, pattern := range candidates {
		matches, _ := filepath.Glob(pattern)
		hits = append(hits, matches...)
	}
	sort.Slice(hits, func(i, j int) bool {
		ni, nj := fileModTime(hits[i]), fileModTime(hits[j])
		return ni.After(nj)
	})
	for _, h := range hits {
		if info, err := os.Stat(h); err == nil && !info.IsDir() {
			return h
		}
	}
	return ""
}

func fileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// snapshotPostgres creates a pg_dump --format=custom snapshot at
// SnapshotDir/orenda-YYYYMMDD-HHMMSS.dump and rotates old snapshots —
// the exact naming/rotation convention the sqlite VACUUM INTO path
// uses (wiki:storage-adapters D8).
func (s *Service) snapshotPostgres(ctx context.Context) (string, error) {
	cfg := s.getCfg()
	dumpBin, err := ResolvePGTool("pg_dump", cfg.Postgres.DumpBin)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(cfg.SnapshotDir, 0o755); err != nil {
		return "", fmt.Errorf("backup snapshot: mkdir: %w", err)
	}
	dsn, err := s.pgTarget()
	if err != nil {
		return "", fmt.Errorf("backup snapshot: %w", err)
	}

	ts := time.Now().UTC().Format("20060102-150405")
	dst := filepath.Join(cfg.SnapshotDir, "orenda-"+ts+".dump")
	// Same collision suffixing as the sqlite snapshot: a second
	// snapshot within the same second must not overwrite the first.
	for i := 1; ; i++ {
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			break
		}
		dst = filepath.Join(cfg.SnapshotDir,
			fmt.Sprintf("orenda-%s-%02d.dump", ts, i))
	}

	// pg_dump --format=custom is the compressed, restore-selectable
	// archive format; pg_restore --list can read it without a server.
	cmd := exec.CommandContext(ctx, dumpBin, "--format=custom", "--file", dst, "--dbname", dsn)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(dst) // never leave a partial archive behind
		return "", fmt.Errorf("backup snapshot: pg_dump: %v: %s", err, string(out))
	}

	rotateSnapshots(cfg.SnapshotDir, cfg.SnapshotRotationDays)
	return dst, nil
}

// RestorePostgresResult summarizes one RestorePostgres run.
type RestorePostgresResult struct {
	// Database is the scratch (dropped) or kept database name.
	Database string
	// Kept reports whether the database survived the run (the CLI's
	// --to <database> promotion path) or was dropped after verify.
	Kept bool
	// TOCEntries is the number of objects pg_restore --list reported.
	TOCEntries int
}

// RestorePostgres verifies a custom-format dump by restoring it into a
// scratch database on the configured target server:
//
//  1. pg_restore --no-owner --dbname=<scratch> <dump>;
//  2. readability check: pg_restore --list yields a non-empty TOC;
//  3. basic check: the restored database has applied migrations
//     (schema_migrations non-empty) — anything else is not an orenda
//     dump and the operator should know immediately;
//  4. teardown: the scratch is dropped (WITH (FORCE) so lingering
//     connections can't wedge the cleanup), unless keepDatabase names
//     a persistent target for the operator to promote.
//
// A kept database must not exist beforehand — restore refuses to
// overwrite. The server must be up: pg_dump/pg_restore own their
// connections, so the backup Service needs no live *sql.DB here.
//
//nolint:contextcheck // RestorePostgres intentionally tears down on a detached context: a canceled/expired request ctx is exactly the case the scratch drop must survive (leak-free teardown beats ctx inheritance here).
func (s *Service) RestorePostgres(ctx context.Context, dumpPath, keepDatabase string) (RestorePostgresResult, error) {
	if dumpPath == "" {
		return RestorePostgresResult{}, ErrInvalidInput
	}
	if _, err := os.Stat(dumpPath); err != nil {
		if os.IsNotExist(err) {
			return RestorePostgresResult{}, ErrNotFound
		}
		return RestorePostgresResult{}, fmt.Errorf("backup restore: stat dump: %w", err)
	}
	cfg := s.getCfg()
	dumpBin, err := ResolvePGTool("pg_dump", cfg.Postgres.DumpBin)
	if err != nil {
		return RestorePostgresResult{}, err
	}
	restoreBin, err := resolvePGRestoreTool(dumpBin)
	if err != nil {
		return RestorePostgresResult{}, err
	}
	target, err := s.pgTarget()
	if err != nil {
		return RestorePostgresResult{}, fmt.Errorf("backup restore: %w", err)
	}

	scratch := keepDatabase
	kept := scratch != ""
	if !kept {
		// Nanosecond granularity: same-second concurrent restores must
		// not collide (prepare's DROP ... WITH (FORCE) would kill the
		// first restore's in-flight pg_restore).
		scratch = fmt.Sprintf("orenda_restore_%d", time.Now().UnixNano())
	}

	// Admin handle on the target server. postgres.Open is the simple-
	// protocol opener (CREATE/DROP DATABASE must not go through
	// prepared statements) — the same dial flavour the migration
	// runner uses.
	admin, err := postgres.Open(ctx, target)
	if err != nil {
		return RestorePostgresResult{}, fmt.Errorf("backup restore: connect target: %w", err)
	}
	defer func() { _ = admin.Close() }()

	if err := prepareScratchDatabase(ctx, admin, scratch, kept); err != nil {
		return RestorePostgresResult{}, err
	}

	// Teardown runs detached: a canceled/expired request ctx must not
	// leak the scratch — the drop IS the cleanup for that very case.
	//nolint:contextcheck // intentional detach: the request ctx may be the reason we're tearing down.
	teardownCtx, teardownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer teardownCancel()

	// Anything failing from here on must drop the scratch before
	// returning — the operator should never have to clean up after a
	// failed verify.
	fail := func(err error) (RestorePostgresResult, error) {
		if !kept {
			_, _ = admin.ExecContext(teardownCtx, `DROP DATABASE IF EXISTS `+quoteIdent(scratch)+` WITH (FORCE)`)
		}
		return RestorePostgresResult{}, err
	}

	scratchDSN := swapDatabaseInDSN(target, scratch)
	restore := exec.CommandContext(ctx, restoreBin, "--no-owner", "--dbname", scratchDSN, dumpPath)
	if out, err := restore.CombinedOutput(); err != nil {
		return fail(fmt.Errorf("backup restore: pg_restore: %v: %s", err, string(out)))
	}

	entries, err := verifyRestoredScratch(ctx, restoreBin, dumpPath, scratchDSN)
	if err != nil {
		return fail(err)
	}

	if !kept {
		// Same detached teardown context — the drop must run even when
		// the caller's ctx just expired (that's a verify failure too).
		if _, err := admin.ExecContext(teardownCtx, `DROP DATABASE `+quoteIdent(scratch)+` WITH (FORCE)`); err != nil {
			return fail(fmt.Errorf("backup restore: drop scratch: %w", err))
		}
	}
	return RestorePostgresResult{Database: scratch, Kept: kept, TOCEntries: entries}, nil
}

// prepareScratchDatabase creates the scratch database on the target
// server: a kept database must not exist (no silent overwrites of the
// operator's promotion target), a scratch starts from a forced drop of
// any stale leftovers.
func prepareScratchDatabase(ctx context.Context, admin *sql.DB, scratch string, kept bool) error {
	if kept {
		var exists bool
		if err := admin.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, scratch,
		).Scan(&exists); err != nil {
			return fmt.Errorf("backup restore: check database %q: %w", scratch, err)
		}
		if exists {
			return fmt.Errorf("backup restore: database %q already exists — restore would overwrite it; drop it first or pick another name", scratch)
		}
	} else if _, err := admin.ExecContext(ctx,
		`DROP DATABASE IF EXISTS `+quoteIdent(scratch)+` WITH (FORCE)`); err != nil {
		return fmt.Errorf("backup restore: drop stale scratch: %w", err)
	}
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE `+quoteIdent(scratch)); err != nil {
		return fmt.Errorf("backup restore: create scratch database: %w", err)
	}
	return nil
}

// verifyRestoredScratch runs the post-restore checks: pg_restore --list
// proves the archive is readable without a server, and the restored
// database must carry applied migrations — anything else is not an
// orenda dump and the operator should know immediately. Returns the
// number of TOC entries.
func verifyRestoredScratch(ctx context.Context, restoreBin, dumpPath, scratchDSN string) (int, error) {
	toc, err := pgRestoreList(ctx, restoreBin, dumpPath)
	if err != nil {
		return 0, err
	}
	if toc.entries == 0 {
		return 0, fmt.Errorf("backup restore: pg_restore --list reported an empty table of contents — %s is not a readable dump", dumpPath)
	}
	scratchDB, err := postgres.Open(ctx, scratchDSN)
	if err != nil {
		return 0, fmt.Errorf("backup restore: open scratch: %w", err)
	}
	defer func() { _ = scratchDB.Close() }()
	var applied int
	if err := scratchDB.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil {
		return 0, fmt.Errorf("backup restore: schema_migrations check: %w — not an orenda dump?", err)
	}
	if applied == 0 {
		return 0, fmt.Errorf("backup restore: restored database has no applied migrations — not an orenda dump?")
	}
	return toc.entries, nil
}

// pgRestoreList runs `pg_restore --list` — the readability probe that
// parses the archive without touching any server.
func pgRestoreList(ctx context.Context, restoreBin, dumpPath string) (toc, error) {
	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, restoreBin, "--list", dumpPath)
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		// stderr carries the actual parser complaint ("did not find
		// magic string in file header", …) — surface it, an empty
		// "exit status 1:" reason helps nobody.
		return toc{}, fmt.Errorf("backup restore: pg_restore --list: %w: %s", err, strings.TrimSpace(errOut.String()))
	}
	return parseTOC(out.String()), nil
}

// toc is the parsed `pg_restore --list` output. Object lines look like
// `215; 1259 16456 TABLE projects postgres`; comment lines start with
// `;`.
type toc struct {
	entries int
}

func parseTOC(list string) toc {
	t := toc{}
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		t.entries++
	}
	return t
}

// swapDatabaseInDSN returns the target connection string pointed at
// another database on the same server. URI form is re-encoded;
// keyword/value form gets its dbname pair replaced (appended when
// missing), with libpq single-quote escaping for tricky values.
func swapDatabaseInDSN(target, database string) string {
	if u, err := url.Parse(target); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
		u.Path = "/" + database
		return u.String()
	}
	pairs := splitKeywordPairs(target)
	for i, p := range pairs {
		if strings.HasPrefix(strings.ToLower(p), "dbname=") {
			pairs[i] = "dbname=" + quoteConnstringValue(database)
			return strings.Join(pairs, " ")
		}
	}
	pairs = append(pairs, "dbname="+quoteConnstringValue(database))
	return strings.Join(pairs, " ")
}

// splitKeywordPairs splits a libpq keyword/value connstring on
// whitespace, honoring single-quoted values.
func splitKeywordPairs(s string) []string {
	var (
		pairs []string
		cur   strings.Builder
		inQ   bool
	)
	for _, r := range s {
		switch {
		case r == '\'' && !inQ:
			inQ = true
			cur.WriteRune(r)
		case r == '\'' && inQ:
			inQ = false
			cur.WriteRune(r)
		case r == ' ' && !inQ:
			if cur.Len() > 0 {
				pairs = append(pairs, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		pairs = append(pairs, cur.String())
	}
	return pairs
}

// quoteConnstringValue applies libpq connstring quoting: values with
// spaces or quotes are single-quoted with backslash escapes.
func quoteConnstringValue(v string) string {
	if !strings.ContainsAny(v, " '\\") {
		return v
	}
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`, ` `, `\ `)
	return `'` + r.Replace(v) + `'`
}

// quoteIdent double-quotes a SQL identifier (database names here are
// generated or operator-supplied — quoting is mandatory for the
// latter).
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// checkpoint is the dialect hook behind the scheduler's WAL job.
//
// sqlite: PRAGMA wal_checkpoint(TRUNCATE) bounds WAL growth — see the
// scheduler comment for the (non-archival) contract.
//
// postgres: no-op. PostgreSQL checkpoints itself (checkpointer
// background process), and pg_dump --format=custom reads a consistent
// snapshot without a manual checkpoint. skipped=true tells the caller
// not to record a backup_log row for work that never ran — a fake
// "success" row would poison the log's meaning for both dialects.
func (s *Service) checkpoint(ctx context.Context) (skipped bool, err error) {
	if s.Dialect() == DialectPostgres {
		return true, nil
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return false, fmt.Errorf("backup checkpoint: %w", err)
	}
	return false, nil
}
