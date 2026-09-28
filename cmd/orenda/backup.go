// Package main — `orenda backup` subcommands (Phase 7.6).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ramgml/orenda/internal/backup"
	"github.com/ramgml/orenda/internal/config"
	"github.com/ramgml/orenda/internal/storage"
	"github.com/ramgml/orenda/internal/storage/postgres"
)

func newBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Backup operations (git push, sqlite snapshot, status, restore)",
	}

	pushCmd := &cobra.Command{
		Use:   "push",
		Short: "Commit and push the mirror directory to the configured remote",
		Long: "Without flags: commit any pending changes in the mirror and push.\n" +
			"  --with-snapshots also runs a fresh SQLite snapshot, copies it\n" +
			"  into the mirror's snapshots/ directory, writes manifest.json\n" +
			"  (sha256 + size), and commits both. Phase 32.5 pilot task #1.",
		RunE: runBackupPush,
	}
	// Registered on the subcommand: a parent-level flag never reached
	// runBackupPush (cobra doesn't inherit plain Flags), so
	// `backup push --with-snapshots` failed flag parsing outright.
	pushCmd.Flags().Bool("with-snapshots", false, "also push a fresh sqlite snapshot to the mirror repo")
	cmd.AddCommand(pushCmd)
	cmd.AddCommand(&cobra.Command{
		Use:   "snapshot",
		Short: "Create a database snapshot (sqlite: VACUUM INTO, postgres: pg_dump -Fc)",
		RunE:  runBackupSnapshot,
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show recent backup log entries and snapshot list",
		RunE:  runBackupStatus,
	})
	cmd.AddCommand(newBackupRestoreCmd())
	return cmd
}

