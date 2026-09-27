package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// baselineTables is the full table set 001_baseline.up.sql must leave
// behind (schema_migrations bookkeeping excluded — the runner owns it).
var baselineTables = []string{
	"users", "api_tokens", "agents", "wiki_pages", "projects", "boards",
	"columns", "tags", "comments", "mentions", "attachments", "wiki_links",
	"notifications", "bot_subscriptions", "backup_settings", "backup_log",
	"sync_ops", "tasks", "courses", "task_locks", "checklists",
	"checklist_items", "task_tags", "task_activity", "time_entries",
	"task_dependencies", "course_modules", "course_lessons", "course_quizzes",
	"study_proposals", "course_activity", "project_activity", "task_retracted",
	"chat_messages", "chat_threads", "wiki_blocks", "project_agents",
	"tutor_messages", "lesson_reviews",
	"task_number_seq", "project_number_seq", "wiki_page_number_seq",
	"course_number_seq", "lesson_number_seq",
}

// openTestDB connects to the cluster named by ORENDA_TEST_PG_DSN and
// returns a handle to a fresh throwaway database (created via CREATE
// DATABASE, dropped with FORCE at cleanup). Running the migration cycle
// against a dedicated database keeps the test side-effect free for
// whatever the DSN points at.
//
// The cleanup drops through its own connection and verifies via
// pg_database that the throwaway is really gone: a run of this suite
// must leave zero orenda_pgtest_* databases behind.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("ORENDA_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("ORENDA_TEST_PG_DSN not set; postgres runner tests need a real server")
	}

	admin, err := Open(context.Background(), dsn)
	require.NoError(t, err, "connect to test cluster")
	defer func() { _ = admin.Close() }()

	suffix := make([]byte, 4)
	_, err = rand.Read(suffix)
	require.NoError(t, err)
	testName := "orenda_pgtest_" + hex.EncodeToString(suffix)

	_, err = admin.Exec(fmt.Sprintf(`CREATE DATABASE %s`, testName))
	require.NoError(t, err, "create throwaway database")

	t.Cleanup(func() {
		// The admin handle above is already closed when cleanups run,
		// so the drop goes through a fresh connection — a silent drop
		// failure would leak orenda_pgtest_* databases. FORCE
		// terminates the still-open test handle's connections (PG 13+).
		vadmin, err := Open(context.Background(), dsn)
		if err != nil {
			t.Errorf("cleanup: connect to drop %s: %v", testName, err)
			return
		}
		defer func() { _ = vadmin.Close() }()
		if _, err := vadmin.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, testName)); err != nil {
			t.Errorf("cleanup: drop %s: %v", testName, err)
			return
		}
		var leftovers int
		if err := vadmin.QueryRow(`SELECT count(*) FROM pg_database WHERE datname = $1`, testName).Scan(&leftovers); err != nil {
			t.Errorf("cleanup: verify %s dropped: %v", testName, err)
			return
		}
		if leftovers != 0 {
			t.Errorf("cleanup: database %s still exists after drop", testName)
		}
	})

	u, err := url.Parse(dsn)
	require.NoError(t, err, "test DSN must be a URL for the database swap")
	u.Path = "/" + testName
	db, err := Open(context.Background(), u.String())
	require.NoError(t, err, "connect to throwaway database")
	return db
}

// listTables returns the table names visible in the public schema.
func listTables(t *testing.T, db *sql.DB) map[string]struct{} {
	t.Helper()
	rows, err := db.Query(`SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	out := make(map[string]struct{})
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		out[name] = struct{}{}
	}
	require.NoError(t, rows.Err())
	return out
}

// TestMigrateUpDownUpCycle walks the full lifecycle on a real server:
// up → every migration applied (001_baseline + 002_search) and version
// recorded; repeated up → no-op; down → the most recent migration's
// objects rolled back and its version unrecorded while the baseline
// stays; up again → clean reapply.
func TestMigrateUpDownUpCycle(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	// -- up: full chain applied ------------------------------------------
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))

	versions, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, []string{"001_baseline", "002_search"}, versions, "every migration applied once, in order")

	tables := listTables(t, db)
	for _, want := range baselineTables {
		assert.Contains(t, tables, want, "baseline table created")
	}

	// -- repeated up: no-op ----------------------------------------------
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))
	versionsAfter, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, versions, versionsAfter, "repeated up must not reapply or duplicate versions")

	// -- down: most recent migration rolled back --------------------------
	// One MigrateDown steps back exactly one migration: 002_search's
	// objects disappear, the baseline stays intact.
	require.NoError(t, MigrateDown(ctx, db, MigrationsFS, "migrations"))

	versionsDown, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, []string{"001_baseline"}, versionsDown, "only the head version unrecorded after down")

	var searchColumns int
	require.NoError(t, db.QueryRow(
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_schema = 'public' AND column_name = 'search_vec'`,
	).Scan(&searchColumns))
	assert.Zero(t, searchColumns, "002_search generated columns dropped by down")

	var searchIndexes int
	require.NoError(t, db.QueryRow(
		`SELECT count(*) FROM pg_indexes
		 WHERE schemaname = 'public' AND indexname LIKE 'idx_%_search'`,
	).Scan(&searchIndexes))
	assert.Zero(t, searchIndexes, "002_search GIN indexes dropped by down")

	tablesDown := listTables(t, db)
	for _, want := range baselineTables {
		assert.Contains(t, tablesDown, want, "baseline tables survive the 002 down")
	}

	// -- up again: clean reapply ------------------------------------------
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))
	versionsAgain, err := AppliedVersions(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, versions, versionsAgain, "full chain reapplies after down")

	// The regenerated tsvector columns are live: a stored document is
	// searchable immediately after the reapply.
	_, err = db.Exec(`INSERT INTO comments (id, target_type, target_id, author_type, author_id, body_md)
		VALUES ('c-fts', 'task', 't-fts', 'user', 'u1', 'searchable reapply body')`)
	require.NoError(t, err)
	var matched bool
	require.NoError(t, db.QueryRow(
		`SELECT search_vec @@ phraseto_tsquery('simple', 'reapply body') FROM comments WHERE id = 'c-fts'`,
	).Scan(&matched))
	assert.True(t, matched, "generated column indexes new rows after reapply")
}

