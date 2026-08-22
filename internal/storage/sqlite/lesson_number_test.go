package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/course"
)

// seedLessonCourse creates a course with a module for lesson tests.
func seedLessonCourse(t *testing.T, db interface {
	ExecContext(ctx context.Context, query string, args ...any) (interface{ RowsAffected() (int64, error) }, error)
	QueryRowContext(ctx context.Context, query string, args ...any) interface{ Scan(dest ...any) error }
}, owner string) (courseID, moduleID string) {
	t.Helper()
	courseID = "c-lesson-seed"
	moduleID = "m-lesson-seed"
	_, _ = db.ExecContext(context.Background(),
		`INSERT INTO courses (id, title, owner_id, status) VALUES (?, ?, ?, 'active')`,
		courseID, "Seed Course", owner)
	_, _ = db.ExecContext(context.Background(),
		`INSERT INTO course_modules (id, course_id, title, position) VALUES (?, ?, ?, 0)`,
		moduleID, courseID, "M1")
	return courseID, moduleID
}

// TestLessonRepo_NumberAssignedSequentially pins the Phase-39 contract:
// every CreateLesson draws COALESCE(MAX(number),0)+1, so numbers are
// 1-based and monotonically increasing in creation order.
func TestLessonRepo_NumberAssignedSequentially(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()

	c := &course.Course{
		Title:   "Course for Lessons",
		Status:  course.StatusDraft,
		Level:   "beginner",
		Pace:    "regular",
		OwnerID: owner,
	}
	require.NoError(t, repo.CreateCourse(ctx, c))

	m := &course.Module{
		CourseID: c.ID,
		Title:    "M1",
		Position: 0,
	}
	require.NoError(t, repo.CreateModule(ctx, m))

	var prev int
	for range 5 {
		l := &course.Lesson{
			ModuleID: m.ID,
			Title:    "Lesson",
			Status:   course.LessonLocked,
			Position: prev,
		}
		require.NoError(t, repo.CreateLesson(ctx, l))
		assert.Equal(t, prev+1, l.Number, "lesson numbers must be sequential")
		prev = l.Number
	}
	assert.Equal(t, 5, prev)
}

// TestLessonRepo_NumberNeverReused: deleting a lesson must NOT free its
// number — an "L10" reference in a commit message has to keep pointing
// at the same (now deleted) lesson forever.
func TestLessonRepo_NumberNeverReused(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()

	c := &course.Course{
		Title:   "Course",
		Status:  course.StatusDraft,
		Level:   "beginner",
		Pace:    "regular",
		OwnerID: owner,
	}
	require.NoError(t, repo.CreateCourse(ctx, c))

	m := &course.Module{
		CourseID: c.ID,
		Title:    "M1",
		Position: 0,
	}
	require.NoError(t, repo.CreateModule(ctx, m))

	pos := 0
	mk := func() *course.Lesson {
		l := &course.Lesson{
			ModuleID: m.ID,
			Title:    "Lesson",
			Status:   course.LessonLocked,
			Position: pos,
		}
		pos++
		require.NoError(t, repo.CreateLesson(ctx, l))
		return l
	}

	t.Run("delete head", func(t *testing.T) {
		a, b, c := mk(), mk(), mk()
		require.NoError(t, repo.DeleteLesson(ctx, c.ID))
		d := mk()
		assert.Equal(t, c.Number+1, d.Number,
			"after deleting the newest lesson its number must stay burned")
		_, err := repo.GetLessonByNumber(ctx, c.Number)
		assert.ErrorIs(t, err, course.ErrNotFound)
		_ = a
		_ = b
	})

	t.Run("delete middle", func(t *testing.T) {
		a, b, c := mk(), mk(), mk()
		require.NoError(t, repo.DeleteLesson(ctx, b.ID))
		d := mk()
		assert.Equal(t, c.Number+1, d.Number,
			"after deleting a middle lesson its number must stay burned")
		_, err := repo.GetLessonByNumber(ctx, b.Number)
		assert.ErrorIs(t, err, course.ErrNotFound)
		_ = a
		_ = c
	})
}

// TestLessonRepo_GetByNumber: the "L<N>" lookup — hit and miss.
func TestLessonRepo_GetByNumber(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()

	c := &course.Course{
		Title:   "Course",
		Status:  course.StatusDraft,
		Level:   "beginner",
		Pace:    "regular",
		OwnerID: owner,
	}
	require.NoError(t, repo.CreateCourse(ctx, c))

	m := &course.Module{
		CourseID: c.ID,
		Title:    "M1",
		Position: 0,
	}
	require.NoError(t, repo.CreateModule(ctx, m))

	l := &course.Lesson{
		ModuleID: m.ID,
		Title:    "First Lesson",
		Status:   course.LessonLocked,
		Position: 0,
	}
	require.NoError(t, repo.CreateLesson(ctx, l))
	require.NotZero(t, l.Number)

	got, err := repo.GetLessonByNumber(ctx, l.Number)
	require.NoError(t, err)
	assert.Equal(t, l.ID, got.ID)
	assert.Equal(t, "First Lesson", got.Title)

	// Miss: number 999999 doesn't exist.
	_, err = repo.GetLessonByNumber(ctx, 999999)
	assert.ErrorIs(t, err, course.ErrNotFound)
}