func newBackupRestoreCmd() *cobra.Command {
	var (
		from string
		to   string
		yes  bool
	)
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore/verify the database from a snapshot",
		Long: "sqlite: Replace the live database with a snapshot produced by\n" +
			"`orenda backup snapshot`. The server MUST be stopped first (it holds the\n" +
			"live file open). Use --to to restore into a separate path for verification.\n" +
			"postgres: The dump (pg_dump -Fc) is restored into a scratch database and\n" +
			"verified (pg_restore --list + applied-migration check); the server must be\n" +
			"UP. Without --to the scratch is dropped after verify. With --to <database>\n" +
			"the restore lands in that kept database — point the server at it\n" +
			"(storage.postgres.database) to promote it. The server must be running for\n" +
			"the embedded runtime (it owns the cluster).",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runBackupRestoreWithVerify(cmd, restoreInput{From: from, To: to, Yes: yes})
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "path to the snapshot .db file (required)")
	cmd.Flags().StringVar(&to, "to", "", "destination path (defaults to the live db_path)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

type restoreInput struct {
	From string
	To   string
	Yes  bool
}

// backupConfigFor maps the app config into the backup Service config.
// Dialect + Postgres come from storage.* (T366): the snapshot strategy
// follows the configured driver, and on postgres the pg_dump/pg_restore
// connection target is the configured dsn/parts (or the bootstrap
// cluster when storage.postgres.embedded — the adapter resolves it).
func backupConfigFor(cfg *config.Config) backup.Config {
	return backup.Config{
		MirrorDir:            cfg.Backup.MirrorDir,
		SnapshotDir:          cfg.Backup.SnapshotDir,
		DBPath:               cfg.ResolveDBPath("."),
		RemoteURL:            cfg.Backup.RemoteURL,
		RemoteAuth:           cfg.Backup.RemoteAuth,
		SnapshotRotationDays: cfg.Backup.SnapshotRotationDays,
		SnapshotCron:         cfg.Backup.SQLiteSnapshotCron,
		Dialect:              backup.Dialect(cfg.Storage.Driver),
		Postgres:             backupPostgresConfigFor(cfg.Storage.Postgres),
	}
}

// backupPostgresConfigFor maps the config's postgres section 1:1 into
// the backup Service's pg target.
func backupPostgresConfigFor(p config.PostgresConfig) backup.PostgresConfig {
	return backup.PostgresConfig{
		DSN:          p.DSN,
		Host:         p.Host,
		Port:         p.Port,
		User:         p.User,
		Password:     p.Password,
		Database:     p.Database,
		SSLMode:      p.SSLMode,
		Embedded:     p.Embedded,
		EmbeddedPort: p.EmbeddedPort,
		DumpBin:      p.DumpBin,
	}
}

// openBackupDB dials the configured database for the backup commands
// WITHOUT owning the embedded cluster lifecycle (T366). The sqlite
// dialect opens the file directly, as always. On postgres the backup
// commands are operator actions against a running install — the server
// (and, in embedded mode, the cluster it owns) must be up, so the CLI
// attaches to the bootstrap cluster instead of trying to start a second
// postmaster on the same port/PGDATA.
func openBackupDB(ctx context.Context, cfg *config.Config) (*sql.DB, func(), error) {
	scfg := storageConfigFor(cfg, cfg.ResolveDBPath("."))
	if cfg.Storage.Driver == "postgres" && cfg.Storage.Postgres.Embedded {
		opts, err := postgres.BuildEmbeddedOptions(
			cfg.Storage.Postgres.Database,
			cfg.Storage.Postgres.EmbeddedPort,
			cfg.Storage.Postgres.User,
			cfg.Storage.Postgres.Password,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("backup: resolve embedded cluster: %w", err)
		}
		// DSN must lose: the whole point of this branch is dialing the
		// bootstrap cluster the running server owns — a configured dsn
		// (storage seam priority) would send the CLI to a different
		// server than the one pg_dump/pg_restore (pgTarget) hit.
		scfg.Postgres.DSN = ""
		conn := opts.Connection()
		scfg.Postgres.Host = conn.Host
		scfg.Postgres.Port = conn.Port
		scfg.Postgres.User = conn.User
		scfg.Postgres.Password = conn.Password
		scfg.Postgres.SSLMode = conn.SSLMode
		scfg.Postgres.Embedded = false
	}
	sdb, err := storage.Open(ctx, scfg)
	if err != nil {
		if cfg.Storage.Driver == "postgres" && cfg.Storage.Postgres.Embedded {
			return nil, nil, fmt.Errorf("backup: attach to the embedded cluster (is the server running? only the running server owns it): %w", err)
		}
		return nil, nil, err
	}
	return sdb.DB, func() { _ = sdb.Close() }, nil
}

// backupService wires a Service from the config + a backup-scoped DB
// handle. Reused by the push/snapshot/status commands. On postgres the
// handle attaches to the running cluster (openBackupDB — the server
// must be up); the pg_dump/pg_restore tools used by Snapshot/Restore
// bring their own connections regardless of it.
func backupService(ctx context.Context, cfgPath string) (*backup.Service, func(), error) {
	cfg, err := loadConfigForCLI(cfgPath)
	if err != nil {
		return nil, nil, err
	}
	var (
		db  *sql.DB
		cup func()
	)
	if cfg.Storage.Driver != "postgres" {
		db, cup, err = openCLIDB(ctx, cfg)
	} else {
		db, cup, err = openBackupDB(ctx, cfg)
	}
	if err != nil {
		return nil, nil, err
	}
	svc := backup.New(backupConfigFor(cfg), db)
	return svc, cup, nil
}

func runBackupPush(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	withSnapshots, _ := cmd.Flags().GetBool("with-snapshots")
	if _, err := loadConfigForCLI(cfgPath); err != nil {
		return err
	}
	svc, cleanup, err := backupService(cmd.Context(), cfgPath)
	if err != nil {
		return err
	}
	defer cleanup()

	if withSnapshots {
		if err := svc.PushWithSnapshot(cmd.Context()); err != nil {
			return err
		}
		fmt.Println("backup pushed with snapshot")
		return nil
	}

	if err := svc.CommitAndPush(cmd.Context(), "manual backup"); err != nil {
		return err
	}
	fmt.Println("backup pushed")
	return nil
}

func runBackupSnapshot(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	svc, cleanup, err := backupService(cmd.Context(), cfgPath)
	if err != nil {
		return err
	}
	defer cleanup()

	// T366: dialect-aware — sqlite runs VACUUM INTO, postgres runs
	// pg_dump --format=custom. A missing pg_dump surfaces here with
	// the install hint (the protective behavior the old reject-gate
	// provided, now where it's actually true).
	path, err := svc.Snapshot(cmd.Context())
	if err != nil {
		return err
	}
	fmt.Printf("snapshot written: %s\n", path)
	return nil
}

func runBackupStatus(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	svc, cleanup, err := backupService(cmd.Context(), cfgPath)
	if err != nil {
		return err
	}
	defer cleanup()

	logs, err := svc.ListLog(cmd.Context(), 10)
	if err != nil {
		return err
	}
	fmt.Println("recent backup log:")
	for _, l := range logs {
		fmt.Printf("  %s %s %s %s\n", l.CreatedAt.Format("2006-01-02 15:04:05"), l.Type, l.Status, l.Message)
	}
	snaps, err := svc.ListSnapshots(cmd.Context())
	if err != nil {
		return err
	}
	fmt.Printf("snapshots (%d):\n", len(snaps))
	for _, s := range snaps {
		fmt.Printf("  %s  %8d bytes  %s\n", s.ModTime.Format("2006-01-02 15:04:05"), s.Size, s.Path)
	}
	return nil
}

// Phase 22: enhanced restore pipeline.
//
// Steps (sqlite; the same staging order applies to an ad-hoc --to
// restore):
//
//  1. Server-running guard (above). Refuse if the live server is up.
//  2. Restore the snapshot into a staging copy NEXT TO the destination
//     (<dest>.restore-staging-<ts>, same filesystem). The destination
//     — the live database file — is not touched yet.
//  3. Migrations + integrity_check + foreign_key_check run on the
//     staging copy (T374: verify-before-swap). A snapshot from a
//     previous schema version catches up automatically. On any failure
//     the staging copy is removed and the problem list (T369) is
//     returned as-is: the previous database file stays byte-for-byte
//     intact and no artifact is left behind.
//  4. Only after verify ok is the destination replaced: safety-copy
//     the live DB to <dest>.pre-restore-<ts> (in place — the
//     operator's "oh no" escape hatch for the swap itself), then
//     atomically rename the staging copy onto the destination and drop
//     its stale -wal/-shm sidecars so sqlite starts a clean journal.
func runBackupRestoreWithVerify(cmd *cobra.Command, in restoreInput) error {
	if in.From == "" {
		return fmt.Errorf("backup restore: --from <snapshot.db> is required")
	}
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfigForCLI(cfgPath)
	if err != nil {
		return err
	}
	// T366: the postgres dialect never swaps files in place — it
	// restores into a scratch/kept database on the live server.
	if cfg.Storage.Driver == "postgres" {
		return runBackupRestorePostgres(cmd, cfg, in)
	}
	if in.To == "" {
		in.To = cfg.ResolveDBPath(".")
	}
	isInPlace := in.To == cfg.ResolveDBPath(".")
	if isInPlace && backup.IsServerRunning(cmd.Context(), cfg.Server.Host, cfg.Server.Port) {
		return fmt.Errorf("backup restore: server is running on %s:%d — stop the server first (e.g. `Ctrl+C` or `systemctl --user stop orenda`)",
			cfg.Server.Host, cfg.Server.Port)
	}
	if !in.Yes {
		fmt.Printf("About to restore:\n  from: %s\n  to:   %s\n", in.From, in.To)
		if isInPlace {
			fmt.Println("This OVERWRITES the live database. Pass --yes to confirm.")
		} else {
			fmt.Println("Pass --yes to proceed.")
		}
		return nil
	}

	// Step 1: restore into the staging copy, not onto the destination.
	staging := restoreStagingPath(in.To, time.Now())
	if err := backup.New(backup.Config{
		SnapshotDir: cfg.Backup.SnapshotDir,
		DBPath:      cfg.ResolveDBPath("."),
	}, nil).Restore(cmd.Context(), in.From, staging); err != nil {
		return err
	}

	// Step 2: migrations + integrity/foreign-key checks on the staging
	// copy. The restore artifact is always a sqlite file (VACUUM INTO
	// snapshot), so this path pins the sqlite dialect regardless of
	// storage.driver.
	if err := verifyStagedRestore(cmd.Context(), staging); err != nil {
		// T374 contract: a failed verify never touches the destination.
		// Drop the staging artifact; the T369 problem list passes
		// through untouched.
		removeRestoreStaging(staging)
		return fmt.Errorf("%w\nrestore stopped: staging copy removed, previous database left untouched", err)
	}
	fmt.Println("restore verify: ok (integrity + foreign keys)")

	// Step 3: the destructive moment — now that the staged copy is
	// verified, keep the operator's rollback path and swap it in.
	// Safety-copy only when restoring in place: for ad-hoc restores to
	// a different path there's nothing meaningful to copy (the dest
	// doesn't exist or is a scratch file).
	if isInPlace {
		if _, statErr := os.Stat(in.To); statErr == nil {
			safetyPath := backup.SafetyCopyPath(in.To, time.Now())
			if err := copyFile(in.To, safetyPath); err != nil {
				removeRestoreStaging(staging)
				return fmt.Errorf("backup restore: safety-copy %s: %w", safetyPath, err)
			}
			fmt.Printf("safety copy: %s\n", safetyPath)
		}
	}
	if err := os.Rename(staging, in.To); err != nil {
		removeRestoreStaging(staging)
		return fmt.Errorf("backup restore: swap in restored database: %w", err)
	}
	// Drop stale -wal/-shm sidecars of the destination so sqlite starts
	// a clean journal with the restored file — the same guarantee
	// Service.Restore gave when it wrote the destination itself.
	for _, side := range []string{in.To + "-wal", in.To + "-shm"} {
		if rmErr := os.Remove(side); rmErr != nil && !os.IsNotExist(rmErr) {
			return fmt.Errorf("backup restore: remove sidecar %s: %w", side, rmErr)
		}
	}
	fmt.Printf("restored: %s <- %s\n", in.To, in.From)
	return nil
}

// restoreStagingPath returns the staging path a snapshot is restored
// into for verification: next to the destination, on the same
// filesystem, so the final promotion is an atomic rename (T374).
// Timestamped like SafetyCopyPath — concurrent restores don't clobber
// each other's staging copies.
func restoreStagingPath(destPath string, t time.Time) string {
	if destPath == "" {
		return ""
	}
	return fmt.Sprintf("%s.restore-staging-%d", destPath, t.Unix())
}

// removeRestoreStaging deletes the staging copy plus any sqlite
// sidecars verification may have left next to it. Best-effort: the
// contract that matters (the previous database was never touched)
// holds regardless of cleanup errors.
func removeRestoreStaging(path string) {
	for _, side := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Remove(side)
	}
}

