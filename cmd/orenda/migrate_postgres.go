package main

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/config"
	"github.com/ramgml/orenda/internal/storage"
)

// runMigratePostgres executes `orenda migrate up|down|status` against the
// postgres driver, fully through the driver-neutral seam: storage.Open
// dials via the pgx stdlib connector and the dialect shim, and the
// dialect-aware migrate helpers apply the postgres baseline (T361) —
// the runner's multi-statement files go through a short-lived
// simple-protocol handle opened by the seam itself.
//
// With storage.postgres.embedded=true the lifecycle wrapper owns a
// local cluster for the duration of the command.
func runMigratePostgres(ctx context.Context, cfg *config.Config, logger *zap.Logger, action migrateAction) error {
	stopEmbedded, err := startEmbeddedIfConfiguredCLI(cfg)
	if err != nil {
		return err
	}
	defer stopEmbedded()

	sdb, err := storage.Open(ctx, storageConfigFor(cfg, cfg.ResolveDBPath(".")))
	if err != nil {
		return fmt.Errorf("open postgres db: %w", err)
	}
	defer func() { _ = sdb.Close() }()

	switch action {
	case migrateUp:
		if err := sdb.Migrate(ctx); err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
		versions, err := sdb.AppliedVersions(ctx)
		if err != nil {
			return err
		}
		logger.Info("migrate up complete", zap.Strings("applied", versions))
		fmt.Println("applied:", versions)
	case migrateDown:
		if err := sdb.MigrateDown(ctx); err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
		logger.Info("migrate down complete", zap.String("rolled_back", "last migration"))
	case migrateStatus:
		versions, err := sdb.AppliedVersions(ctx)
		if err != nil {
			return err
		}
		if len(versions) == 0 {
			fmt.Println("no migrations applied")
		} else {
			fmt.Println("applied migrations:")
			for _, v := range versions {
				fmt.Println(" -", v)
			}
		}
	}
	return nil
}
