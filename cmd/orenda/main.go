// Package main is the Orenda CLI entry point.
//
// Available subcommands:
//
//	orenda serve [--config FILE]      start HTTP server
//	orenda version                    print version and exit
//	orenda migrate up                 apply all pending migrations
//	orenda migrate down [--steps N]   rollback the last N migrations (Phase 1+)
//	orenda migrate status             list applied migrations
//	orenda backup push|status|snapshot    backup operations (stubs in Phase 0)
//
// Global flags:
//
//	--config FILE, -c FILE            path to config.yaml (default data/config.yaml)
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/config"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// version is set by -ldflags at build time.
var version = "0.1.0-dev"

// commit is set by -ldflags at build time (git describe --always).
var commit = "dev"

// buildDate is set by -ldflags at build time.
var buildDate = "unknown"

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// newRootCmd constructs the top-level cobra command tree.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "orenda",
		Short:         "Orenda — local-first productivity suite with first-class AI agents",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.PersistentFlags().StringP("config", "c", "data/config.yaml", "path to config file")

	root.AddCommand(newServeCmd())
	root.AddCommand(newVersionCmd())
	root.AddCommand(newMigrateCmd())
	root.AddCommand(newBackupCmd())

	return root
}

// ----------------------------------------------------------------------------
// serve
// ----------------------------------------------------------------------------

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the Orenda HTTP server",
		RunE:  runServe,
	}
}

func runServe(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	logger, err := buildLogger(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = logger.Sync() }()

	absCfg, _ := filepath.Abs(cfgPath)
	logger.Info("starting orenda",
		zap.String("version", version),
		zap.String("commit", commit),
		zap.String("config", absCfg),
		zap.String("addr", fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)),
	)

	// Open SQLite database.
	dbPath := cfg.ResolveDBPath(cwdOr(absCfg, "."))
	db, err := sqlite.Open(cmd.Context(), dbPath, sqlite.OpenConfig{
		WALMode:       cfg.Storage.WALMode,
		EnableForeign: cfg.Storage.EnableForeign,
		BusyTimeoutMs: cfg.Storage.BusyTimeoutMs,
	})
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer func() { _ = db.Close() }()

	logger.Info("sqlite opened", zap.String("path", dbPath))

	// Ensure migrations are up to date before serving traffic.
	if err := sqlite.Migrate(cmd.Context(), db, sqlite.MigrationsFS, "migrations"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	logger.Info("migrations applied")

	// Build repositories.
	users := sqlite.NewUserRepository(db)
	projects := sqlite.NewProjectRepository(db)
	tasks := sqlite.NewTaskRepository(db)
	tokens := sqlite.NewAPITokenRepository(db)

	// Build the JWT signer. JWT secret is mandatory for Phase 1+ — refuse
	// to start without it so the operator doesn't discover the missing
	// config at first login.
	if cfg.Auth.JWTSecret == "" {
		return fmt.Errorf("auth: ORENDA_AUTH__JWT_SECRET (or auth.jwt_secret in config) is required for `serve`")
	}
	signer := auth.NewSigner(cfg.Auth.JWTSecret, cfg.Auth.JWTTTL, "orenda")

	// Build the router.
	api.Version = version
	router := api.NewRouter(api.Dependencies{
		Logger:     logger,
		Signer:     signer,
		Users:      users,
		Projects:   projects,
		Tasks:      tasks,
		Tokens:     tokens,
		CookieName: cfg.Auth.CookieName,
	})

	// HTTP server with graceful shutdown.
	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("http listening", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("shutdown complete")
	return nil
}

// cwdOr returns the current working directory, falling back to fallback if
// os.Getwd() fails. It is used as the base for resolving relative paths from
// the config (db_path, mirror_dir, etc).
//
// absCfg is currently unused; the parameter is kept so future versions can
// resolve paths relative to the config file when needed (e.g. if the
// operator installs Orenda in /opt but runs from /home/me).
func cwdOr(_ string, fallback string) string {
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		return fallback
	}
	return wd
}