// verifyStagedRestore opens the staged copy, brings migrations up to
// current and runs integrity_check + foreign_key_check on it. Any
// problem is returned as the operator-facing error (T369 layout);
// closing the handle and removing the staging copy are the caller's
// job.
func verifyStagedRestore(ctx context.Context, path string) error {
	sdb, err := storage.Open(ctx, storage.Config{
		Driver:        string(storage.DialectSQLite),
		Path:          path,
		WALMode:       true,
		EnableForeign: true,
		BusyTimeoutMs: 5000,
	})
	if err != nil {
		return fmt.Errorf("backup restore: open restored db: %w", err)
	}
	db := sdb.DB
	defer func() { _ = db.Close() }()
	if err := storage.Migrate(ctx, db); err != nil {
		return fmt.Errorf("backup restore: migrate: %w", err)
	}
	if err := runRestoreCheck(ctx, db, "integrity_check"); err != nil {
		return err
	}
	return runRestoreCheck(ctx, db, "foreign_key_check")
}

// runBackupRestorePostgres is the postgres leg of `backup restore`
// (T366): the dump is restored into a scratch database on the live
// server and verified (pg_restore --list + applied-migration check);
// the live database is never touched under a running server. Without
// --to the scratch is dropped after verify. With --to <database> the
// restore lands in a kept database the operator can promote by
// pointing the server at it — that's also the smoke path for "the
// server rises on the restored data".
//
// T374: this leg is verify-before-promote by construction and needs no
// sqlite-style reordering — the dump lands in a scratch/kept database
// (RestorePostgres), verifyRestoredScratch checks it THERE, the
// promote instruction is only printed after "restore verify: ok", and
// a failed verify drops the scratch, so the operator is never left
// with a half-installed database and the configured app database is
// never written to.
//
// Unlike the sqlite leg there is no server-running guard: the
// constraint is inverted (the server must be UP — pg_restore needs a
// server, and in embedded mode only the running server owns the
// cluster). No DB handle is opened here: pg_dump/pg_restore bring
// their own connections, so a CLI restore works even when the config's
// app database is mid-migration.
func runBackupRestorePostgres(cmd *cobra.Command, cfg *config.Config, in restoreInput) error {
	if !in.Yes {
		fmt.Printf("About to restore:\n  from: %s\n", in.From)
		if in.To == "" {
			fmt.Println("The dump is restored into a scratch database and dropped after verify.")
		} else {
			fmt.Printf("  to:   database %q (kept — promote by pointing storage.postgres.database at it)\n", in.To)
		}
		fmt.Println("Pass --yes to proceed.")
		return nil
	}

	// No open app DB: New(cfg, nil) is the documented postgres-only
	// wiring (the tools connect themselves).
	svc := backup.New(backupConfigFor(cfg), nil)
	res, err := svc.RestorePostgres(cmd.Context(), in.From, in.To)
	if err != nil {
		return err
	}

	if res.Kept {
		fmt.Printf("restored: database %q <- %s\n", res.Database, in.From)
		fmt.Printf("restore verify: ok (pg_restore --list: %d toc entries; schema_migrations: applied)\n", res.TOCEntries)
		fmt.Printf("promote: set storage.postgres.database=%s (or ORENDA_STORAGE__POSTGRES__DATABASE=%s) and start the server\n",
			res.Database, res.Database)
	} else {
		fmt.Printf("restored into scratch database %q and dropped after verify\n", res.Database)
		fmt.Printf("restore verify: ok (pg_restore --list: %d toc entries; schema_migrations: applied)\n", res.TOCEntries)
		fmt.Println("keep the restored database with: --to <database>")
	}
	return nil
}

