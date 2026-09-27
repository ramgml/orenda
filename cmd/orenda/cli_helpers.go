// Package main — CLI helpers shared between subcommands (config + DB open).
//
// Phase 16 dropped the legacy "system Inbox" bootstrap (ensureInboxProject
// + ensureInboxBoardAndColumns + the inboxProjectID constant). The Inbox
// is no longer a project — it's tasks with project_id IS NULL — so
// there's nothing to seed on first startup.
package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ramgml/orenda/internal/config"
	"github.com/ramgml/orenda/internal/storage"
)

// loadConfigForCLI loads the config from --config or the default path.
func loadConfigForCLI(cfgPath string) (*config.Config, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}

// storageConfigFor builds the seam open config from the app config and
// a resolved database path. Driver comes from storage.driver (default
// sqlite); the postgres driver fails fast inside storage.Open.
func storageConfigFor(cfg *config.Config, dbPath string) storage.Config {
	return storage.Config{
		Driver:        cfg.Storage.Driver,
		Path:          dbPath,
		WALMode:       cfg.Storage.WALMode,
		EnableForeign: cfg.Storage.EnableForeign,
		BusyTimeoutMs: cfg.Storage.BusyTimeoutMs,
	}
}

// openCLIDB opens the database and applies pending migrations.
// The returned cleanup function closes the handle. No system-Inbox
// bootstrap — see the package doc.
func openCLIDB(ctx context.Context, cfg *config.Config) (*sql.DB, func(), error) {
	return openCLIDBWithRaw(ctx, cfg)
}

// openCLIDBRaw opens the database WITHOUT running any
// migrations — the caller sees the schema exactly as it is on disk
// (T154). Needed by `migrate down` / `migrate status`: the hidden
// Migrate(UP) inside the migrating open path re-applied whatever a
// previous `down` had just rolled back, so repeated `down` calls
// never moved the head. The migration runner (MigrateDown) and the
// status command bootstrap schema_migrations themselves when it is
// missing.
func openCLIDBRaw(ctx context.Context, cfg *config.Config) (*sql.DB, func(), error) {
	dbPath := cfg.ResolveDBPath(".")
	sdb, err := storage.Open(ctx, storageConfigFor(cfg, dbPath))
	if err != nil {
		return nil, nil, fmt.Errorf("open db: %w", err)
	}
	return sdb.DB, func() { _ = sdb.Close() }, nil
}

// openCLIDBWithRaw opens the DB and runs migrations without any
// additional seeding. Useful for subcommands that need a migrated
// DB but not the bootstrap side-effects (e.g. `migrate status`).
func openCLIDBWithRaw(ctx context.Context, cfg *config.Config) (*sql.DB, func(), error) {
	dbPath := cfg.ResolveDBPath(".")
	sdb, err := storage.Open(ctx, storageConfigFor(cfg, dbPath))
	if err != nil {
		return nil, nil, fmt.Errorf("open db: %w", err)
	}
	if err := storage.Migrate(ctx, sdb.DB); err != nil {
		_ = sdb.Close()
		return nil, nil, fmt.Errorf("migrate: %w", err)
	}
	return sdb.DB, func() { _ = sdb.Close() }, nil
}
