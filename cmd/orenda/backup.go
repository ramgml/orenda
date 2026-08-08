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
	cmd.AddCommand(&cobra.Command{
		Use:   "restore",
		Short: "Restore the database from a snapshot (Phase 9 wires the actual restore flow)",
		RunE:  runBackupRestore,
	})
	return cmd
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

func runBackupRestore(_ *cobra.Command, _ []string) error {
	// Phase 9 wires the real restore (stop server, swap DB, restart).
	// For now we just print the hint so operators don't lose data.
	fmt.Println("orenda backup restore: not yet implemented (Phase 9)")
	return nil
}
