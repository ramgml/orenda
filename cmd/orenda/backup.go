// Package main — `orenda backup` subcommands (Phase 7.6).
package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ramgml/orenda/internal/backup"
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
			return runBackupRestore(cmd, restoreInput{From: from, To: to, Yes: yes})
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
