package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/config"
	"github.com/ramgml/orenda/internal/storage/postgres"
)

// postgresDSN builds a connection URL from the storage.postgres config
// section. Host and port fall back to the libpq defaults (127.0.0.1:5432);
// the database name is required — guessing one would silently target the
// wrong cluster. User and password are optional (peer/trust auth setups).
func postgresDSN(p config.PostgresConfig) (string, error) {
	if p.Database == "" {
		return "", fmt.Errorf("storage.postgres.database is required when storage.driver=postgres")
	}
	host := p.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := p.Port
	if port == 0 {
		port = 5432
	}
	u := url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
		Path:   p.Database,
	}
	if p.User != "" || p.Password != "" {
		u.User = url.UserPassword(p.User, p.Password)
	}
	return u.String(), nil
}

// runMigratePostgres executes `orenda migrate up|down|status` against the
// postgres driver. It opens the database directly through the postgres
// package (external DSN from config storage.postgres) — the runner is
// wired past the driver-neutral storage seam until the postgres rebind
// lands (T362/T363).
func runMigratePostgres(ctx context.Context, cfg *config.Config, logger *zap.Logger, action migrateAction) error {
	if cfg.Storage.Postgres.Embedded {
		// Embedded postgres management is a separate storage-adapters
		// deliverable (T363); failing fast beats a confusing connect
		// error against an unprovisioned external cluster.
		return fmt.Errorf("storage.postgres.embedded is not implemented yet (external postgres only); configure storage.postgres.host/port/user/password/database")
	}
	dsn, err := postgresDSN(cfg.Storage.Postgres)
	if err != nil {
		return err
	}
	db, err := postgres.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open postgres db: %w", err)
	}
	defer func() { _ = db.Close() }()

	switch action {
	case migrateUp:
		if err := postgres.Migrate(ctx, db, postgres.MigrationsFS, "migrations"); err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
		versions, err := postgres.AppliedVersions(ctx, db)
		if err != nil {
			return err
		}
		logger.Info("migrate up complete", zap.Strings("applied", versions))
		fmt.Println("applied:", versions)
	case migrateDown:
		if err := postgres.MigrateDown(ctx, db, postgres.MigrationsFS, "migrations"); err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
		logger.Info("migrate down complete", zap.String("rolled_back", "last migration"))
	case migrateStatus:
		versions, err := postgres.AppliedVersions(ctx, db)
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
