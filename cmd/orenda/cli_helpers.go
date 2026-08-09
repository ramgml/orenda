// Package main — CLI helpers shared between subcommands (config + DB open).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

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
//
// We also seed a board and the default columns so the Inbox is a
// fully navigable project — without that, GET /projects/{id}/board
// returns 404 and the frontend can't render the kanban for the
// default landing project.
func ensureInboxProject(ctx context.Context, db *sql.DB) error {
	// Probe first to avoid a noisy log on the happy path.
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM projects WHERE id = ?`, inboxProjectID).Scan(&n); err != nil {
		return fmt.Errorf("probe inbox: %w", err)
	}
	if n > 0 {
		// Project row already exists (created by migration 012 or a
		// previous bootstrap). Still verify the board + default
		// columns are present, since older bootstrap calls did not
		// seed them.
		return ensureInboxBoardAndColumns(ctx, db)
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
	if err := ensureInboxBoardAndColumns(ctx, db); err != nil {
		return err
	}
	slog.Info("created default Inbox project for calendar events")
	return nil
}

// ensureInboxBoardAndColumns makes sure the Inbox project has a board
// and the default five columns (backlog/todo/in_progress/review/done).
// Older seeds only inserted the project row; the frontend then
// returned 404 when the user navigated to the Inbox project page.
func ensureInboxBoardAndColumns(ctx context.Context, db *sql.DB) error {
	var hasBoard int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM boards WHERE project_id = ?`, inboxProjectID).Scan(&hasBoard); err != nil {
		return fmt.Errorf("probe inbox board: %w", err)
	}
	if hasBoard > 0 {
		return nil
	}
	boardID := newInboxID()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO boards (id, project_id, name, position, created_at)
		VALUES (?, ?, 'Main', 0, datetime('now'))`,
		boardID, inboxProjectID); err != nil {
		return fmt.Errorf("seed inbox board: %w", err)
	}
	defaultCols := []string{"backlog", "todo", "in_progress", "review", "done"}
	for i, name := range defaultCols {
		colID := newInboxID()
		position := float64(i) * 1024
		if _, err := db.ExecContext(ctx, `
			INSERT INTO columns (id, board_id, name, position) VALUES (?, ?, ?, ?)`,
			colID, boardID, name, position); err != nil {
			return fmt.Errorf("seed inbox column %q: %w", name, err)
		}
	}
	return nil
}

// newInboxID returns a unique id for board/column rows seeded by the
// Inbox bootstrap. Combines unix-nanos with a process counter so
// successive calls during the same bootstrap produce distinct ids.
func newInboxID() string {
	var ctr uint64
	ctr++
	now := time.Now().UnixNano()
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		now&0xffffffff,
		(now>>32)&0xffff,
		((now>>48)&0x0fff)|0x4000, // RFC 4122 version 4 marker
		((now>>52)&0x3fff)|0x8000, // RFC 4122 variant marker
		ctr,
	)
}
