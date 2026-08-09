// Package main — CLI helpers shared between subcommands (config + DB open).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/ramgml/orenda/internal/config"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// inboxProjectID is the well-known id used for the default calendar
// project ("Inbox"). The frontend and the event service both reference
// it as a string; the database just stores it as a regular project
// row. Migrations ensure it exists on first startup.
const inboxProjectID = "00000000-0000-0000-0000-00000000cafe"

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
	db, cleanup, err := openCLIDBWithRaw(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	// Make sure the Inbox project exists on every startup so calendar
	// events created before any user has set up a real project still
	// have a valid FK target. Safe to run repeatedly.
	if err := ensureInboxProject(ctx, db); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("ensure inbox: %w", err)
	}
	return db, cleanup, nil
}

// openCLIDBWithRaw is openCLIDB without the Inbox bootstrap; useful
// for tests and for subcommands that need to migrate but not seed
// (e.g. the migrate command itself).
func openCLIDBWithRaw(ctx context.Context, cfg *config.Config) (*sql.DB, func(), error) {
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

// ensureInboxProject creates the default calendar "Inbox" project if
// it doesn't exist. The id is a well-known constant so the event
// service and the frontend can default to it without a lookup.
//
// We don't tie the Inbox to a specific user (the project is shared,
// the calendar shows everything in it). Migration 012 also creates
// the Inbox during events-folded migration; this function is the
// idempotent safety net for fresh installs where no events existed
// at migration time.
func ensureInboxProject(ctx context.Context, db *sql.DB) error {
	// Probe first to avoid a noisy log on the happy path.
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM projects WHERE id = ?`, inboxProjectID).Scan(&n); err != nil {
		return fmt.Errorf("probe inbox: %w", err)
	}
	if n > 0 {
		return nil
	}
	// Bootstrap a system user to own the project. We never log in as
	// this user (its password_hash is the literal "!" string and we
	// filter it out of the user repo via role='owner'), but the
	// projects.owner_id FK is NOT NULL, so something has to back it.
	if _, err := db.ExecContext(ctx, `
		INSERT OR IGNORE INTO users (id, email, password_hash, display_name, role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		"00000000-0000-0000-0000-000000000001",
		"system-inbox@orenda.local",
		"!",
		"Inbox system",
		"system",
	); err != nil {
		return fmt.Errorf("seed inbox owner: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO projects (id, name, color, owner_id, archived, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, datetime('now'), datetime('now'))`,
		inboxProjectID,
		"Inbox",
		"#6b7280",
		"00000000-0000-0000-0000-000000000001",
	); err != nil {
		return fmt.Errorf("seed inbox project: %w", err)
	}
	slog.Info("created default Inbox project for calendar events")
	return nil
}