// TestMigrateDownWithoutMigrations pins the runner's contract for an
// empty database: down without any applied version is a clean error,
// not a crash and not a silent success.
func TestMigrateDownWithoutMigrations(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	err := MigrateDown(ctx, db, MigrationsFS, "migrations")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no migrations applied")
}

// TestBaselineRuntimeContracts exercises the sqlite-mirror behaviours the
// baseline DDL promises: mentions.rowid ordering, GENERATED ALWAYS
// rejecting explicit rowid inserts, the updated_at touch trigger, and the
// number_seq UPDATE..RETURNING high-watermark pattern.
func TestBaselineRuntimeContracts(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))

	// mentions.rowid: identity + insertion-order mirrors comment_repo's
	// `ORDER BY rowid ASC` body-order contract.
	_, err := db.Exec(`INSERT INTO comments (id, target_type, target_id, author_type, author_id, body_md)
		VALUES ('c1', 'task', 't1', 'user', 'u1', 'body')`)
	require.NoError(t, err)
	for _, target := range []string{"u1", "a1", "u2"} {
		_, err := db.Exec(`INSERT INTO mentions (comment_id, target_type, target_id)
			VALUES ('c1', 'user', $1)`, target)
		require.NoError(t, err, "INSERT must not name rowid (GENERATED ALWAYS)")
	}
	var rowids []int64
	rows, err := db.Query(`SELECT rowid FROM mentions ORDER BY rowid ASC`)
	require.NoError(t, err)
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		rowids = append(rowids, id)
	}
	require.NoError(t, rows.Err())
	_ = rows.Close()
	assert.Equal(t, []int64{1, 2, 3}, rowids, "identity starts at 1 and preserves insertion order")

	// GENERATED ALWAYS must reject explicit rowid writes.
	_, err = db.Exec(`INSERT INTO mentions (rowid, comment_id, target_type, target_id)
		VALUES (99, 'c1', 'user', 'u3')`)
	assert.Error(t, err, "explicit rowid insert must fail (GENERATED ALWAYS)")

	// Touch trigger: BEFORE UPDATE overwrites an explicit stale
	// updated_at with the current time (sqlite trg_*_touch mirror).
	_, err = db.Exec(`INSERT INTO users (id, email, password_hash, display_name)
		VALUES ('u1', 'u1@test', 'h', 'U One')`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE users SET display_name = 'Renamed', updated_at = '2000-01-01 00:00:00' WHERE id = 'u1'`)
	require.NoError(t, err)
	var updatedAt string
	require.NoError(t, db.QueryRow(`SELECT updated_at FROM users WHERE id = 'u1'`).Scan(&updatedAt))
	assert.NotEqual(t, "2000-01-01 00:00:00", updatedAt, "touch trigger must overwrite stale updated_at")

	// number_seq tables: the BASELINE must seed the singleton rows —
	// sqlite migrations 033/036/037/038/039 do (`SELECT 1,
	// COALESCE(MAX(number), 0) + 1` → (1, 1) on a fresh database). The
	// test deliberately does not seed anything itself: an unseeded
	// table would make the repositories' watermark UPDATE find no rows
	// (sql.ErrNoRows) and the first Create would fail.
	numberSeqTables := []string{
		"task_number_seq", "project_number_seq", "wiki_page_number_seq",
		"course_number_seq", "lesson_number_seq",
	}
	for _, table := range numberSeqTables {
		var seeded int
		require.NoError(t, db.QueryRow(
			fmt.Sprintf(`SELECT count(*) FROM %s WHERE id = 1 AND next = 1`, table),
		).Scan(&seeded), "%s must be seeded by the baseline", table)
		assert.Equal(t, 1, seeded, "%s must hold exactly the singleton (1, 1)", table)
	}

	// High-watermark: the repository pattern verbatim, on top of the
	// migration-provided seed — first number is 1, watermark advanced.
	var number int
	require.NoError(t, db.QueryRow(
		`UPDATE task_number_seq SET next = next + 1 WHERE id = 1 RETURNING next - 1`).Scan(&number))
	assert.Equal(t, 1, number, "first number is 1, watermark advanced atomically")
}
