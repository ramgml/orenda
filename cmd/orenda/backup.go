// Package main — `orenda backup` subcommands (Phase 7.6).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/ramgml/orenda/internal/backup"
	"github.com/ramgml/orenda/internal/config"
	"github.com/ramgml/orenda/internal/storage"
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

// backupService wires a Service from the config + open DB. Reused by the
// push/snapshot/status commands.
func backupService(ctx context.Context, cfgPath string) (*backup.Service, func(), error) {
	cfg, err := loadConfigForCLI(cfgPath)
	if err != nil {
		return nil, nil, err
	}
	db, cleanup, err := openCLIDB(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	svc := backup.New(backupConfigFor(cfg), db)
	return svc, cleanup, nil
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
		Postgres: backup.PostgresConfig{
			DSN:          cfg.Storage.Postgres.DSN,
			Host:         cfg.Storage.Postgres.Host,
			Port:         cfg.Storage.Postgres.Port,
			User:         cfg.Storage.Postgres.User,
			Password:     cfg.Storage.Postgres.Password,
			Database:     cfg.Storage.Postgres.Database,
			SSLMode:      cfg.Storage.Postgres.SSLMode,
			Embedded:     cfg.Storage.Postgres.Embedded,
			EmbeddedPort: cfg.Storage.Postgres.EmbeddedPort,
			DumpBin:      cfg.Storage.Postgres.DumpBin,
		},
	}
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
// Steps (when restoring in place to the live DB path):
//
//  1. Server-running guard (above). Refuse if the live server is up.
//  2. Safety-copy the existing DB to <dest>.pre-restore-<ts>.
//     This is the operator's "oh no" escape hatch — restore is
//     destructive and we never want it without a rollback path.
//  3. Restore overwrites destPath atomically via Restore().
//  4. Migrations: open the restored DB and run every pending
//     migration. A snapshot from a previous schema version should
//     catch up automatically.
//  5. integrity_check + foreign_key_check on the result.
//     Abort the install if either fails — better to ship a broken
//     database than a corrupted one.
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

	// Step 1: safety-copy. Only when restoring in place — for ad-hoc
	// restores to a different path there's nothing meaningful to copy
	// (the dest doesn't exist or is a scratch file).
	if isInPlace {
		if _, statErr := os.Stat(in.To); statErr == nil {
			safetyPath := backup.SafetyCopyPath(in.To, time.Now())
			if err := copyFile(in.To, safetyPath); err != nil {
				return fmt.Errorf("backup restore: safety-copy %s: %w", safetyPath, err)
			}
			fmt.Printf("safety copy: %s\n", safetyPath)
		}
	}

	// Step 2: filesystem restore.
	if err := backup.New(backup.Config{
		SnapshotDir: cfg.Backup.SnapshotDir,
		DBPath:      cfg.ResolveDBPath("."),
	}, nil).Restore(cmd.Context(), in.From, in.To); err != nil {
		return err
	}
	fmt.Printf("restored: %s <- %s\n", in.To, in.From)

	// Step 3: open the restored DB and bring migrations up to current.
	// The restore artifact is always a sqlite file (VACUUM INTO
	// snapshot), so this path pins the sqlite dialect regardless of
	// storage.driver.
	sdb, err := storage.Open(cmd.Context(), storage.Config{
		Driver:        string(storage.DialectSQLite),
		Path:          in.To,
		WALMode:       true,
		EnableForeign: true,
		BusyTimeoutMs: 5000,
	})
	if err != nil {
		return fmt.Errorf("backup restore: open restored db: %w", err)
	}
	db := sdb.DB
	defer func() { _ = db.Close() }()
	if err := storage.Migrate(cmd.Context(), db); err != nil {
		return fmt.Errorf("backup restore: migrate: %w", err)
	}

	// Step 4: integrity + foreign-key checks. Both run as PRAGMA
	// commands; integrity_check returns "ok" when clean, foreign_key_check
	// returns no rows when clean.
	if err := runRestoreCheck(cmd.Context(), db, "integrity_check"); err != nil {
		return err
	}
	if err := runRestoreCheck(cmd.Context(), db, "foreign_key_check"); err != nil {
		return err
	}
	fmt.Println("restore verify: ok (integrity + foreign keys)")
	return nil
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

// runRestoreCheck runs a single-row PRAGMA on db and returns a
// descriptive error if the result is anything other than "ok" / empty.
func runRestoreCheck(ctx context.Context, db *sql.DB, pragma string) error {
	row := db.QueryRowContext(ctx, "PRAGMA "+pragma)
	var s string
	if err := row.Scan(&s); err != nil {
		// foreign_key_check may surface multiple rows; a Scan error
		// here means "no rows" which is the success signal.
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("backup restore: %s: %w", pragma, err)
	}
	if s != "" && s != "ok" {
		return fmt.Errorf("backup restore: %s: %s", pragma, s)
	}
	return nil
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
