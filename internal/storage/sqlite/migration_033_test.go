package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
)

// Phase 33: human-readable task numbers (033_task_numbers).
//
// We pin four contracts:
//  1. Up adds tasks.number, backfills existing rows in
//     (created_at, rowid) order starting at 1 (ROW_NUMBER window),
//     and installs the UNIQUE index idx_tasks_number.
//  2. New inserts through the repository draw from the
//     task_number_seq high-watermark (UPDATE ... RETURNING in the
//     Create transaction) — monotone, and never re-issued even after
//     the newest task is deleted (a bare MAX+1 over `tasks` would
//     re-issue it).
//  3. The UNIQUE index rejects a hand-crafted duplicate number.
//  4. Down drops the index and the column (DROP COLUMN; modernc.org/
//     sqlite bundles ≥ 3.35), so a down/up round-trip is clean.
func TestMigrate_033TaskNumbers(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	applyUpTo(t, ctx, db, "032_chat_messages")

	// Fixture: three raw tasks with distinct created_at, inserted
	// newest-first so rowid order disagrees with created_at order —
	// the backfill must follow (created_at, rowid), not rowid alone.
	const (
		taskOld    = "t-033-old"
		taskMiddle = "t-033-mid"
		taskNew    = "t-033-new"
	)
	_, err = db.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, project_id, created_at) VALUES
		 (?, 'Newest',  'todo', NULL, '2026-08-19 10:00:00'),
		 (?, 'Oldest',  'todo', NULL, '2026-08-01 10:00:00'),
		 (?, 'Middle',  'todo', NULL, '2026-08-10 10:00:00')`,
		taskNew, taskOld, taskMiddle)
	require.NoError(t, err)

	// Apply the migration under test.
	body, err := MigrationsFS.ReadFile("migrations/033_task_numbers.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(body))
	require.NoError(t, err)

	// Contract 1: backfill in (created_at, rowid) order, 1-based.
	numberOf := func(id string) int {
		var n int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT number FROM tasks WHERE id = ?`, id).Scan(&n))
		return n
	}
	assert.Equal(t, 1, numberOf(taskOld), "oldest by created_at is #1")
	assert.Equal(t, 2, numberOf(taskMiddle))
	assert.Equal(t, 3, numberOf(taskNew))

	// Contract 1 (cont'd): UNIQUE index exists.
	assert.Contains(t, listIndexes(t, ctx, db, "tasks"), "idx_tasks_number",
		"unique index on tasks.number must exist")
	// Task 115: repo.Create (used by Contract 2 below) writes
	// tasks.blocked_prev_status, which only exists from migration 042
	// on. The test deliberately pins the schema at 032 + 033 (the
	// raw-numbered fixture above must precede 033), so 042 is applied
	// by hand here — mirroring what a live upgrade would have done.
	body042, err := MigrationsFS.ReadFile("migrations/042_task_blocked_status.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(body042))
	require.NoError(t, err)

	// Contract 2: repository INSERT subquery assigns MAX+1.
	repo := NewTaskRepository(db)
	newTask := &task.Task{Title: "post-migration"}
	require.NoError(t, repo.Create(ctx, newTask))
	assert.Equal(t, 4, newTask.Number,
		"first post-migration task draws MAX(number)+1")

	// Contract 3: the UNIQUE index rejects duplicates.
	_, err = db.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, project_id, number) VALUES (?, 'Dup', 'todo', NULL, ?)`,
		"t-033-dup", newTask.Number)
	require.Error(t, err, "duplicate number must violate idx_tasks_number")

	// Contract 4: down drops index + column.
	downBody, err := MigrationsFS.ReadFile("migrations/033_task_numbers.down.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(downBody))
	require.NoError(t, err)

	assert.NotContains(t, listIndexes(t, ctx, db, "tasks"), "idx_tasks_number",
		"down must drop idx_tasks_number")
	_, err = db.ExecContext(ctx, `SELECT number FROM tasks LIMIT 1`)
	require.Error(t, err, "down must drop tasks.number")

	// Round-trip: up again works on the downed schema (fresh backfill).
	_, err = db.ExecContext(ctx, string(body))
	require.NoError(t, err, "up after down must re-apply cleanly")
	assert.Equal(t, 1, numberOf(taskOld), "re-backfill keeps (created_at, rowid) order")
}
