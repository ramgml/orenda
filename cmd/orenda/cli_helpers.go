// Package main — CLI helpers shared between subcommands (config + DB open).
package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ramgml/orenda/internal/config"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// loadConfigForCLI loads the config from --config or the default path.
func loadConfigForCLI(cfgPath string) (*config.Config, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}

// openCLIDB opens the SQLite database and applies pending migrations.
// The returned cleanup function closes the handle.
func openCLIDB(ctx context.Context, cfg *config.Config) (*sql.DB, func(), error) {
	dbPath := cfg.ResolveDBPath(".")
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenConfig{
		WALMode:       cfg.Storage.WALMode,
		EnableForeign: cfg.Storage.EnableForeign,
		BusyTimeoutMs: cfg.Storage.BusyTimeoutMs,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("open db: %w", err)
	}
	if err := sqlite.Migrate(ctx, db, sqlite.MigrationsFS, "migrations"); err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("migrate: %w", err)
	}
	return db, func() { _ = db.Close() }, nil
}