// ----------------------------------------------------------------------------
// version
// ----------------------------------------------------------------------------

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf("orenda %s (commit %s, built %s)\n", version, commit, buildDate)
		},
	}
}

// ----------------------------------------------------------------------------
// migrate
// ----------------------------------------------------------------------------

func newMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Database migrations",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "up",
		Short: "Apply all pending migrations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMigrate(cmd, migrateUp)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "down",
		Short: "Rollback the last migration (Phase 1+)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMigrate(cmd, migrateDown)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "List applied migrations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMigrate(cmd, migrateStatus)
		},
	})
	return cmd
}

type migrateAction int

const (
	migrateUp migrateAction = iota
	migrateDown
	migrateStatus
)

func runMigrate(cmd *cobra.Command, action migrateAction) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	logger, err := buildLogger(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = logger.Sync() }()

	dbPath := cfg.ResolveDBPath(cwdOr(cfgPath, "."))
	db, err := sqlite.Open(cmd.Context(), dbPath, sqlite.OpenConfig{
		WALMode:       cfg.Storage.WALMode,
		EnableForeign: cfg.Storage.EnableForeign,
		BusyTimeoutMs: cfg.Storage.BusyTimeoutMs,
	})
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer func() { _ = db.Close() }()

	switch action {
	case migrateUp:
		if err := sqlite.Migrate(cmd.Context(), db, sqlite.MigrationsFS, "migrations"); err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
		versions, _ := sqlite.AppliedVersions(cmd.Context(), db)
		logger.Info("migrate up complete", zap.Strings("applied", versions))
		fmt.Println("applied:", versions)
	case migrateDown:
		// Phase 1+ will track down-migrations explicitly.
		// For now we just record that the operator wanted a rollback.
		logger.Warn("migrate down is not yet implemented (Phase 1+); no changes made")
		fmt.Println("migrate down: not implemented (Phase 1+)")
	case migrateStatus:
		// Need a migrations table to query status; create it lazily.
		if _, err := db.ExecContext(cmd.Context(), `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`); err != nil {
			return err
		}
		versions, err := sqlite.AppliedVersions(cmd.Context(), db)
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

// ----------------------------------------------------------------------------
// backup
// ----------------------------------------------------------------------------

func newBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Backup operations (Phase 7; Phase 0 is a no-op stub)",
	}

	for _, sub := range []struct {
		use, short, help string
	}{
		{"push", "Commit and push mirror to git remote", "Not implemented in Phase 0; will land in Phase 7."},
		{"snapshot", "Create SQLite snapshot", "Not implemented in Phase 0; will land in Phase 7."},
		{"status", "Show backup status", "Not implemented in Phase 0; will land in Phase 7."},
	} {
		s := sub // capture
		cmd.AddCommand(&cobra.Command{
			Use:   s.use,
			Short: s.short,
			Long:  s.help,
			Run: func(_ *cobra.Command, _ []string) {
				fmt.Printf("orenda backup %s: not implemented (Phase 7)\n", s.use)
			},
		})
	}
	return cmd
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

// buildLogger constructs a zap.Logger from the config section.
//
// Phase 0 logs only to stderr — file rotation (cfg.Logging.Path) lands with
// the structured-logging refactor in Phase 9.
func buildLogger(cfg *config.Config) (*zap.Logger, error) {
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(cfg.Logging.Level)); err != nil {
		level = zapcore.InfoLevel
	}

	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "ts"
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	var encoder zapcore.Encoder
	if cfg.Logging.Format == "console" {
		encoder = zapcore.NewConsoleEncoder(encCfg)
	} else {
		encoder = zapcore.NewJSONEncoder(encCfg)
	}

	core := zapcore.NewCore(encoder, zapcore.AddSync(os.Stderr), level)
	return zap.New(core, zap.AddCaller()), nil
}

// suppress unused-import warning on systems without time package usage.
var _ = time.Second
