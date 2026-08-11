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
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

func newBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Backup operations (git push, sqlite snapshot, status, restore)",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "push",
		Short: "Commit and push the mirror directory to the configured remote",
		RunE:  runBackupPush,
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "snapshot",
		Short: "Create a SQLite snapshot of the database",
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
		Short: "Restore the database from a snapshot file",
		Long: "Replace the live database with a snapshot produced by `orenda backup snapshot`.\n" +
			"The server MUST be stopped first (it holds the live file open). Use --to to\n" +
			"restore into a separate path for verification.",
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
	svc := backup.New(backup.Config{
		MirrorDir:            cfg.Backup.MirrorDir,
		SnapshotDir:          cfg.Backup.SnapshotDir,
		DBPath:               cfg.ResolveDBPath("."),
		RemoteURL:            cfg.Backup.RemoteURL,
		RemoteAuth:           cfg.Backup.RemoteAuth,
		SnapshotRotationDays: cfg.Backup.SnapshotRotationDays,
	}, db)
	return svc, cleanup, nil
}

func runBackupPush(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	svc, cleanup, err := backupService(cmd.Context(), cfgPath)
	if err != nil {
		return err
	}
	defer cleanup()

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

func runBackupRestore(cmd *cobra.Command, in restoreInput) error {
	if in.From == "" {
		return fmt.Errorf("backup restore: --from <snapshot.db> is required")
	}

	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfigForCLI(cfgPath)
	if err != nil {
		return err
	}

	if in.To == "" {
		in.To = cfg.ResolveDBPath(".")
	}

	// Refuse if the server is listening on the configured port: replacing
	// the sqlite file while another process has it open corrupts WAL/SHM.
	// When --to is something other than the live DB path the user is
	// restoring to a copy, so the live server is irrelevant.
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
		return nil // dry-run when --yes is missing
	}

	// Restore is filesystem-only; no need to open the DB.
	if err := backup.New(backup.Config{
		SnapshotDir: cfg.Backup.SnapshotDir,
		DBPath:      cfg.ResolveDBPath("."),
	}, nil).Restore(cmd.Context(), in.From, in.To); err != nil {
		return err
	}
	fmt.Printf("restored: %s <- %s\n", in.To, in.From)
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
	db, err := sqlite.Open(cmd.Context(), in.To, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	if err != nil {
		return fmt.Errorf("backup restore: open restored db: %w", err)
	}
	defer func() { _ = db.Close() }()
	if err := sqlite.Migrate(cmd.Context(), db, sqlite.MigrationsFS, "migrations"); err != nil {
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
	defer in.Close()
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
