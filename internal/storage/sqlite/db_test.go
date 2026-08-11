package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
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
	// trg_events_touch was removed in 012_events_to_tasks along with
	// the legacy events table.
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

	assertIndexExists(t, db, "idx_tasks_time")
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

func TestMigrate_009NotificationsAddsIndexes(t *testing.T) {
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
	assert.Contains(t, versions, "009_notifications")

	assertIndexExists(t, db, "idx_notifications_user_unread")
	assertIndexExists(t, db, "idx_notifications_target")
	assertIndexExists(t, db, "idx_bot_subs_user")
}

func TestMigrate_010BackupsAddsIndexes(t *testing.T) {
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
	assert.Contains(t, versions, "010_backups")
	assertIndexExists(t, db, "idx_backup_log_type_created")
}

func TestMigrate_013SubtasksToChildren(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")

	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()

	// 1. Apply every migration up to 012, then seed two subtasks that
	//    reference a real parent task. This simulates a pre-Phase-14 DB.
	applyUpTo(t, ctx, db, "012_events_to_tasks")

	const projectID = "p-013"
	const parentID = "task-parent-013"
	const childA = "task-child-a-013"
	const childB = "task-child-b-013"

	// Seed owner, project, parent task, and two subtasks (one done).
	_, err = db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)`,
		"u-013", "owner@013.local", "x", "Owner")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO projects (id, name, owner_id) VALUES (?, 'p', 'u-013')`,
		projectID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO tasks (id, project_id, title) VALUES (?, ?, 'parent')`,
		parentID, projectID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO subtasks (id, task_id, title, done, position) VALUES
		    (?, ?, 'first',  0, 0),
		    (?, ?, 'second', 1, 1)
	`, childA, parentID, childB, parentID)
	require.NoError(t, err)

	// 2. Apply the rest of the migrations. 013 will fold subtasks into tasks.
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))

	versions, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.Contains(t, versions, "013_subtasks_to_children")

	// 3. The subtasks table is gone.
	assertTableNotExists(t, db, "subtasks")
	assertIndexNotExists(t, db, "idx_subtasks_task")

	// 4. The two rows now live under `tasks` with parent_task_id set.
	rows, err := db.QueryContext(ctx, `
		SELECT title, status, parent_task_id, project_id
		FROM tasks
		WHERE parent_task_id = ?
		ORDER BY position
	`, parentID)
	require.NoError(t, err)
	defer rows.Close()

	type child struct {
		title   string
		status  string
		parent  string
		project string
	}
	var got []child
	for rows.Next() {
		var c child
		require.NoError(t, rows.Scan(&c.title, &c.status, &c.parent, &c.project))
		got = append(got, c)
	}
	require.NoError(t, rows.Err())
	require.Len(t, got, 2)
	assert.Equal(t, "first", got[0].title)
	assert.Equal(t, "todo", got[0].status)
	assert.Equal(t, "second", got[1].title)
	assert.Equal(t, "done", got[1].status)
	for _, c := range got {
		assert.Equal(t, parentID, c.parent)
		assert.Equal(t, projectID, c.project)
	}
}

func TestMigrate_014ChildTasksInheritColumn(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")

	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()

	// Apply every migration up to 013 — leaves the schema with one
	// child task whose column_id is NULL (we set it manually before
	// 014 runs).
	applyUpTo(t, ctx, db, "013_subtasks_to_children")

	const ownerID = "u-014"
	const projectID = "p-014"
	const colID = "col-014"
	const parentID = "task-parent-014"
	const childID = "task-child-014"

	_, err = db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)`,
		ownerID, "owner@014.local", "x", "Owner")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO projects (id, name, owner_id) VALUES (?, 'p', ?)`,
		projectID, ownerID)
	require.NoError(t, err)
	// One board + one column for the parent to live in.
	_, err = db.ExecContext(ctx,
		`INSERT INTO boards (id, project_id, name, position) VALUES ('b-014', ?, 'main', 0)`,
		projectID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO columns (id, board_id, name, position) VALUES (?, 'b-014', 'todo', 0)`,
		colID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO tasks (id, project_id, column_id, title) VALUES (?, ?, ?, 'parent')`,
		parentID, projectID, colID)
	require.NoError(t, err)
	// Pre-existing child with NULL column_id (simulates a subtask
	// row that came through migration 013 unchanged).
	_, err = db.ExecContext(ctx,
		`INSERT INTO tasks (id, project_id, parent_task_id, title, column_id) VALUES (?, ?, ?, 'child', NULL)`,
		childID, projectID, parentID)
	require.NoError(t, err)

	// Apply the rest. 014 should copy parent's column_id onto the child.
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))

	versions, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.Contains(t, versions, "014_child_tasks_inherit_column")

	var gotCol string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT column_id FROM tasks WHERE id = ?`, childID,
	).Scan(&gotCol))
	assert.Equal(t, colID, gotCol, "child should inherit parent's column")
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

func assertTableNotExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "table %s should not exist", name)
}

func assertIndexNotExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "index %s should not exist", name)
}

// applyUpTo applies every migration whose name sorts <= target and
// records them in schema_migrations so the next Migrate() call picks
// up at the next version.
func applyUpTo(t *testing.T, ctx context.Context, db *sql.DB, target string) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
		    version TEXT PRIMARY KEY,
		    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
	`)
	require.NoError(t, err)
	entries, err := fs.ReadDir(MigrationsFS, "migrations")
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".sql")
		if name > target {
			continue
		}
		body, err := MigrationsFS.ReadFile("migrations/" + e.Name())
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, string(body))
		require.NoError(t, err, "migration %s", name)
		_, err = db.ExecContext(ctx,
			`INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)`, name)
		require.NoError(t, err)
	}
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
