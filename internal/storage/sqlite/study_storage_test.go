package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/course"
	"github.com/ramgml/orenda/internal/domain/task"
)

// Phase 31: tasks.study_course_id round-trips through Create /
// GetByID / Update. The repo carries the column on every SELECT;
// this test pins the round-trip.
func TestTaskRepo_StudyCourseIDRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openStudyTestDB(t)
	t.Cleanup(func() { _ = db.Close() })

	// Seed: an owner + a course (FK target). Inbox task ⇒
	// project_id NULL + column_id NULL — the typical reminder
	// shape after accept.
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)`,
		"u-study", "study@031.local", "x", "U")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO courses (id, title, owner_id, status) VALUES (?, ?, ?, 'active')`,
		"c-study", "Rust", "u-study")
	require.NoError(t, err)

	repo := NewTaskRepository(db)

	t.Run("create + get", func(t *testing.T) {
		tr := &task.Task{
			Title:         "Read chapter 5",
			StudyCourseID: "c-study",
		}
		require.NoError(t, repo.Create(ctx, tr))
		assert.NotEmpty(t, tr.ID)
		got, err := repo.GetByID(ctx, tr.ID)
		require.NoError(t, err)
		assert.Equal(t, "c-study", got.StudyCourseID, "study_course_id persisted")
	})

	t.Run("create without study link stays empty", func(t *testing.T) {
		_, err := db.ExecContext(ctx,
			`INSERT INTO projects (id, name, owner_id) VALUES (?, ?, ?)`,
			"p-study", "P", "u-study")
		require.NoError(t, err)
		tr := &task.Task{Title: "Plain task", ProjectID: "p-study"}
		require.NoError(t, repo.Create(ctx, tr))
		got, err := repo.GetByID(ctx, tr.ID)
		require.NoError(t, err)
		assert.Equal(t, "", got.StudyCourseID)
	})

	t.Run("update attaches a study_course_id", func(t *testing.T) {
		tr := &task.Task{Title: "Patch me", ProjectID: "p-study"}
		require.NoError(t, repo.Create(ctx, tr))
		assert.Equal(t, "", tr.StudyCourseID, "fresh task has no link")

		tr.StudyCourseID = "c-study"
		require.NoError(t, repo.Update(ctx, tr))
		got, err := repo.GetByID(ctx, tr.ID)
		require.NoError(t, err)
		assert.Equal(t, "c-study", got.StudyCourseID, "PATCH persisted the link")
	})

	t.Run("update clears study_course_id when set to empty", func(t *testing.T) {
		tr := &task.Task{Title: "Clear me", StudyCourseID: "c-study"}
		require.NoError(t, repo.Create(ctx, tr))

		tr.StudyCourseID = ""
		require.NoError(t, repo.Update(ctx, tr))
		got, err := repo.GetByID(ctx, tr.ID)
		require.NoError(t, err)
		assert.Equal(t, "", got.StudyCourseID, "empty string clears the link")
	})

	t.Run("course delete clears the link on remaining tasks", func(t *testing.T) {
		// Use a separate course + task so the previous tests don't
		// observe this FK action.
		_, err := db.ExecContext(ctx,
			`INSERT INTO courses (id, title, owner_id, status) VALUES (?, ?, ?, 'active')`,
			"c-temp", "Tmp", "u-study")
		require.NoError(t, err)
		tr := &task.Task{
			Title:         "Reminder",
			StudyCourseID: "c-temp",
		}
		require.NoError(t, repo.Create(ctx, tr))

		// Delete the course — SET NULL on tasks.study_course_id.
		_, err = db.ExecContext(ctx, `DELETE FROM courses WHERE id = ?`, "c-temp")
		require.NoError(t, err)

		got, err := repo.GetByID(ctx, tr.ID)
		require.NoError(t, err, "task must survive course deletion")
		assert.Equal(t, "", got.StudyCourseID, "study_course_id cleared by FK action")
	})
}

