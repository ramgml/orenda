package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpen_AppliesPragmas(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")

	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode:       true,
		EnableForeign: true,
		BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	assertJournalMode(t, db, "wal")
	assertForeignKeys(t, db, 1)
	assertBusyTimeout(t, db, 5000)
}

func TestOpen_NoWAL(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")

	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode:       false,
		EnableForeign: true,
		BusyTimeoutMs: 1000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// journal_mode DELETE is the default for non-WAL connections.
	assertJournalMode(t, db, "delete")
}

func TestMigrate_AppliesOnce(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")

	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))

	// schema_migrations table exists.
	versions, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.NotEmpty(t, versions, "expected at least one applied migration")
	assert.Contains(t, versions, "001_init")

	// Tables from 001_init.sql exist.
	assertTableExists(t, db, "users")
	assertTableExists(t, db, "tasks")
	assertTableExists(t, db, "wiki_pages")
	assertTableExists(t, db, "notifications")

	// Re-running is idempotent.
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))
	versions2, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, versions, versions2)
}

func TestMigrate_002AuthAddsIndexesAndTrigger(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")

	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))

	// 002_auth was applied.
	versions, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.Contains(t, versions, "002_auth")

	// New indexes exist.
	assertIndexExists(t, db, "idx_api_tokens_hash")
	assertIndexExists(t, db, "idx_users_email")

	// updated_at trigger exists.
	assertTriggerExists(t, db, "trg_users_touch")
}

func TestMigrate_003ProjectsTasksAddsIndexesAndTriggers(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")

	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))

	versions, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.Contains(t, versions, "003_projects_tasks")

	assertIndexExists(t, db, "idx_tasks_project_column_position")
	assertIndexExists(t, db, "idx_tasks_assignee_status")
	assertTriggerExists(t, db, "trg_projects_touch")
	assertTriggerExists(t, db, "trg_tasks_touch")
}

func TestMigrate_004AgentsAddsIndexes(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")

	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))

	versions, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.Contains(t, versions, "004_agents")

	assertIndexExists(t, db, "idx_agents_status")
	assertIndexExists(t, db, "idx_agents_last_seen")
	assertIndexExists(t, db, "idx_task_locks_agent")
}

func TestMigrate_005CommentsAttachmentsAddsIndexesAndTriggers(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")

	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))

	versions, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.Contains(t, versions, "005_comments_attachments")

	assertIndexExists(t, db, "idx_comments_author")
	assertIndexExists(t, db, "idx_attachments_sha256")
	assertIndexExists(t, db, "idx_activity_actor")
	assertTriggerExists(t, db, "trg_wiki_pages_touch")
	assertTriggerExists(t, db, "trg_events_touch")
}

func TestMigrate_006CalendarTimeAddsIndexes(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")

	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))

	versions, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.Contains(t, versions, "006_calendar_time")

	assertIndexExists(t, db, "idx_events_range")
	assertIndexExists(t, db, "idx_events_project")
	assertIndexExists(t, db, "idx_time_entries_agent")
	assertIndexExists(t, db, "idx_time_entries_open")
}

func TestMigrate_008WikiAddsFTS5AndIndexes(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")

	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))

	versions, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.Contains(t, versions, "008_wiki")

	// FTS5 virtual tables are stored as regular tables with type='table'.
	assertTableExists(t, db, "pages_fts")
	assertTableExists(t, db, "tasks_fts")
	assertTableExists(t, db, "comments_fts")

	assertIndexExists(t, db, "idx_wiki_links_to")
	assertIndexExists(t, db, "idx_wiki_links_from")
}

func TestBuildDSN(t *testing.T) {
	dsn := buildDSN("/tmp/foo.db", OpenConfig{WALMode: true, BusyTimeoutMs: 5000})
	assert.Contains(t, dsn, "_pragma=busy_timeout(5000)")
	assert.Contains(t, dsn, "_pragma=journal_mode(WAL)")
}

func assertJournalMode(t *testing.T, db *sql.DB, want string) {
	t.Helper()
	var got string
	require.NoError(t, db.QueryRow(`PRAGMA journal_mode`).Scan(&got))
	assert.Equal(t, want, strings.ToLower(got))
}

func assertForeignKeys(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	require.NoError(t, db.QueryRow(`PRAGMA foreign_keys`).Scan(&got))
	assert.Equal(t, want, got)
}

func assertBusyTimeout(t *testing.T, db *sql.DB, wantMs int) {
	t.Helper()
	var got int
	require.NoError(t, db.QueryRow(`PRAGMA busy_timeout`).Scan(&got))
	assert.Equal(t, wantMs, got)
}

func assertTableExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "table %s should exist", name)
}

func assertIndexExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "index %s should exist", name)
}

func assertTriggerExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, name).Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "trigger %s should exist", name)
}
