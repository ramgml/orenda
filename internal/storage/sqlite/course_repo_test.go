package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/course"
	"github.com/ramgml/orenda/internal/domain/user"
)

// Phase 18: course repository smoke tests.
//
// We focus on the small surface that catches the most likely
// regressions: lifecycle CRUD, the SubmitCurriculum atomic swap,
// and the Progress count.

func seedUser(t *testing.T, db interface {
	Exec(string, ...any) (any, error)
}) string {
	t.Helper()
	return ""
}

func setupCourseOwner(t *testing.T, db *sql.DB) string {
	t.Helper()
	users := NewUserRepository(db)
	u := &user.User{
		Email:        "course-owner@x.com",
		PasswordHash: "x",
		DisplayName:  "CourseOwner",
	}
	require.NoError(t, users.Create(context.Background(), u))
	return u.ID
}

func TestCourseRepo_BasicLifecycle(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()

	// Create draft.
	c := &course.Course{
		Title:    "Learn Rust",
		IntentMD: "One month, 3x/week",
		Level:    "beginner",
		Pace:     "regular",
		Status:   course.StatusDraft,
		OwnerID:  owner,
	}
	require.NoError(t, repo.CreateCourse(ctx, c))
	assert.NotEmpty(t, c.ID)

	got, err := repo.GetCourse(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "Learn Rust", got.Title)
	assert.Equal(t, course.StatusDraft, got.Status)

	// Update → review.
	got.Status = course.StatusReview
	require.NoError(t, repo.UpdateCourse(ctx, got))
	got2, _ := repo.GetCourse(ctx, c.ID)
	assert.Equal(t, course.StatusReview, got2.Status)

	// List by owner.
	all, err := repo.ListCourses(ctx, owner)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, c.ID, all[0].ID)

	// Delete.
	require.NoError(t, repo.DeleteCourse(ctx, c.ID))
	_, err = repo.GetCourse(ctx, c.ID)
	assert.ErrorIs(t, err, course.ErrNotFound)
}

func TestCourseRepo_SubmitCurriculum(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()

	// Create course in draft, then submit a curriculum.
	c := &course.Course{Title: "X", IntentMD: "y", Status: course.StatusDraft, OwnerID: owner}
	require.NoError(t, repo.CreateCourse(ctx, c))

	modules := []*course.Module{
		{ID: "m1", CourseID: c.ID, Title: "Basics", Position: 1},
		{ID: "m2", CourseID: c.ID, Title: "Advanced", Position: 2},
	}
	lessons := []*course.Lesson{
		{ID: "l1", ModuleID: "m1", Title: "Hello", Position: 1, Status: course.LessonLocked},
		{ID: "l2", ModuleID: "m1", Title: "Types", Position: 2, Status: course.LessonLocked},
		{ID: "l3", ModuleID: "m2", Title: "Traits", Position: 1, Status: course.LessonLocked},
	}
	require.NoError(t, repo.SubmitCurriculum(ctx, c.ID, modules, lessons))

	// Modules + lessons persisted.
	modList, err := repo.ListModules(ctx, c.ID)
	require.NoError(t, err)
	assert.Len(t, modList, 2)

	allLessons, err := repo.ListLessonsInCourse(ctx, c.ID)
	require.NoError(t, err)
	assert.Len(t, allLessons, 3)

	// Progress: 0 done, 3 total.
	prog, err := repo.Progress(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, prog.LessonsTotal)
	assert.Equal(t, 0, prog.LessonsDone)
}

func TestCourseRepo_Progress(t *testing.T) {
	db := setupUserDB(t)
	owner := setupCourseOwner(t, db)
	repo := NewCourseRepository(db)
	ctx := context.Background()

	c := &course.Course{Title: "X", Status: course.StatusDraft, OwnerID: owner}
	require.NoError(t, repo.CreateCourse(ctx, c))
	modules := []*course.Module{{ID: "m1", CourseID: c.ID, Title: "M", Position: 1}}
	lessons := []*course.Lesson{
		{ID: "l1", ModuleID: "m1", Title: "A", Position: 1, Status: course.LessonOpen},
		{ID: "l2", ModuleID: "m1", Title: "B", Position: 2, Status: course.LessonLocked},
	}
	require.NoError(t, repo.SubmitCurriculum(ctx, c.ID, modules, lessons))

	l1, err := repo.GetLesson(ctx, "l1")
	require.NoError(t, err)
	l1.Status = course.LessonDone
	require.NoError(t, repo.UpdateLesson(ctx, l1))

	prog, err := repo.Progress(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, prog.LessonsTotal)
	assert.Equal(t, 1, prog.LessonsDone)
}