// runRestoreCheck runs a problem-reporting PRAGMA (integrity_check,
// foreign_key_check) on db, iterating EVERY result row, and returns a
// descriptive error listing the violations if there are any.
//
// Both pragmas are multi-row, just with different shapes:
//
//   - integrity_check yields one TEXT column per row — "ok" when the
//     database is intact, one problem line per row otherwise;
//   - foreign_key_check yields four columns (table, rowid, parent,
//     fkid) per violating row and NO rows when clean.
//
// The previous QueryRow+Scan implementation only ever looked at the
// first cell of the first row: on a real foreign_key_check violation
// it failed with "sql: expected 4 destination arguments in Scan, not
// 1" instead of naming the offending rows, and a multiline
// integrity_check report was truncated to its first line. Iterating
// the rows makes the report complete and Scan-shape errors impossible.
func runRestoreCheck(ctx context.Context, db *sql.DB, pragma string) error {
	problems, err := pragmaProblems(ctx, db, pragma)
	if err != nil {
		return fmt.Errorf("backup restore: %s: %w", pragma, err)
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("backup restore: %s: %d problem(s):\n%s",
		pragma, len(problems), strings.Join(problems, "\n"))
}

// pragmaProblems runs pragma and returns one human-readable line per
// reported problem (empty slice = clean). foreign_key_check rows are
// rendered in the sqlite3 CLI layout (table/rowid/parent/fkid); any
// other pragma is expected to report one text cell per row, of which
// only "ok" means clean.
func pragmaProblems(ctx context.Context, db *sql.DB, pragma string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA "+pragma)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var problems []string
	for rows.Next() {
		cells, err := scanNullStrings(rows)
		if err != nil {
			return nil, err
		}
		if pragma == "foreign_key_check" && len(cells) == 4 {
			problems = append(problems, fmt.Sprintf("table=%s rowid=%s parent=%s fkid=%s",
				nullString(cells[0]), nullString(cells[1]), nullString(cells[2]), nullString(cells[3])))
			continue
		}
		parts := make([]string, len(cells))
		for i, c := range cells {
			parts[i] = nullString(c)
		}
		if line := strings.Join(parts, " "); line != "ok" {
			problems = append(problems, line)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}
	return problems, nil
}

// scanNullStrings scans the current row into one sql.NullString per
// column. The cell count is taken from the result set, so pragmas with
// any arity scan without "expected N destination arguments" errors.
func scanNullStrings(rows *sql.Rows) ([]sql.NullString, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("columns: %w", err)
	}
	cells := make([]sql.NullString, len(cols))
	dest := make([]any, len(cols))
	for i := range cells {
		dest[i] = &cells[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	return cells, nil
}

// nullString renders a scanned cell, preserving NULL as text instead
// of silently turning it into "" (foreign_key_check reports NULL rowids
// for WITHOUT ROWID child tables).
func nullString(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return "NULL"
}

// copyFile duplicates src to dst (creates dst if needed, truncates if
// existing). Plain io.Copy + fsync; not atomic — used only for the
// pre-restore safety copy, where atomicity is irrelevant.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }() // read-side close: copy error, if any, already reported
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