// Phase 31: pace_notes_md round-trips through Create / Get /
// Update / UpdatePaceNotesMD. The narrow PATCH is the agent-
// planner's only edit path on the field.
func TestCourseRepo_PaceNotesMDRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openStudyTestDB(t)
	t.Cleanup(func() { _ = db.Close() })

	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)`,
		"u-pn", "pn@031.local", "x", "U")
	require.NoError(t, err)

	repo := NewCourseRepository(db)

	t.Run("create persists pace_notes_md", func(t *testing.T) {
		c := newCourseDraft("c-pn-create")
		c.PaceNotesMD = "3 раза в неделю, по утрам"
		require.NoError(t, repo.CreateCourse(ctx, c))
		got, err := repo.GetCourse(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, "3 раза в неделю, по утрам", got.PaceNotesMD)
	})

	t.Run("update writes pace_notes_md", func(t *testing.T) {
		c := newCourseDraft("c-pn-update")
		require.NoError(t, repo.CreateCourse(ctx, c))

		c.PaceNotesMD = "после проваленного квиза — повторить"
		require.NoError(t, repo.UpdateCourse(ctx, c))

		got, err := repo.GetCourse(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, "после проваленного квиза — повторить", got.PaceNotesMD)
	})

	t.Run("narrow UpdatePaceNotesMD", func(t *testing.T) {
		c := newCourseDraft("c-pn-narrow")
		c.PaceNotesMD = "initial"
		require.NoError(t, repo.CreateCourse(ctx, c))

		require.NoError(t, repo.UpdatePaceNotesMD(ctx, c.ID, "replaced"))

		got, err := repo.GetCourse(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, "replaced", got.PaceNotesMD)
	})

	t.Run("UpdatePaceNotesMD trims whitespace", func(t *testing.T) {
		c := newCourseDraft("c-pn-trim")
		require.NoError(t, repo.CreateCourse(ctx, c))

		require.NoError(t, repo.UpdatePaceNotesMD(ctx, c.ID, "  hello  \n"))
		got, err := repo.GetCourse(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, "hello", got.PaceNotesMD, "UpdatePaceNotesMD trims before write")
	})

	t.Run("UpdatePaceNotesMD rejects oversized content", func(t *testing.T) {
		c := newCourseDraft("c-pn-huge")
		require.NoError(t, repo.CreateCourse(ctx, c))

		huge := make([]byte, 65537)
		for i := range huge {
			huge[i] = 'x'
		}
		require.Error(t, repo.UpdatePaceNotesMD(ctx, c.ID, string(huge)),
			"validation cap should reject oversized payload")
	})

	t.Run("UpdatePaceNotesMD unknown id returns ErrNotFound", func(t *testing.T) {
		require.ErrorIs(t, repo.UpdatePaceNotesMD(ctx, "no-such-course", "x"), course.ErrNotFound)
	})

	// openStudyTestDB pins the open-loop wiring shared with
	// study_proposal_repo_test. Defined there, used here.
	_ = db // unused assert above silences shadowing
}

// newCourseDraft returns a minimal valid draft course. The owner
// is hardcoded to "u-pn" because every caller in this package uses
// the same owner — the parameter was carried for symmetry but never
// actually varied.
func newCourseDraft(id string) *course.Course {
	return &course.Course{
		ID:      id,
		Title:   "Draft",
		Status:  course.StatusDraft,
		OwnerID: "u-pn",
		Level:   "beginner",
		Pace:    "casual",
	}
}

// openStudyTestDB opens a fresh SQLite DB with all migrations applied.
// Used by the storage tests in this file plus the proposal lifecycle
// test in study_proposal_repo_test.go (declared in the same package).
func openStudyTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := Migrate(context.Background(), db, MigrationsFS, "migrations"); err != nil {
		_ = db.Close()
		t.Fatalf("migrate: %v", err)
	}
	return db
}

var _ = (*sql.Tx)(nil) // keep database/sql import live even if some subtests don't use it
