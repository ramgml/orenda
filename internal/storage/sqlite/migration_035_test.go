package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Migration 035: course_lessons.completed_at + idx_course_lessons_completed
// (Phase 32.12, wiki:lms-pace-adaptation).
//
// Contracts:
//  1. Up migration adds course_lessons.completed_at TEXT NULL.
//  2. Existing rows keep completed_at = NULL after the migration.
//  3. The new index idx_course_lessons_completed is created (partial
//     on completed_at IS NOT NULL).
//  4. NULL completed_at rows contribute zero to VelocityStatsByCourse
//     (legacy data — the planner sees slower pace than reality,
//     never faster; the wiki calls this out as conservative).
//  5. Down migration drops the index and the column.
func TestMigrate_035LessonCompletedAt(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	applyUpTo(t, ctx, db, "034_project_wiki_slug")

	// Seed a course + module + lesson so the velocity query has rows
	// to chew on. ownerID is required (NOT NULL) and courses.title
	// has a length constraint — keep them short.
	const (
		ownerID  = "u-035"
		courseID = "c-035"
		moduleID = "m-035"
		lessonA  = "l-035-a"
		lessonB  = "l-035-b"
	)
	_, err = db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)`,
		ownerID, "u@035.local", "x", "U")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO courses (id, owner_id, title, status, pace, intent_md) VALUES (?, ?, ?, ?, ?, ?)`,
		courseID, ownerID, "C", "active", "regular", "")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO course_modules (id, course_id, title, position) VALUES (?, ?, ?, ?)`,
		moduleID, courseID, "M", 0)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO course_lessons (id, module_id, title, position, status) VALUES (?, ?, ?, ?, ?)`,
		lessonA, moduleID, "L1", 0, "done")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO course_lessons (id, module_id, title, position, status) VALUES (?, ?, ?, ?, ?)`,
		lessonB, moduleID, "L2", 1, "done")
	require.NoError(t, err)

	// Contract 1 + 2: column exists, default NULL on existing rows.
	body, err := MigrationsFS.ReadFile("migrations/035_lesson_completed_at.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(body))
	require.NoError(t, err)

	var hasCol int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info('course_lessons') WHERE name = 'completed_at'`).
		Scan(&hasCol))
	assert.Equal(t, 1, hasCol, "course_lessons.completed_at must exist after up")

	var completedA sql.NullString
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT completed_at FROM course_lessons WHERE id = ?`, lessonA).Scan(&completedA))
	assert.False(t, completedA.Valid, "existing done-lessons must default completed_at to NULL")

	// Contract 3: index created.
	assert.Contains(t, listIndexes(t, ctx, db, "course_lessons"), "idx_course_lessons_completed",
		"idx_course_lessons_completed must exist after migration")

	// Contract 4: NULL completed_at contributes zero to VelocityStatsByCourse.
	// Stamp lessonB with a non-null completed_at inside the window so
	// the row counts. lessonA stays NULL — it must NOT count.
	_, err = db.ExecContext(ctx,
		`UPDATE course_lessons SET completed_at = ? WHERE id = ?`,
		"2026-08-19T12:00:00Z", lessonB)
	require.NoError(t, err)
	courseRepo := NewCourseRepository(db)
	stats, err := courseRepo.VelocityStatsByCourse(ctx, courseID, mustParse("2026-08-05T00:00:00Z"))
	require.NoError(t, err)
	assert.Equal(t, 1, stats.LessonsDoneInWindow,
		"only the stamped lesson must count; NULL completed_at is legacy → 0")
	require.NotNil(t, stats.LastCompletedAt,
		"LastCompletedAt must surface the stamped timestamp")
	assert.Equal(t, "2026-08-19T12:00:00Z", stats.LastCompletedAt.UTC().Format("2006-01-02T15:04:05Z"))

	// Contract 5: down migration drops the index and the column.
	downBody, err := MigrationsFS.ReadFile("migrations/035_lesson_completed_at.down.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(downBody))
	require.NoError(t, err)

	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info('course_lessons') WHERE name = 'completed_at'`).
		Scan(&hasCol))
	assert.Equal(t, 0, hasCol, "course_lessons.completed_at must be dropped after down")

	assert.NotContains(t, listIndexes(t, ctx, db, "course_lessons"), "idx_course_lessons_completed",
		"idx_course_lessons_completed must be dropped after down")
}

// mustParse is a tiny helper that parses a time literal and t.Fatal's
// on error — keeps the test body focused on the migration contracts.
func mustParse(s string) time.Time {
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return ts
}
