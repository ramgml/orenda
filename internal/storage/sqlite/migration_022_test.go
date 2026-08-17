package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Phase 31.1: study reminder scaffolding (022_study_planning).
//
// We pin five contracts:
//  1. Up migration adds pace_notes_md (default ”), study_course_id
//     (default NULL), partial index, and the study_proposals table.
//  2. Existing rows are unaffected: course has pace_notes_md = ”,
//     task1 keeps its seeded study_course_id, task2 keeps NULL.
//  3. FK SET NULL: deleting the course clears study_course_id on
//     tasks; the task itself is NOT deleted (it was an inbox reminder
//     before becoming detached).
//  4. FK CASCADE on study_proposals.course_id: deleting the course
//     also drops its proposals.
//  5. CHECK constraint rejects invalid status values.
//  6. Down drops the columns, the index, and the proposals table;
//     data is lost (lossy by design — down is for round-trip tests).
func TestMigrate_022StudyPlanning(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	applyUpTo(t, ctx, db, "021_agent_type_labels")

	// Minimal fixture: 1 owner, 1 api_token, 1 agent, 1 course,
	// 2 tasks. We bypass repositories on purpose — the migration
	// has to work with raw column shapes, not repository validation.
	const (
		ownerID  = "u-022"
		tokenID  = "tok-022"
		agentID  = "a-022"
		courseID = "c-022"
		task1ID  = "t-022-1" // seeded with study_course_id
		task2ID  = "t-022-2" // seeded without study_course_id
	)
	_, err = db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)`,
		ownerID, "u@022.local", "x", "U")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO api_tokens (id, user_id, name, hash, scopes) VALUES (?, ?, ?, ?, '[]')`,
		tokenID, ownerID, "seed", "h")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO agents (id, name, type, token_id, max_concurrent) VALUES (?, ?, ?, ?, 3)`,
		agentID, "planner", "[]", tokenID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO courses (id, title, owner_id, status) VALUES (?, ?, ?, 'active')`,
		courseID, "Rust", ownerID)
	require.NoError(t, err)
	// Before the migration runs, study_course_id does not exist on
	// tasks. We can't pre-seed it; the assertion is that AFTER the
	// migration it ends up at the value we set below on task1.
	_, err = db.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, project_id) VALUES (?, ?, ?, NULL)`,
		task1ID, "Reminder 1", "todo")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, project_id) VALUES (?, ?, ?, NULL)`,
		task2ID, "Plan task", "todo")
	require.NoError(t, err)

	// Apply the migration under test.
	body, err := MigrationsFS.ReadFile("migrations/022_study_planning.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(body))
	require.NoError(t, err)

	// Contract 1: new columns and table exist.
	var pace string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT pace_notes_md FROM courses WHERE id = ?`, courseID).Scan(&pace))
	assert.Equal(t, "", pace, "course.pace_notes_md defaulted to ''")

	var study sql.NullString
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT study_course_id FROM tasks WHERE id = ?`, task1ID).Scan(&study))
	assert.False(t, study.Valid, "task1.study_course_id defaulted to NULL")

	// Now backfill study_course_id on task1 — this exercises a normal
	// post-migration write.
	_, err = db.ExecContext(ctx,
		`UPDATE tasks SET study_course_id = ? WHERE id = ?`,
		courseID, task1ID)
	require.NoError(t, err)
	var studyAfter sql.NullString
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT study_course_id FROM tasks WHERE id = ?`, task1ID).Scan(&studyAfter))
	require.True(t, studyAfter.Valid)
	assert.Equal(t, courseID, studyAfter.String, "task1.study_course_id backfilled")

	// Contract 1 (cont'd): partial index is in place.
	indexes := listIndexes(t, ctx, db, "tasks")
	assert.Contains(t, indexes, "idx_tasks_study_course",
		"partial index on study_course_id must exist")

	// Contract 1 (cont'd): study_proposals table is queryable.
	propID := "p-022-1"
	_, err = db.ExecContext(ctx,
		`INSERT INTO study_proposals (id, course_id, title, target_date, created_by_agent) VALUES (?, ?, ?, ?, ?)`,
		propID, courseID, "Study caps", "2026-08-17", agentID)
	require.NoError(t, err)

	// Contract 2: existing course has pace_notes_md = ''; task2
	// (seeded without study_course_id) stays NULL.
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT pace_notes_md FROM courses WHERE id = ?`, courseID).Scan(&pace))
	assert.Equal(t, "", pace)
	var task2Study sql.NullString
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT study_course_id FROM tasks WHERE id = ?`, task2ID).Scan(&task2Study))
	assert.False(t, task2Study.Valid, "task2.study_course_id still NULL")

	// Contract 3 + 4: delete the course. study_proposals cascade,
	// tasks.study_course_id clears to NULL, tasks remain.
	_, err = db.ExecContext(ctx, `DELETE FROM courses WHERE id = ?`, courseID)
	require.NoError(t, err)

	var task1StudyAfterDel sql.NullString
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT study_course_id FROM tasks WHERE id = ?`, task1ID).Scan(&task1StudyAfterDel))
	assert.False(t, task1StudyAfterDel.Valid,
		"course delete must SET NULL on tasks.study_course_id (task survives)")

	// task1 still exists.
	var taskTitle string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT title FROM tasks WHERE id = ?`, task1ID).Scan(&taskTitle))
	assert.Equal(t, "Reminder 1", taskTitle,
		"task must survive course deletion (only the link clears)")

	// proposal cascaded.
	row := db.QueryRowContext(ctx,
		`SELECT count(*) FROM study_proposals WHERE id = ?`, propID)
	var count int
	require.NoError(t, row.Scan(&count))
	assert.Equal(t, 0, count,
		"study_proposals.course_id ON DELETE CASCADE drops the proposal")

	// Contract 5: CHECK constraint on status.
	_, err = db.ExecContext(ctx,
		`INSERT INTO study_proposals (id, title, target_date, created_by_agent, status) VALUES (?, ?, ?, ?, 'invalid')`,
		"p-022-bad", "X", "2026-08-17", agentID)
	require.Error(t, err, "status CHECK constraint must reject invalid values")

	// Also: status must be one of the three known values — verify
	// that 'pending' (the default) is accepted and round-trips.
	_, err = db.ExecContext(ctx,
		`INSERT INTO study_proposals (id, title, target_date, created_by_agent) VALUES (?, ?, ?, ?)`,
		"p-022-ok", "Caps", "2026-08-17", agentID)
	require.NoError(t, err)
	var status string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT status FROM study_proposals WHERE id = ?`, "p-022-ok").Scan(&status))
	assert.Equal(t, "pending", status)

	// Contract 6: down — drops columns, index, table. Data is lost.
	downBody, err := MigrationsFS.ReadFile("migrations/022_study_planning.down.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(downBody))
	require.NoError(t, err)

	// study_course_id gone.
	_, err = db.ExecContext(ctx, `SELECT study_course_id FROM tasks WHERE id = ?`, task1ID)
	require.Error(t, err, "tasks.study_course_id should be dropped")

	// pace_notes_md gone.
	_, err = db.ExecContext(ctx, `SELECT pace_notes_md FROM courses LIMIT 1`)
	require.Error(t, err, "courses.pace_notes_md should be dropped")

	// study_proposals table gone.
	_, err = db.ExecContext(ctx, `SELECT id FROM study_proposals LIMIT 1`)
	require.Error(t, err, "study_proposals table should be dropped")

	// Index gone.
	assert.NotContains(t, listIndexes(t, ctx, db, "tasks"), "idx_tasks_study_course",
		"partial index must be dropped")

	// Note: SQLite has no `ALTER TABLE ... DROP COLUMN IF EXISTS` —
	// running the down a second time will error on the second
	// `DROP COLUMN`. That's not part of the runner contract
	// (`MigrateDown` runs each version at most once). 021's
	// idempotency contract is about idempotent UPDATEs, not schema
	// changes; for 022 the inverse is destructive by design.
}

// listIndexes returns the index names on a table. We use a dedicated
// helper rather than fishing through sqlite_master in every assertion
// to keep the assertions easy to read.
func listIndexes(t *testing.T, ctx context.Context, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name = ?`, table)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		out = append(out, n)
	}
	require.NoError(t, rows.Err())
	return out
}
